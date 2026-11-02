package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func init() {
	// A container made on the host that mounts the agent's own volume.
	fakeContainers["thief"] = `{"Id":"` + strings.Repeat("6", 64) + `","HostConfig":{},
		"Mounts":[{"Type":"volume","Name":"agent-data","Destination":"/steal","RW":true}]}`
	// A container whose volume is bound to a path under a root.
	fakeContainers["volbind"] = `{"Id":"` + strings.Repeat("7", 64) + `","HostConfig":{},
		"Mounts":[{"Type":"volume","Name":"appbind","Destination":"/v","RW":true}]}`
}

// Codex 1: Go's decoder takes the last of two keys that match without
// regard to case. A null leaves Docker's bool true where it cleared the
// filter's pointer, so a body with a key twice is refused outright.
func TestBody_DuplicateKeysRefused(t *testing.T) {
	f := testFilter(nil, testRoots)
	for _, body := range []string{
		`{"Image":"alpine","HostConfig":{"Privileged":true,"Privileged":null}}`,
		`{"Image":"alpine","HostConfig":{"Privileged":true,"privileged":null}}`,
		`{"Image":"alpine","HostConfig":{"PRIVILEGED":true,"Privileged":false}}`,
		`{"Image":"alpine","HostConfig":{},"hostconfig":{"Privileged":true}}`,
		`{"Image":"alpine","HostConfig":{"Mounts":[{"Type":"volume","Source":"cache","type":"bind","Target":"/x"}]}}`,
		`{"Image":"alpine","HostConfig":{"PidMode":"host","PidMode":""}}`,
		// The Kelvin sign folds to k, as it does for the decoder.
		"{\"Image\":\"alpine\",\"HostConfig\":{\"MaskedPaths\":null,\"Mas\u212AedPaths\":null}}",
		`{"Image":"alpine"} {"Image":"x"}`,
	} {
		refusedBy(t, f, http.MethodPost, "/containers/create", body)
	}
	refusedBy(t, f, http.MethodPost, "/containers/app/update", `{"Memory":1,"memory":2}`)
	refusedBy(t, f, http.MethodPost, "/volumes/create", `{"Driver":"local","driver":"x"}`)
	refusedBy(t, f, http.MethodPost, "/containers/app/exec", `{"Privileged":true,"privileged":null,"Cmd":["sh"]}`)
	if foldKey("\u212Aey") != foldKey("key") || foldKey("Key") != foldKey("KEY") {
		t.Error("foldKey does not fold as strings.EqualFold does")
	}
	// A null on its own is Docker's false too, and goes through.
	forwardedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"alpine","HostConfig":{"Privileged":null}}`)
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"alpine","HostConfig":{"Privileged":true}}`)
}

