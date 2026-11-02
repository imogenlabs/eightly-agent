package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func send(t *testing.T, method, target, body string) (*httptest.ResponseRecorder, *sinkHandler) {
	t.Helper()
	sink := &sinkHandler{}
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	rr := httptest.NewRecorder()
	testFilter(sink, testRoots).ServeHTTP(rr, httptest.NewRequest(method, target, r))
	return rr, sink
}

func wantRefused(t *testing.T, name string, rr *httptest.ResponseRecorder, sink *sinkHandler, code int) {
	t.Helper()
	if rr.Code != code {
		t.Errorf("%s: got %d, want %d (body %q)", name, rr.Code, code, rr.Body.String())
	}
	if sink.reached {
		t.Errorf("%s: request reached the daemon", name)
	}
}

func wantForwarded(t *testing.T, name string, rr *httptest.ResponseRecorder, sink *sinkHandler) {
	t.Helper()
	if rr.Code != http.StatusOK || !sink.reached {
		t.Errorf("%s: not forwarded: %d %q", name, rr.Code, rr.Body.String())
	}
}

// What Eight.ly OS and its users legitimately send must keep working.
func TestFilter_EightlyTrafficForwarded(t *testing.T) {
	cases := []struct{ name, method, target, body string }{
		{"typical create", http.MethodPost, "/v1.45/containers/create?name=web", `{
			"Image":"lscr.io/linuxserver/sonarr:latest",
			"Env":["PUID=1000","PGID=1000","TZ=Europe/London"],
			"Labels":{"eightly.app":"sonarr"},
			"ExposedPorts":{"8989/tcp":{}},
			"HostConfig":{
				"PortBindings":{"8989/tcp":[{"HostPort":"8989"}]},
				"RestartPolicy":{"Name":"unless-stopped"},
				"Binds":["sonarr-config:/config","/mnt/user/appdata/sonarr:/data","/opt/appdata/x:/x:ro","/mnt/user/media:/tv:rw"],
				"Mounts":[{"Type":"volume","Source":"cache","Target":"/cache"},{"Type":"bind","Source":"/srv/downloads","Target":"/downloads"}],
				"CapAdd":["NET_BIND_SERVICE"],
				"CapDrop":"ALL",
				"NetworkMode":"bridge",
				"MaskedPaths":null
			}}`},
		{"lowercase keys", http.MethodPost, "/containers/create", `{"image":"nginx","hostConfig":{"restartPolicy":{"name":"always"},"binds":["/mnt/user/appdata/x:/x"]}}`},
		{"cap as string", http.MethodPost, "/containers/create", `{"HostConfig":{"CapAdd":"CHOWN"}}`},
		{"exec shell", http.MethodPost, "/containers/app/exec", `{"AttachStdin":true,"AttachStdout":true,"AttachStderr":true,"Tty":true,"Cmd":["/bin/sh","-c","exec /bin/bash"]}`},
		{"exec privileged false", http.MethodPost, "/containers/app/exec", `{"Privileged":false,"Cmd":["ls"]}`},
		{"exec start", http.MethodPost, "/exec/exec-app/start", `{"Detach":false,"Tty":true}`},
		{"update restart policy", http.MethodPost, "/containers/abc/update", `{"restartPolicy":{"Name":"always"}}`},
		{"volume create", http.MethodPost, "/volumes/create", `{"name":"data","driver":"local"}`},
		{"volume create short path", http.MethodPost, "/volumes", `{"name":"data","driver":"local"}`},
		{"nfs volume", http.MethodPost, "/volumes/create", `{"Name":"media","Driver":"local","DriverOpts":{"type":"nfs","o":"addr=10.0.0.5,rw","device":":/export/media"}}`},
		{"cifs volume", http.MethodPost, "/volumes/create", `{"Name":"share","DriverOpts":{"type":"cifs","o":"username=u","device":"//nas/share"}}`},
		{"tmpfs volume", http.MethodPost, "/volumes/create", `{"Name":"scratch","DriverOpts":{"type":"tmpfs","o":"size=100m"}}`},
		{"image pull", http.MethodPost, "/images/create?fromImage=nginx&tag=latest", ""},
		{"start", http.MethodPost, "/containers/abc/start", ""},
		{"empty create body", http.MethodPost, "/containers/create", ""},
	}
	for _, c := range cases {
		rr, sink := send(t, c.method, c.target, c.body)
		wantForwarded(t, c.name, rr, sink)
	}
}

