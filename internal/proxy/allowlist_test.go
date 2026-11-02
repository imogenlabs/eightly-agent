package proxy

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// sendTo runs one request through f and reports what reached the daemon.
func sendTo(t *testing.T, f *filter, method, target, body string) (*httptest.ResponseRecorder, *pathSink) {
	t.Helper()
	sink := &pathSink{}
	f.next = sink
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	rr := httptest.NewRecorder()
	f.ServeHTTP(rr, httptest.NewRequest(method, target, r))
	return rr, sink
}

type pathSink struct {
	sinkHandler
	path string
}

func (s *pathSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.path = r.URL.Path
	s.sinkHandler.ServeHTTP(w, r)
}

func refusedBy(t *testing.T, f *filter, method, target, body string) {
	t.Helper()
	rr, sink := sendTo(t, f, method, target, body)
	if rr.Code != http.StatusForbidden || sink.reached {
		t.Errorf("%s %s %s: got %d reached=%v, want refused (%s)", method, target, body, rr.Code, sink.reached, rr.Body.String())
	}
}

func forwardedBy(t *testing.T, f *filter, method, target, body string) *pathSink {
	t.Helper()
	rr, sink := sendTo(t, f, method, target, body)
	if rr.Code != http.StatusOK || !sink.reached {
		t.Errorf("%s %s %s: got %d reached=%v, want forwarded (%s)", method, target, body, rr.Code, sink.reached, rr.Body.String())
	}
	return sink
}

// Every call Eight.ly OS sends to a remote agent, with the agent at its
// defaults (no bind roots).
func TestAllowList_EightlyOSCallsPass(t *testing.T) {
	f := testFilter(nil, nil)
	for _, c := range []struct{ method, target, body string }{
		{http.MethodGet, "/containers/json?all=true", ""},
		{http.MethodGet, "/containers/app/json", ""},
		{http.MethodGet, "/containers/" + idApp + "/json", ""},
		{http.MethodPost, "/containers/app/start", ""},
		{http.MethodPost, "/containers/app/stop", ""},
		{http.MethodPost, "/containers/app/restart", ""},
		{http.MethodPost, "/containers/app/pause", ""},
		{http.MethodPost, "/containers/app/unpause", ""},
		{http.MethodPost, "/containers/app/kill", ""},
		{http.MethodDelete, "/containers/app?force=true", ""},
		{http.MethodGet, "/containers/app/stats", ""},
		{http.MethodGet, "/containers/app/logs?tail=100", ""},
		{http.MethodPost, "/containers/app/update", `{"restartPolicy":"always"}`},
		{http.MethodGet, "/images/json", ""},
		{http.MethodGet, "/images/sha256:abc/json", ""},
		{http.MethodGet, "/images/lscr.io/linuxserver/sonarr:latest/json", ""},
		{http.MethodPost, "/images/create?fromImage=nginx&tag=latest", ""},
		{http.MethodDelete, "/images/sha256:abc", ""},
		{http.MethodPost, "/images/prune", ""},
		{http.MethodGet, "/volumes", ""},
		{http.MethodGet, "/volumes/cache", ""},
		{http.MethodDelete, "/volumes/cache", ""},
		{http.MethodPost, "/volumes", `{"name":"data","driver":"local"}`},
		{http.MethodPost, "/volumes/prune", ""},
		{http.MethodGet, "/networks", ""},
		{http.MethodGet, "/networks/mynet", ""},
		{http.MethodDelete, "/networks/mynet", ""},
		{http.MethodDelete, "/networks/prune", ""},
		{http.MethodPost, "/networks", `{"name":"web","driver":"bridge"}`},
		{http.MethodGet, "/info", ""},
		// The dashboard console (remote_exec.go): an ordinary container that
		// binds only named volumes and the time zone file.
		{http.MethodPost, "/containers/eightly-ok/exec", `{"AttachStdin":true,"AttachStdout":true,"AttachStderr":true,"Tty":true,"Cmd":["/bin/sh","-c","exec /bin/bash 2>/dev/null || exec /bin/sh"]}`},
		{http.MethodPost, "/exec/exec-ok/start", `{"Detach":false,"Tty":true}`},
		// Standard reads and actions beyond what the dashboard sends today.
		{http.MethodGet, "/_ping", ""},
		{http.MethodHead, "/_ping", ""},
		{http.MethodGet, "/version", ""},
		{http.MethodGet, "/events?since=1", ""},
		{http.MethodGet, "/system/df", ""},
		{http.MethodGet, "/containers/app/top", ""},
		{http.MethodPost, "/containers/app/rename?name=x", ""},
		{http.MethodGet, "/exec/exec-app/json", ""},
		{http.MethodPost, "/images/nginx/tag?repo=x", ""},
		{http.MethodPost, "/networks/mynet/connect", `{"Container":"app"}`},
	} {
		forwardedBy(t, f, c.method, c.target, c.body)
	}
}