// Codex 2 / Opus O5: the agent's own volume is off limits to every other
// container, and so is a host path holding its keys or state.
func TestSelf_VolumesAndPathsProtected(t *testing.T) {
	f := testFilter(nil, testRoots)
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"alpine","HostConfig":{"Binds":["agent-data:/steal"]}}`)
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"alpine","HostConfig":{"Mounts":[{"Type":"volume","Source":"agent-data","Target":"/steal"}]}}`)
	refusedBy(t, f, http.MethodPost, "/containers/thief/exec", `{"Cmd":["sh"]}`)
	forwardedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"alpine","HostConfig":{"Binds":["cache:/cache"]}}`)

	// On the host, the agent's files are protected by path.
	host := testFilter(nil, testRoots)
	host.hostFS, host.lstat = true, func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist }
	ProtectPaths("/srv/agent/eightly-agent.json", "/srv/agent/key.pem", "")(host)
	for _, src := range []string{"/srv", "/srv/agent", "/srv/agent/key.pem"} {
		refusedBy(t, host, http.MethodPost, "/containers/create", `{"Image":"a","HostConfig":{"Binds":["`+src+`:/x"]}}`)
	}
	refusedBy(t, host, http.MethodPost, "/volumes/create", `{"Driver":"local","DriverOpts":{"type":"none","o":"bind","device":"/srv/agent"}}`)
	forwardedBy(t, host, http.MethodPost, "/containers/create", `{"Image":"a","HostConfig":{"Binds":["/srv/agent/binds/v:/x"]}}`)

	// The agent's own bind sources are protected the same way when it
	// runs in a container.
	self := testFilter(nil, testRoots)
	fakeContainers["eightly-agent-bound"] = `{"Id":"` + strings.Repeat("8", 64) + `","HostConfig":{},
		"Mounts":[{"Type":"bind","Source":"/srv/eightly","Destination":"/data","RW":true}]}`
	self.selfIDs = []string{strings.Repeat("8", 64)}
	refusedBy(t, self, http.MethodPost, "/containers/create", `{"Image":"a","HostConfig":{"Binds":["/srv/eightly/key.pem:/k"]}}`)

	// The file browser asks the same question of a volume.
	tc := &TargetChecker{f: testFilter(nil, testRoots)}
	for name, want := range map[string]int{"agent-data": http.StatusForbidden, "hostroot": http.StatusForbidden, "missing": http.StatusNotFound, "cache": 0, "appbind": 0} {
		if got, reason := tc.Volume(context.Background(), name); got != want {
			t.Errorf("Volume(%s) = %d %q, want %d", name, got, reason, want)
		}
	}
	down := &TargetChecker{f: &filter{roots: testRoots, daemon: failingDaemon{}, selfIDs: []string{"x"}}}
	if got, _ := down.Volume(context.Background(), "cache"); got != http.StatusServiceUnavailable {
		t.Errorf("Volume with the daemon down = %d, want 503", got)
	}
	if full, reason := down.Container(context.Background(), "app"); full != "" || reason == "" {
		t.Errorf("Container with the daemon down = (%q, %q), want refused", full, reason)
	}
	missing := &TargetChecker{f: testFilter(nil, testRoots)}
	if full, reason := missing.Container(context.Background(), "missing"); full != "" || reason != "" {
		t.Errorf("Container(missing) = (%q, %q), want both empty for a 404", full, reason)
	}
	if reason := missing.Exec(context.Background(), "nope"); !strings.Contains(reason, "no such exec") {
		t.Errorf("Exec(nope) = %q, want refused", reason)
	}
	if reason := missing.Exec(context.Background(), "exec-ok"); reason != "" {
		t.Errorf("Exec(exec-ok) = %q, want allowed", reason)
	}
}

// Opus O7: the agent's own container cannot be stopped, killed, paused,
// removed, updated, restarted or renamed through itself; a socket holder
// cannot be renamed.
func TestSelf_ContainerOffLimits(t *testing.T) {
	f := testFilter(nil, testRoots)
	for _, c := range []struct{ method, target, body string }{
		{http.MethodPost, "/containers/eightly-agent/stop", ""},
		{http.MethodPost, "/containers/eightly-agent/kill", ""},
		{http.MethodPost, "/containers/eightly-agent/pause", ""},
		{http.MethodPost, "/containers/eightly-agent/restart", ""},
		{http.MethodPost, "/containers/eightly-agent/rename?name=x", ""},
		{http.MethodPost, "/containers/eightly-agent/update", `{"Memory":6291456}`},
		{http.MethodDelete, "/containers/eightly-agent?force=true", ""},
		{http.MethodDelete, "/containers/" + idAgent[:12], ""},
	} {
		refusedBy(t, f, c.method, c.target, c.body)
	}
	fakeContainers["sockholder"] = `{"Id":"` + strings.Repeat("0", 64) + `","HostConfig":{},
		"Mounts":[{"Type":"bind","Source":"/var/run/docker.sock","Destination":"/var/run/docker.sock","RW":true}]}`
	refusedBy(t, f, http.MethodPost, "/containers/sockholder/rename?name=y", "")
	forwardedBy(t, f, http.MethodPost, "/containers/sockholder/stop", "")
	forwardedBy(t, f, http.MethodPost, "/containers/eightly-agent/start", "")
}

// Codex 4: the list carries each container's command line as one string.
func TestRedact_ContainerList(t *testing.T) {
	list := `[{"Id":"` + idAgent + `","Command":"/eightly-agent --secret s3cr3t","Mounts":[]},
		{"Id":"` + idApp + `","Command":"/run --secret=abc --enroll-token t0kval --port 1","Mounts":[]},
		{"Id":"` + idPriv + `","Command":"/tool hunter2","Mounts":[{"Source":"/var/run/docker.sock","Destination":"/s"}]},
		{"Id":"` + idHost + `","Names":["/x"]}]`
	f := testFilter(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1.45/containers/json?all=1", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Request: req, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(list))}
	if err := f.redactResponse(resp); err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	for _, secret := range []string{"s3cr3t", "abc", "t0kval", "hunter2"} {
		if bytes.Contains(out, []byte(secret)) {
			t.Errorf("list still shows %q: %s", secret, out)
		}
	}
	var got []map[string]any
	if err := json.Unmarshal(out, &got); err != nil || len(got) != 4 {
		t.Fatalf("redacted list unreadable: %v %s", err, out)
	}
	if got[1]["Command"] != "/run --secret=<redacted> --enroll-token <redacted> --port 1" {
		t.Errorf("ordinary command = %q", got[1]["Command"])
	}
	if got[0]["Command"] != redacted || got[2]["Command"] != redacted {
		t.Errorf("protected commands = %q, %q, want suppressed", got[0]["Command"], got[2]["Command"])
	}
	bad := &http.Response{StatusCode: http.StatusOK, Request: req, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{"))}
	if f.redactResponse(bad) == nil {
		t.Error("an unreadable list was passed on")
	}
	other := &http.Response{StatusCode: http.StatusOK, Request: httptest.NewRequest(http.MethodGet, "/images/json", nil), Body: io.NopCloser(strings.NewReader("x"))}
	if f.redactResponse(other) != nil {
		t.Error("an image list was touched")
	}
}

// Codex 5 / Opus O6: the daemon reads fromSrc from the form, and a form body
// ahead of the query. Only a registry pull with no body goes through.
func TestImageCreate_RegistryPullOnly(t *testing.T) {
	f := testFilter(nil, nil)
	send := func(target, ctype, body string) *httptest.ResponseRecorder {
		sink := &pathSink{}
		f.next = sink
		r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		if ctype != "" {
			r.Header.Set("Content-Type", ctype)
		}
		rr := httptest.NewRecorder()
		f.ServeHTTP(rr, r)
		if rr.Code == http.StatusOK != sink.reached {
			t.Errorf("%s: code %d but reached=%v", target, rr.Code, sink.reached)
		}
		return rr
	}
	for _, c := range []struct{ target, ctype, body string }{
		{"/images/create", "application/x-www-form-urlencoded", "fromSrc=http://internal/a.tar"},
		{"/images/create?fromImage=nginx", "application/x-www-form-urlencoded", "fromImage=&fromSrc=-"},
		{"/images/create?fromImage=nginx", "multipart/form-data; boundary=x", "--x\r\nContent-Disposition: form-data; name=\"fromSrc\"\r\n\r\n-\r\n--x--\r\n"},
		{"/images/create?fromSrc=-", "", ""},
		{"/images/create?fromImage=nginx&fromSrc=-", "", ""},
		{"/images/create?repo=x", "", ""},
		{"/images/create?fromImage=", "", ""},
	} {
		if rr := send(c.target, c.ctype, c.body); rr.Code != http.StatusForbidden {
			t.Errorf("%s %q: %d, want 403", c.target, c.body, rr.Code)
		}
	}
	if rr := send("/images/create?fromImage=nginx&tag=latest", "", ""); rr.Code != http.StatusOK {
		t.Errorf("registry pull: %d %s", rr.Code, rr.Body)
	}
}

// Opus O4: an image cannot be tagged into the agent's own repository.
func TestImageTag_AgentRepositoryRefused(t *testing.T) {
	f := testFilter(nil, nil)
	for _, q := range []string{
		"repo=ghcr.io/imogenlabs/eightly-agent&tag=latest",
		"repo=GHCR.IO/imogenlabs/eightly-agent:2",
		"repo=smashingtags/eightly-agent",
		"repo=docker.io/smashingtags/eightly-agent",
		"repo=index.docker.io/smashingtags/eightly-agent",
		"repo=registry.example:5000/ops/agent&tag=2.1", // the image the agent runs
	} {
		refusedBy(t, f, http.MethodPost, "/images/evil/tag?"+q, "")
	}
	refusedBy(t, f, http.MethodPost, "/images/evil/tag?repo=x", "repo=ghcr.io/imogenlabs/eightly-agent")
	forwardedBy(t, f, http.MethodPost, "/images/nginx/tag?repo=myrepo/nginx&tag=v1", "")
	forwardedBy(t, f, http.MethodPost, "/images/nginx/tag?repo=localhost:5000/eightly-agent", "")
	for in, want := range map[string]string{
		"nginx":                         "docker.io/library/nginx",
		"nginx:1@sha256:abc":            "docker.io/library/nginx",
		"localhost/x:1":                 "localhost/x",
		"sha256:abc":                    "",
		"registry.example:5000/a/b:2.1": "registry.example:5000/a/b",
	} {
		if got := normalizeRepo(in); got != want {
			t.Errorf("normalizeRepo(%q) = %q, want %q", in, got, want)
		}
	}
}

// Opus O2: a Windows daemon reads "c:/:c:/host" as the host's C: drive.
func TestBinds_WindowsAndOddSourcesRefused(t *testing.T) {
	f := testFilter(nil, testRoots)
	for _, hc := range []string{
		`{"Binds":["c:/:c:/host"]}`,
		`{"Binds":["C:/:/host"]}`,
		`{"Binds":["c:\\:/host"]}`,
		`{"Binds":["//./c:/:/host"]}`,
		`{"Binds":["//?/c:/:/host"]}`,
		`{"Binds":["\\\\?\\c:\\:/host"]}`,
		`{"Binds":["c:/data:/data"]}`,
		`{"Binds":["c:"]}`,
		`{"Binds":["v:/data"]}`,
		`{"Mounts":[{"Type":"bind","Source":"c:/","Target":"/h"}]}`,
		`{"Mounts":[{"Type":"bind","Source":"/opt/appdata/c:/x","Target":"/h"}]}`,
		`{"Mounts":[{"Type":"bind","Source":"//./opt/appdata","Target":"/h"}]}`,
		`{"Mounts":[{"Type":"volume","Source":"c:/","Target":"/h"}]}`,
		`{"Mounts":[{"Type":"volume","Source":"../x","Target":"/h"}]}`,
	} {
		refusedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"a","HostConfig":`+hc+`}`)
	}
	forwardedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"a","HostConfig":{"Binds":["data:/data","/opt/appdata/x:/x:ro"]}}`)
}

// symlinkAt makes lstat report one path as a symlink and nothing else.
func symlinkAt(link string) func(string) (fs.FileInfo, error) {
	return func(p string) (fs.FileInfo, error) {
		if p == link {
			return fakeInfo{mode: fs.ModeSymlink}, nil
		}
		return fakeInfo{mode: fs.ModeDir}, nil
	}
}

type fakeInfo struct{ mode fs.FileMode }

func (i fakeInfo) Name() string       { return "x" }
func (i fakeInfo) Size() int64        { return 0 }
func (i fakeInfo) Mode() fs.FileMode  { return i.mode }
func (i fakeInfo) ModTime() time.Time { return time.Time{} }
func (i fakeInfo) IsDir() bool        { return i.mode.IsDir() }
func (i fakeInfo) Sys() any           { return nil }

// Opus O3: start re-checks a local volume bound under a root, not only binds.
func TestStart_RechecksBoundVolumes(t *testing.T) {
	f := testFilter(nil, testRoots)
	f.hostFS, f.lstat = true, symlinkAt("/opt/appdata/shared")
	refusedBy(t, f, http.MethodPost, "/containers/volbind/start", "")
	refusedBy(t, f, http.MethodPost, "/containers/volbind/restart", "")
	f.lstat = symlinkAt("/elsewhere")
	forwardedBy(t, f, http.MethodPost, "/containers/volbind/start", "")
}

// Opus O3: the daemon restarts a container on its own, without /start. In a
// container the agent cannot check the paths, so a restart policy is refused
// where a mount reaches a root.
func TestRestartPolicy_OnRootsRefusedInAContainer(t *testing.T) {
	f := testFilter(nil, testRoots) // hostFS false: the agent in a container
	for _, hc := range []string{
		`{"Binds":["/opt/appdata/x:/x"],"RestartPolicy":{"Name":"always"}}`,
		`{"Binds":["appbind:/x"],"RestartPolicy":{"Name":"unless-stopped"}}`,
		`{"Mounts":[{"Type":"bind","Source":"/srv/x","Target":"/x"}],"RestartPolicy":{"Name":"on-failure","MaximumRetryCount":3}}`,
		`{"Mounts":[{"Type":"volume","Source":"appbind","Target":"/x"}],"RestartPolicy":{"Name":"always"}}`,
		`{"Mounts":[{"Type":"volume","Target":"/x","VolumeOptions":{"DriverConfig":{"Name":"local","Options":{"type":"none","o":"bind","device":"/srv/y"}}}}],"RestartPolicy":{"Name":"always"}}`,
	} {
		refusedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"a","HostConfig":`+hc+`}`)
	}
	refusedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"a","Binds":["/opt/appdata/x:/x"],"RestartPolicy":{"Name":"always"}}`)
	for _, hc := range []string{
		`{"Binds":["/opt/appdata/x:/x"],"RestartPolicy":{"Name":"no"}}`,
		`{"Binds":["/opt/appdata/x:/x"]}`,
		`{"Binds":["cache:/x","/etc/localtime:/etc/localtime:ro"],"RestartPolicy":{"Name":"always"}}`,
	} {
		forwardedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"a","HostConfig":`+hc+`}`)
	}
	refusedBy(t, f, http.MethodPost, "/containers/app/update", `{"RestartPolicy":{"Name":"always"}}`)
	refusedBy(t, f, http.MethodPost, "/containers/volbind/update", `{"RestartPolicy":{"Name":"always"}}`)
	forwardedBy(t, f, http.MethodPost, "/containers/app/update", `{"RestartPolicy":{"Name":"no"},"Memory":1}`)
	forwardedBy(t, f, http.MethodPost, "/containers/eightly-ok/update", `{"RestartPolicy":{"Name":"always"}}`)

	// On the host the paths are checked at create, and the policy is fine.
	f.hostFS, f.lstat = true, func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist }
	forwardedBy(t, f, http.MethodPost, "/containers/create", `{"Image":"a","HostConfig":{"Binds":["/opt/appdata/x:/x"],"RestartPolicy":{"Name":"always"}}}`)
	forwardedBy(t, f, http.MethodPost, "/containers/app/update", `{"RestartPolicy":{"Name":"always"}}`)

	// A daemon that cannot answer is a refusal.
	down := &filter{roots: testRoots, daemon: failingDaemon{}}
	if reason := down.checkRestartOnRoots(context.Background(), &hostConfig{Binds: strOrList{"appbind:/x"}, RestartPolicy: restartPolicy{Name: "always"}}); !strings.Contains(reason, "could not be checked") {
		t.Errorf("restart policy with the daemon down: %q", reason)
	}
}