// Bodies the filter cannot interpret used to be forwarded untouched.
func TestFilter_UninterpretableBodyRefused(t *testing.T) {
	cases := map[string]string{
		"privileged string":      `{"Privileged":"x","HostConfig":{"Privileged":true}}`,
		"hostconfig privileged":  `{"HostConfig":{"Privileged":"true"}}`,
		"capadd number":          `{"HostConfig":{"CapAdd":1}}`,
		"hostconfig number":      `{"HostConfig":1}`,
		"binds object":           `{"HostConfig":{"Binds":{"/":"/host"}}}`,
		"trailing second object": `{"Image":"a"}{"HostConfig":{"Privileged":true}}`,
		"not json":               `not json`,
	}
	for name, body := range cases {
		rr, sink := send(t, http.MethodPost, "/containers/create", body)
		wantRefused(t, name, rr, sink, http.StatusForbidden)
	}
	rr, sink := send(t, http.MethodPost, "/containers/abc/exec", `{"Privileged":"yes"}`)
	wantRefused(t, "exec privileged string", rr, sink, http.StatusForbidden)
	rr, sink = send(t, http.MethodPost, "/volumes/create", `{"DriverOpts":"device=/etc"}`)
	wantRefused(t, "volume opts string", rr, sink, http.StatusForbidden)
}

func TestFilter_CapAddStringRefused(t *testing.T) {
	rr, sink := send(t, http.MethodPost, "/containers/create", `{"HostConfig":{"CapAdd":"SYS_ADMIN"}}`)
	wantRefused(t, "CapAdd string", rr, sink, http.StatusForbidden)
}

// Older daemons read host-config fields at the top level when HostConfig is absent.
func TestFilter_TopLevelHostConfigRefused(t *testing.T) {
	cases := map[string]string{
		"privileged": `{"Image":"alpine","Privileged":true}`,
		"binds":      `{"Image":"alpine","Binds":["/:/host"]}`,
		"pid host":   `{"Image":"alpine","PidMode":"host"}`,
		"capadd":     `{"Image":"alpine","CapAdd":["SYS_ADMIN"]}`,
	}
	for name, body := range cases {
		rr, sink := send(t, http.MethodPost, "/containers/create", body)
		wantRefused(t, name, rr, sink, http.StatusForbidden)
	}
}

func TestFilter_BindSourcePolicy(t *testing.T) {
	f := testFilter(nil, testRoots)
	refused := []string{
		"/", "//", "/./", "/etc", "/etc/", "//etc", "/etc/../etc/shadow", "/tmp/../etc",
		"/mnt/../../root", "/var/run", "/var/run/docker.sock", "/run", "/run/containerd",
		"/var/lib/docker", "/var/lib/docker/volumes", "/home", "/home/michael", "/boot",
		"/dev", "/dev/sda", "/proc/1/root", "/sys/kernel", "/var", "/var/lib",
		// What the old deny-list missed (C3).
		"/usr", "/usr/local/bin", "/bin", "/sbin", "/lib", "/lib64", "/opt", "/var/spool/cron",
		// A root's prefix is not the root, ".." is never accepted, nor is a
		// socket under a root, nor a relative or Windows path.
		"/srvfoo", "/mnt/users", "/opt/appdata/../../etc", "/srv/a/../b", "/srv/docker.sock",
		"mydata", "../etc", "", `C:\data`, "/srv/x\\y",
		// The time zone files only read-only.
		"/etc/localtime",
	}
	for _, p := range refused {
		if f.checkBindSource(p, false) == "" {
			t.Errorf("checkBindSource(%q) allowed it", p)
		}
	}
	allowed := []string{"/mnt/user/appdata/x", "/opt/appdata/x", "/srv", "/srv/data/", "/mnt/user"}
	for _, p := range allowed {
		if reason := f.checkBindSource(p, false); reason != "" {
			t.Errorf("checkBindSource(%q) = %q, want allowed", p, reason)
		}
	}
	if reason := f.checkBindSource("/etc/localtime", true); reason != "" {
		t.Errorf("read-only /etc/localtime refused: %s", reason)
	}
	// No roots: no host path at all.
	none := testFilter(nil, nil)
	if none.checkBindSource("/opt/appdata/x", false) == "" {
		t.Error("with no roots configured a host bind was allowed")
	}
}