func init() {
	fakeContainers["eightly-ok"] = `{"Id":"` + strings.Repeat("9", 64) + `","HostConfig":{"NetworkMode":"bridge","CgroupnsMode":"private",
		"Binds":["data:/data","/etc/localtime:/etc/localtime:ro"]},
		"Mounts":[{"Type":"volume","Name":"data","Destination":"/data","RW":true},
		{"Type":"bind","Source":"/etc/localtime","Destination":"/etc/localtime","RW":false}]}`
	fakeExecs["exec-ok"] = execInfo{ContainerID: strings.Repeat("9", 64)}
}

// An endpoint not on the list is refused, whatever the method.
func TestAllowList_UnlistedEndpointsRefused(t *testing.T) {
	f := testFilter(nil, testRoots)
	for _, c := range []struct{ method, target string }{
		{http.MethodPost, "/build?networkmode=host"},
		{http.MethodPost, "/v1.45/build"},
		{http.MethodPost, "/grpc"},
		{http.MethodPost, "/session"},
		{http.MethodPost, "/commit?container=app"},
		{http.MethodGet, "/containers/app/export"},
		{http.MethodGet, "/containers/eightly-agent/archive?path=/data"},
		{http.MethodHead, "/containers/app/archive?path=/"},
		{http.MethodPost, "/images/load"},
		{http.MethodGet, "/images/get?names=x"},
		{http.MethodPost, "/images/nginx/push"},
		{http.MethodPost, "/auth"},
		{http.MethodGet, "/swarm"},
		{http.MethodGet, "/tasks"},
		{http.MethodGet, "/distribution/nginx/json"},
		{http.MethodPost, "/containers/app/stacks"},
		{http.MethodPut, "/containers/app/start"},
		{http.MethodGet, "/containers/app%2F..%2F..%2Fbuild"},
		{http.MethodPost, "/containers/app/../../build"},
	} {
		refusedBy(t, f, c.method, c.target, "")
	}
}

// C1: exec, attach and archive upload into a container that reaches the
// host, the agent's own above all, are refused.
func TestTarget_DangerousContainersRefused(t *testing.T) {
	f := testFilter(nil, testRoots)
	exec := `{"AttachStdin":true,"Tty":true,"User":"root","Cmd":["sh"]}`
	for _, name := range []string{
		"eightly-agent", idAgent, idAgent[:12], // the agent, by name, ID and short ID
		"priv", "hostnet", "joiner", "pidjoin", "caps", "etc", "device", "hostvol", "outside",
	} {
		refusedBy(t, f, http.MethodPost, "/containers/"+name+"/exec", exec)
		refusedBy(t, f, http.MethodPost, "/containers/"+name+"/attach?stream=1&stdin=1", "")
		refusedBy(t, f, http.MethodGet, "/containers/"+name+"/attach/ws", "")
		refusedBy(t, f, http.MethodPut, "/containers/"+name+"/archive?path=/", "tar")
	}
	for _, id := range []string{"exec-agent", "exec-priv", "exec-hostp"} {
		refusedBy(t, f, http.MethodPost, "/exec/"+id+"/start", `{"Detach":false,"Tty":true}`)
	}
	// The command line of a socket holder may carry the agent's secret.
	refusedBy(t, f, http.MethodGet, "/containers/eightly-agent/top", "")

	// The agent recognises itself by its hostname even without the socket.
	self := testFilter(nil, testRoots)
	self.selfIDs = []string{idApp[:12]}
	refusedBy(t, self, http.MethodPost, "/containers/app/exec", exec)
}

// The ID that was checked is the ID that reaches the daemon: a name can be
// handed to another container between the check and the forward.
func TestTarget_ForwardsTheCheckedID(t *testing.T) {
	f := testFilter(nil, testRoots)
	sink := forwardedBy(t, f, http.MethodPost, "/v1.45/containers/app/exec", `{"Cmd":["sh"]}`)
	if sink.path != "/containers/"+idApp+"/exec" {
		t.Errorf("forwarded path %q, want the full ID", sink.path)
	}
	sink = forwardedBy(t, f, http.MethodPut, "/containers/app/archive?path=/tmp", "tar")
	if sink.path != "/containers/"+idApp+"/archive" {
		t.Errorf("forwarded path %q, want the full ID", sink.path)
	}
	// app binds /opt/appdata/sonarr: without that root it is off limits.
	refusedBy(t, testFilter(nil, nil), http.MethodPost, "/containers/app/exec", `{"Cmd":["sh"]}`)
	// A container the daemon does not know is answered 404 here, never
	// forwarded by name for some other container to take that name first.
	for _, c := range []struct{ method, target, body string }{
		{http.MethodPost, "/containers/missing/exec", `{"Cmd":["sh"]}`},
		{http.MethodPost, "/containers/missing/attach", ""},
		{http.MethodPut, "/containers/missing/archive?path=/", "tar"},
		{http.MethodGet, "/containers/missing/top", ""},
		{http.MethodPost, "/containers/missing/start", ""},
		{http.MethodPost, "/containers/missing/stop", ""},
		{http.MethodPost, "/containers/missing/rename?name=x", ""},
		{http.MethodDelete, "/containers/missing", ""},
		{http.MethodPost, "/containers/missing/update", `{}`},
	} {
		rr, sink := sendTo(t, f, c.method, c.target, c.body)
		if rr.Code != http.StatusNotFound || sink.reached {
			t.Errorf("%s %s: got %d reached=%v, want 404 here", c.method, c.target, rr.Code, sink.reached)
		}
	}
	// Every checked call is pinned to the full ID, the ones without a
	// trailing segment included.
	for _, c := range []struct{ method, target, want string }{
		{http.MethodGet, "/containers/app/top", "/containers/" + idApp + "/top"},
		{http.MethodPost, "/containers/app/start", "/containers/" + idApp + "/start"},
		{http.MethodPost, "/containers/app/restart", "/containers/" + idApp + "/restart"},
		{http.MethodPost, "/containers/app/stop", "/containers/" + idApp + "/stop"},
		{http.MethodPost, "/containers/app/rename?name=x", "/containers/" + idApp + "/rename"},
		{http.MethodDelete, "/containers/app", "/containers/" + idApp},
	} {
		if sink := forwardedBy(t, f, c.method, c.target, ""); sink.path != c.want {
			t.Errorf("%s %s forwarded as %q, want %q", c.method, c.target, sink.path, c.want)
		}
	}
}