func TestFilter_HostPathBindsRefused(t *testing.T) {
	cases := map[string]string{
		"dotdot bind":    `{"HostConfig":{"Binds":["/tmp/../etc:/x"]}}`,
		"double slash":   `{"HostConfig":{"Binds":["//:/host"]}}`,
		"parent of sock": `{"HostConfig":{"Binds":["/var:/v"]}}`,
		"home":           `{"HostConfig":{"Binds":["/home/user:/h"]}}`,
		"dev":            `{"HostConfig":{"Binds":["/dev:/dev"]}}`,
		"mount dotdot":   `{"HostConfig":{"Mounts":[{"Type":"bind","Source":"/mnt/../run","Target":"/r"}]}}`,
		"mount boot":     `{"HostConfig":{"Mounts":[{"Type":"bind","Source":"/boot","Target":"/b"}]}}`,
	}
	for name, body := range cases {
		rr, sink := send(t, http.MethodPost, "/containers/create", body)
		wantRefused(t, name, rr, sink, http.StatusForbidden)
	}
}

func TestFilter_SwarmAndPluginEndpointsRefused(t *testing.T) {
	cases := []struct{ method, target string }{
		{http.MethodGet, "/secrets"},
		{http.MethodGet, "/v1.45/plugins"},
		{http.MethodGet, "/services"},
		{http.MethodGet, "/nodes"},
		{http.MethodPost, "/plugins/pull?remote=evil/plugin"},
		{http.MethodPost, "/v1.45/plugins/evil/enable"},
		{http.MethodPost, "/plugins/create?name=x"},
		{http.MethodDelete, "/plugins/x"},
		{http.MethodPost, "/swarm/init"},
		{http.MethodPost, "/swarm/join"},
		{http.MethodGet, "/swarm"},
		{http.MethodGet, "/swarm/unlockkey"},
		{http.MethodPost, "/services/create"},
		{http.MethodPost, "/services/abc/update"},
		{http.MethodPost, "/configs/create"},
		{http.MethodPost, "/secrets/create"},
		{http.MethodDelete, "/secrets/abc"},
		{http.MethodPost, "/nodes/abc/update"},
		{http.MethodDelete, "/nodes/abc"},
	}
	for _, c := range cases {
		rr, sink := send(t, c.method, c.target, "{}")
		wantRefused(t, c.method+" "+c.target, rr, sink, http.StatusForbidden)
	}
}

func TestFilter_VolumeDriverOptsRefused(t *testing.T) {
	cases := map[string]string{
		"bind":            `{"Name":"x","Driver":"local","DriverOpts":{"type":"none","o":"bind","device":"/etc"}}`,
		"rbind safe path": `{"Name":"x","DriverOpts":{"type":"none","o":"rbind","device":"/mnt/x"}}`,
		"block device":    `{"Name":"x","DriverOpts":{"type":"ext4","device":"/dev/sda1"}}`,
		"overlay":         `{"Name":"x","DriverOpts":{"type":"overlay","o":"lowerdir=/etc,upperdir=/u,workdir=/w"}}`,
		"device only":     `{"Name":"x","DriverOpts":{"device":"/root"}}`,
		"nfs local dev":   `{"Name":"x","DriverOpts":{"type":"nfs","device":"/etc"}}`,
		"plugin driver":   `{"Name":"x","Driver":"local-persist","DriverOpts":{"mountpoint":"/etc"}}`,
		"lowercase keys":  `{"name":"x","driverOpts":{"Type":"none","O":"bind","Device":"/etc"}}`,
	}
	for name, body := range cases {
		rr, sink := send(t, http.MethodPost, "/volumes/create", body)
		wantRefused(t, name, rr, sink, http.StatusForbidden)
	}
	mounts := map[string]string{
		"mount volume bind":   `{"HostConfig":{"Mounts":[{"Type":"volume","Source":"v","Target":"/v","VolumeOptions":{"DriverConfig":{"Name":"local","Options":{"type":"none","o":"bind","device":"/"}}}}]}}`,
		"mount volume device": `{"HostConfig":{"Mounts":[{"Type":"volume","Target":"/v","VolumeOptions":{"DriverConfig":{"Options":{"type":"xfs","device":"/dev/nvme0n1"}}}}]}}`,
		"mount tmpfs over":    `{"HostConfig":{"Mounts":[{"Type":"tmpfs","Target":"/proc"}]}}`,
	}
	for name, body := range mounts {
		rr, sink := send(t, http.MethodPost, "/containers/create", body)
		wantRefused(t, name, rr, sink, http.StatusForbidden)
	}
}