// C2: VolumesFrom copies another container's mounts, the socket included.
func TestCreate_VolumesFromRefused(t *testing.T) {
	f := testFilter(nil, testRoots)
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"alpine","HostConfig":{"VolumesFrom":["eightly-agent"]}}`)
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"alpine","HostConfig":{"VolumesFrom":"app:ro"}}`)
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"alpine","VolumesFrom":["eightly-agent"]}`)
}

// C3: the host paths the deny-list missed, and symlinks under a root.
func TestCreate_HostPathsOutsideRootsRefused(t *testing.T) {
	f := testFilter(nil, testRoots)
	for _, src := range []string{"/usr", "/usr/lib", "/bin", "/sbin", "/lib", "/lib64", "/opt", "/opt/containerd", "/var/spool/cron", "/var/log", "/tmp", "/tmp/x"} {
		refusedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Binds":["`+src+`:/h"]}}`)
		refusedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Mounts":[{"Type":"bind","Source":"`+src+`","Target":"/h"}]}}`)
	}
	forwardedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Binds":["/opt/appdata/sonarr:/config","/mnt/user/media:/tv:ro","/etc/localtime:/etc/localtime:ro"]}}`)
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Binds":["/etc/localtime:/etc/localtime"]}}`)
	refusedBy(t, testFilter(nil, nil), http.MethodPost, "/containers/create", `{"HostConfig":{"Binds":["/opt/appdata/sonarr:/config"]}}`)
	// Shared propagation would push mounts made in the container to the host.
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Binds":["/srv/x:/x:rshared"]}}`)
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Mounts":[{"Type":"bind","Source":"/srv/x","Target":"/x","BindOptions":{"Propagation":"shared"}}]}}`)
	// Mount types other than bind, volume and tmpfs.
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Mounts":[{"Type":"npipe","Source":"\\\\.\\pipe\\docker_engine","Target":"/p"}]}}`)
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Mounts":[{"Type":"cluster","Source":"x","Target":"/p"}]}}`)
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Mounts":[{"Source":"/","Target":"/p"}]}}`)
}

func TestCreate_SymlinkUnderRootRefused(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("host bind paths and the symlink check apply to Linux hosts")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "app", "real"), 0o750); err != nil {
		t.Fatal(err)
	}
	// What a container that binds root/app can plant: a link to the host's /.
	if err := os.Symlink("/", filepath.Join(root, "app", "x")); err != nil {
		t.Fatal(err)
	}
	f := testFilter(nil, []string{root})
	f.hostFS, f.lstat = true, os.Lstat
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Binds":["`+root+`/app/x:/host"]}}`)
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Binds":["`+root+`/app/x/etc:/host"]}}`)
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Mounts":[{"Type":"bind","Source":"`+root+`/app/x","Target":"/h"}]}}`)
	forwardedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Binds":["`+root+`/app/real:/data","`+root+`/app/new/dir:/new"]}}`)

	// The link planted after create is caught when the container starts,
	// which is when the daemon mounts it.
	fakeContainers["planted"] = `{"Id":"` + strings.Repeat("6", 64) + `","HostConfig":{},"Mounts":[{"Type":"bind","Source":"` + root + `/app/x","Destination":"/h","RW":true}]}`
	t.Cleanup(func() { delete(fakeContainers, "planted") })
	refusedBy(t, f, http.MethodPost, "/containers/planted/start", "")
	refusedBy(t, f, http.MethodPost, "/containers/planted/restart", "")
	// The start goes out under the ID that was checked, so renaming the
	// planted container into a checked name between check and use fails.
	if sink := forwardedBy(t, f, http.MethodPost, "/containers/app/start", ""); sink.path != "/containers/"+idApp+"/start" {
		t.Errorf("start forwarded as %q, want the full ID", sink.path)
	}
}

// M1: namespace joins, capabilities, devices, runtimes and the rest.
func TestCreate_EscalationsRefused(t *testing.T) {
	f := testFilter(nil, testRoots)
	for name, hc := range map[string]string{
		"network container":   `"NetworkMode":"container:` + idHost + `"`,
		"network host by ID":  `"NetworkMode":"hostnetworkid"`,
		"pid container":       `"PidMode":"container:app"`,
		"ipc container":       `"IpcMode":"container:app"`,
		"ipc host":            `"IpcMode":"host"`,
		"uts host":            `"UTSMode":"host"`,
		"userns host":         `"UsernsMode":"host"`,
		"cgroup join":         `"Cgroup":"container:app"`,
		"cgroupns host":       `"CgroupnsMode":"host"`,
		"cap BPF":             `"CapAdd":["BPF"]`,
		"cap PERFMON":         `"CapAdd":["CAP_PERFMON"]`,
		"cap SYS_RESOURCE":    `"CapAdd":["SYS_RESOURCE"]`,
		"cap SYSLOG":          `"CapAdd":["SYSLOG"]`,
		"cap NET_ADMIN":       `"CapAdd":["NET_ADMIN"]`,
		"cap SYS_TIME":        `"CapAdd":"SYS_TIME"`,
		"cap DAC_READ_SEARCH": `"CapAdd":["dac_read_search"]`,
		"gpu request":         `"DeviceRequests":[{"Driver":"nvidia","Count":-1,"Capabilities":[["gpu"]]}]`,
		"cdi request":         `"DeviceRequests":[{"Driver":"cdi","DeviceIDs":["vendor.com/device=all"]}]`,
		"runtime":             `"Runtime":"nvidia"`,
		"annotations":         `"Annotations":{"run.oci.keep_original_groups":"1"}`,
		"oom kill disable":    `"OomKillDisable":true`,
		"volume driver":       `"VolumeDriver":"local-persist"`,
		"cgroup parent":       `"CgroupParent":"/"`,
	} {
		refusedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"alpine","HostConfig":{`+hc+`}}`)
		if strings.HasPrefix(name, "cap") || name == "network container" {
			refusedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"alpine",`+hc+`}`)
		}
	}
	// The host network named in NetworkingConfig rather than NetworkMode.
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"alpine","NetworkingConfig":{"EndpointsConfig":{"host":{}}}}`)
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"alpine","NetworkingConfig":{"EndpointsConfig":{"hostnetworkid":{}}}}`)
	refusedBy(t, f, http.MethodPost, "/networks/host/connect", `{"Container":"app"}`)
	refusedBy(t, f, http.MethodPost, "/networks/hostnetworkid/connect", `{"Container":"app"}`)
	for _, driver := range []string{"host", "null", "overlay", "weave", "evil/plugin"} {
		refusedBy(t, f, http.MethodPost, "/networks/create", `{"Name":"n","Driver":"`+driver+`"}`)
	}
	refusedBy(t, f, http.MethodPost, "/images/create?fromSrc=http://169.254.169.254/x&repo=x", "")
	refusedBy(t, f, http.MethodPost, "/images/create?fromSrc=-", "tarball")
	// Updates may not add devices, OOM immunity or a cgroup parent either.
	refusedBy(t, f, http.MethodPost, "/containers/app/update", `{"DeviceRequests":[{"Driver":"cdi"}]}`)
	refusedBy(t, f, http.MethodPost, "/containers/app/update", `{"OomKillDisable":true}`)

	// What stays allowed: Docker's default capabilities, private namespaces,
	// a user network, a pids limit of -1 (the daemon's own default).
	forwardedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"alpine","HostConfig":{
		"CapDrop":["ALL"],"CapAdd":["CHOWN","SETUID","SETGID","NET_BIND_SERVICE","KILL","DAC_OVERRIDE","FOWNER","NET_RAW"],
		"IpcMode":"shareable","PidMode":"","CgroupnsMode":"private","NetworkMode":"mynet","PidsLimit":-1,
		"Runtime":"runc","OomKillDisable":false,"Init":true}}`)
	forwardedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"alpine","NetworkingConfig":{"EndpointsConfig":{"mynet":{}}}}`)
	forwardedBy(t, f, http.MethodPost, "/networks/create", `{"Name":"n","Driver":"macvlan"}`)
}