func TestFilter_SandboxOverridesRefused(t *testing.T) {
	cases := map[string]string{
		"device cgroup rule":    `{"HostConfig":{"DeviceCgroupRules":["c *:* rmw"]}}`,
		"device cgroup string":  `{"HostConfig":{"DeviceCgroupRules":"b 8:* rmw"}}`,
		"masked paths empty":    `{"HostConfig":{"MaskedPaths":[]}}`,
		"masked paths set":      `{"HostConfig":{"MaskedPaths":["/proc/acpi"]}}`,
		"readonly paths empty":  `{"HostConfig":{"ReadonlyPaths":[]}}`,
		"cgroupns host":         `{"HostConfig":{"CgroupnsMode":"host"}}`,
		"uts host":              `{"HostConfig":{"UTSMode":"host"}}`,
		"userns host uppercase": `{"HostConfig":{"UsernsMode":"HOST"}}`,
	}
	for name, body := range cases {
		rr, sink := send(t, http.MethodPost, "/containers/create", body)
		wantRefused(t, name, rr, sink, http.StatusForbidden)
	}
	update := map[string]string{
		"update devices":       `{"Devices":[{"PathOnHost":"/dev/sda","PathInContainer":"/dev/sda","CgroupPermissions":"rwm"}]}`,
		"update cgroup rules":  `{"DeviceCgroupRules":["a *:* rwm"]}`,
		"update cgroup parent": `{"CgroupParent":"/"}`,
	}
	for name, body := range update {
		rr, sink := send(t, http.MethodPost, "/containers/abc/update", body)
		wantRefused(t, name, rr, sink, http.StatusForbidden)
	}
	rr, sink := send(t, http.MethodPost, "/containers/create", `{"HostConfig":{"CgroupnsMode":"private","UTSMode":""}}`)
	wantForwarded(t, "cgroupns private", rr, sink)
}

func TestFilter_OversizedBodyRefused(t *testing.T) {
	// Padding after a safe prefix: the old filter read the first 1 MiB,
	// found nothing, and forwarded the whole body.
	big := `{"Image":"alpine","Labels":{"pad":"` + strings.Repeat("a", maxInspectBody) + `"},"HostConfig":{"Privileged":true}}`
	rr, sink := send(t, http.MethodPost, "/containers/create", big)
	wantRefused(t, "oversized create", rr, sink, http.StatusRequestEntityTooLarge)

	// Same body with no Content-Length (chunked): caught by the read limit.
	sink = &sinkHandler{}
	req := httptest.NewRequest(http.MethodPost, "/containers/create", io.MultiReader(strings.NewReader(big)))
	req.ContentLength = -1
	rr = httptest.NewRecorder()
	testFilter(sink, testRoots).ServeHTTP(rr, req)
	wantRefused(t, "oversized chunked create", rr, sink, http.StatusRequestEntityTooLarge)

	// Exactly at the limit still goes through.
	pad := maxInspectBody - len(`{"Image":"alpine","Labels":{"pad":""}}`)
	ok := `{"Image":"alpine","Labels":{"pad":"` + strings.Repeat("a", pad) + `"}}`
	rr, sink = send(t, http.MethodPost, "/containers/create", ok)
	wantForwarded(t, "body at the limit", rr, sink)
}

func TestFilter_PathTricksStillInspected(t *testing.T) {
	body := `{"HostConfig":{"Privileged":true}}`
	for _, target := range []string{
		"/v1.45/v1.45/containers/create",
		"/v1.45/v1.41/v1.24/containers/create",
		"/containers//create",
		"/containers/./create",
		"/images/../containers/create",
		"/v1.45/containers/abc/../create",
		"/containers%2Fcreate",
	} {
		rr, sink := send(t, http.MethodPost, target, body)
		wantRefused(t, target, rr, sink, http.StatusForbidden)
	}
}

func TestDockerPath(t *testing.T) {
	cases := map[string]string{
		"/v1.45/containers/create":       "/containers/create",
		"/v1.45/v1.45/containers/create": "/containers/create",
		"/containers//create":            "/containers/create",
		"/v1.41/images":                  "/images/json",
		"/containers/abc/logs":           "/containers/abc/logs",
		"/v1.45":                         "/",
	}
	for in, want := range cases {
		if got := dockerPath(in); got != want {
			t.Errorf("dockerPath(%q) = %q, want %q", in, got, want)
		}
	}
}