// Named volumes made outside the agent with local bind options are held to
// the bind policy.
func TestNamedVolumes_BindPolicy(t *testing.T) {
	f := testFilter(nil, testRoots)
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Binds":["hostroot:/host"]}}`)
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Mounts":[{"Type":"volume","Source":"hostroot","Target":"/host"}]}}`)
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Binds":["persist:/p"]}}`)
	forwardedBy(t, f, http.MethodPost, "/containers/create", `{"HostConfig":{"Binds":["appbind:/shared","cache:/cache","brand-new:/n"]}}`)
	refusedBy(t, testFilter(nil, nil), http.MethodPost, "/containers/create", `{"HostConfig":{"Binds":["appbind:/shared"]}}`)

	// The same rule for /volumes/create: a bind under a root is fine now.
	forwardedBy(t, f, http.MethodPost, "/volumes/create", `{"Name":"x","DriverOpts":{"type":"none","o":"bind","device":"/srv/shared"}}`)
	refusedBy(t, f, http.MethodPost, "/volumes/create", `{"Name":"x","DriverOpts":{"type":"none","o":"bind","device":"/etc"}}`)
	refusedBy(t, f, http.MethodPost, "/volumes", `{"Name":"x","DriverOpts":{"type":"ext4","device":"/srv/disk.img"}}`)
}

func TestRedact_ContainerInspect(t *testing.T) {
	agent := `{"Id":"` + idAgent + `","Args":["--secret","s3cret","--port=9001"],"Config":{"Env":["EIGHTLY_AGENT_SECRET=s3cret","DOCKER_GID=999","PATH=/bin"],
		"Cmd":["--enroll-token=t0k3n"]},"Mounts":[{"Type":"bind","Source":"/var/run/docker.sock","Destination":"/var/run/docker.sock"}],"SizeRw":12345678901}`
	out, err := redactContainerJSON([]byte(agent), func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"s3cret", "999", "/bin", "t0k3n"} {
		if strings.Contains(string(out), leak) {
			t.Errorf("socket holder's inspect still carries %q: %s", leak, out)
		}
	}
	if !strings.Contains(string(out), "12345678901") || !strings.Contains(string(out), "--port=9001") {
		t.Errorf("redaction changed fields it should not: %s", out)
	}

	app := `{"Id":"` + idApp + `","Config":{"Env":["eightly_token=abc","TZ=UTC","EIGHTLY_X=y"]},"Mounts":[]}`
	out, err = redactContainerJSON([]byte(app), func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Config struct{ Env []string } }
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"eightly_token=" + redacted, "TZ=UTC", "EIGHTLY_X=" + redacted}
	if strings.Join(got.Config.Env, " ") != strings.Join(want, " ") {
		t.Errorf("Env = %v, want %v", got.Config.Env, want)
	}

	// The agent's own container by its ID, even without a socket mount.
	out, _ = redactContainerJSON([]byte(app), func(id string) bool { return id == idApp })
	if strings.Contains(string(out), "UTC") {
		t.Errorf("own container's Env not redacted: %s", out)
	}
}

// End to end: the inspect response from the daemon reaches the caller
// redacted, and other responses are untouched.
func TestRedact_ThroughTheProxy(t *testing.T) {
	dir, err := os.MkdirTemp(socketBase(), "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	agent := `{"Id":"` + idAgent + `","Config":{"Env":["EIGHTLY_AGENT_SECRET=s3cret"]},"Mounts":[{"Type":"bind","Source":"/var/run/docker.sock","Destination":"/var/run/docker.sock"}],"HostConfig":{}}`
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, agent)
	}))
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	h := NewDockerProxy(sock, nil)
	for _, target := range []string{"/containers/eightly-agent/json", "/v1.45/containers/" + idAgent + "/json?size=1"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
		if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "s3cret") {
			t.Errorf("%s: %d %s", target, rr.Code, rr.Body.String())
		}
		if rr.Header().Get("Content-Length") != "" && rr.Header().Get("Content-Length") != itoa(rr.Body.Len()) {
			t.Errorf("%s: Content-Length %s for a %d-byte body", target, rr.Header().Get("Content-Length"), rr.Body.Len())
		}
	}
	// Logs are not inspect output and pass byte for byte.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/containers/app/logs?stdout=1", nil))
	if rr.Body.String() != agent {
		t.Errorf("logs response changed: %s", rr.Body.String())
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
