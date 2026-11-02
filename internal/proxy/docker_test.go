package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewDockerProxy_ReturnsNonNilHandler(t *testing.T) {
	h := NewDockerProxy("/var/run/docker.sock")
	if h == nil {
		t.Fatal("NewDockerProxy returned nil")
	}
}

func TestNewDockerProxy_NonexistentSocket_ReturnsError(t *testing.T) {
	h := NewDockerProxy("/tmp/nonexistent-docker-test.sock")

	req := httptest.NewRequest("GET", "/containers/json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Error("expected non-200 when socket doesn't exist")
	}
}

func TestRewriteDockerPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/containers", "/containers/json"},
		{"/images", "/images/json"},
		{"/volumes", "/volumes"},
		{"/networks", "/networks"},
		{"/containers/abc123/start", "/containers/abc123/start"},
		{"/images/abc123/tag", "/images/abc123/tag"},
		{"/something-else", "/something-else"},
	}
	for _, tt := range tests {
		got := rewriteDockerPath(tt.input)
		if got != tt.want {
			t.Errorf("rewriteDockerPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestVersionPrefixStripping(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/v1.41/containers/json", "/containers/json"},
		{"/v1.45/images/json", "/images/json"},
		{"/containers/json", "/containers/json"},
	}
	for _, tt := range tests {
		got := versionPrefix.ReplaceAllString(tt.input, "")
		if got != tt.want {
			t.Errorf("versionPrefix strip %q = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- Fix #2: Docker proxy operation deny-list ---

// sinkHandler records whether the request was forwarded to the daemon.
type sinkHandler struct{ reached bool }

func (s *sinkHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.reached = true
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"Id":"abc"}`))
}

func postCreate(t *testing.T, body string) (*httptest.ResponseRecorder, *sinkHandler) {
	t.Helper()
	sink := &sinkHandler{}
	h := denyDangerousCreates(sink)
	req := httptest.NewRequest(http.MethodPost, "/v1.45/containers/create", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr, sink
}

func TestProxy_PrivilegedCreate_Rejected(t *testing.T) {
	rr, sink := postCreate(t, `{"Image":"alpine","HostConfig":{"Privileged":true}}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for privileged create, got %d", rr.Code)
	}
	if sink.reached {
		t.Fatal("privileged create was forwarded to the daemon (deny-list bypassed)")
	}
}

func TestProxy_NormalCreate_PassesThrough(t *testing.T) {
	rr, sink := postCreate(t, `{"Image":"nginx","HostConfig":{"RestartPolicy":{"Name":"always"},"Binds":["mydata:/data"]}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for a normal create, got %d (body %q)", rr.Code, rr.Body.String())
	}
	if !sink.reached {
		t.Fatal("normal create was not forwarded to the daemon")
	}
}

func TestProxy_DangerousFields_Rejected(t *testing.T) {
	cases := map[string]string{
		"host PID":         `{"HostConfig":{"PidMode":"host"}}`,
		"host IPC":         `{"HostConfig":{"IpcMode":"host"}}`,
		"host network":     `{"HostConfig":{"NetworkMode":"host"}}`,
		"SYS_ADMIN cap":    `{"HostConfig":{"CapAdd":["SYS_ADMIN"]}}`,
		"CAP_ prefix cap":  `{"HostConfig":{"CapAdd":["CAP_SYS_PTRACE"]}}`,
		"ALL caps":         `{"HostConfig":{"CapAdd":["ALL"]}}`,
		"bind root":        `{"HostConfig":{"Binds":["/:/host"]}}`,
		"bind etc":         `{"HostConfig":{"Binds":["/etc:/host-etc:ro"]}}`,
		"bind docker.sock": `{"HostConfig":{"Binds":["/var/run/docker.sock:/var/run/docker.sock"]}}`,
		"bind proc":        `{"HostConfig":{"Binds":["/proc/sys:/x"]}}`,
		"structured mount": `{"HostConfig":{"Mounts":[{"Type":"bind","Source":"/root"}]}}`,
	}
	for name, body := range cases {
		rr, sink := postCreate(t, body)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s: expected 403, got %d", name, rr.Code)
		}
		if sink.reached {
			t.Errorf("%s: dangerous create reached the daemon", name)
		}
	}
}

func TestProxy_SafeBind_PassesThrough(t *testing.T) {
	// A bind under a non-sensitive path (e.g. /srv/appdata) must NOT be blocked.
	rr, sink := postCreate(t, `{"HostConfig":{"Binds":["/srv/appdata/nginx:/usr/share/nginx/html:ro"]}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for safe bind, got %d", rr.Code)
	}
	if !sink.reached {
		t.Fatal("safe bind create was not forwarded")
	}
}

func TestProxy_PathPrefixNotOverMatched(t *testing.T) {
	// "/etcfoo" must NOT match the sensitive "/etc" entry.
	rr, sink := postCreate(t, `{"HostConfig":{"Binds":["/etcfoo:/x"]}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for /etcfoo bind (not sensitive), got %d", rr.Code)
	}
	if !sink.reached {
		t.Fatal("/etcfoo create was not forwarded")
	}
}

func TestProxy_ExecPrivileged_Rejected(t *testing.T) {
	sink := &sinkHandler{}
	h := denyDangerousCreates(sink)
	req := httptest.NewRequest(http.MethodPost, "/v1.45/containers/abc123/exec", strings.NewReader(`{"Privileged":true,"Cmd":["sh"]}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for privileged exec, got %d", rr.Code)
	}
	if sink.reached {
		t.Fatal("privileged exec reached the daemon")
	}
}

func TestProxy_ReadEndpoints_NotInspected(t *testing.T) {
	// GET /containers/json and other non-create traffic must pass untouched.
	sink := &sinkHandler{}
	h := denyDangerousCreates(sink)
	req := httptest.NewRequest(http.MethodGet, "/v1.45/containers/json", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !sink.reached || rr.Code != http.StatusOK {
		t.Fatalf("read endpoint should pass through; reached=%v code=%d", sink.reached, rr.Code)
	}
}

func TestInspectCreateBody_MalformedJSONAllowed(t *testing.T) {
	// A body that isn't valid JSON is not our job to block — the daemon rejects it.
	if reason := inspectCreateBody([]byte("not json")); reason != "" {
		t.Fatalf("expected malformed body to pass inspection, got reason %q", reason)
	}
}

// --- Extended deny-list vectors (SecurityOpt/Devices/Sysctls/CgroupParent/Tmpfs) ---

func TestProxy_SecurityOpt_Rejected(t *testing.T) {
	// Each entry disables a runtime sandbox; tolerate '=' and ':' separators,
	// whitespace, and case.
	cases := map[string]string{
		"seccomp unconfined":      `{"HostConfig":{"SecurityOpt":["seccomp=unconfined"]}}`,
		"apparmor unconfined":     `{"HostConfig":{"SecurityOpt":["apparmor=unconfined"]}}`,
		"label disable =":         `{"HostConfig":{"SecurityOpt":["label=disable"]}}`,
		"label disable :":         `{"HostConfig":{"SecurityOpt":["label:disable"]}}`,
		"seccomp colon sep":       `{"HostConfig":{"SecurityOpt":["seccomp:unconfined"]}}`,
		"mixed case + whitespace": `{"HostConfig":{"SecurityOpt":[" Seccomp = Unconfined "]}}`,
	}
	for name, body := range cases {
		rr, sink := postCreate(t, body)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s: expected 403, got %d", name, rr.Code)
		}
		if sink.reached {
			t.Errorf("%s: confinement-disabling SecurityOpt reached the daemon", name)
		}
	}
}

func TestProxy_SecurityOpt_SafeProfilePassesThrough(t *testing.T) {
	// A custom seccomp profile / no-new-privileges is NOT a confinement bypass.
	cases := map[string]string{
		"custom seccomp profile": `{"HostConfig":{"SecurityOpt":["seccomp=/path/to/profile.json"]}}`,
		"no-new-privileges":      `{"HostConfig":{"SecurityOpt":["no-new-privileges"]}}`,
		"label user":             `{"HostConfig":{"SecurityOpt":["label=user:USER"]}}`,
	}
	for name, body := range cases {
		rr, sink := postCreate(t, body)
		if rr.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", name, rr.Code)
		}
		if !sink.reached {
			t.Errorf("%s: safe SecurityOpt was not forwarded", name)
		}
	}
}

func TestProxy_Devices_Rejected(t *testing.T) {
	rr, sink := postCreate(t, `{"HostConfig":{"Devices":[{"PathOnHost":"/dev/sda","PathInContainer":"/dev/sda"}]}}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for host device passthrough, got %d", rr.Code)
	}
	if sink.reached {
		t.Fatal("host device passthrough reached the daemon")
	}
}

func TestProxy_EmptyDevices_PassesThrough(t *testing.T) {
	// An empty Devices array must not block a normal create.
	rr, sink := postCreate(t, `{"Image":"nginx","HostConfig":{"Devices":[]}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for empty Devices, got %d", rr.Code)
	}
	if !sink.reached {
		t.Fatal("create with empty Devices was not forwarded")
	}
}

func TestProxy_Sysctls_Rejected(t *testing.T) {
	cases := map[string]string{
		"general kernel": `{"HostConfig":{"Sysctls":{"kernel.core_pattern":"|/tmp/x"}}}`,
		"vm":             `{"HostConfig":{"Sysctls":{"vm.overcommit_memory":"1"}}}`,
	}
	for name, body := range cases {
		rr, sink := postCreate(t, body)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s: expected 403, got %d", name, rr.Code)
		}
		if sink.reached {
			t.Errorf("%s: non-namespaced sysctl reached the daemon", name)
		}
	}
}

func TestProxy_SafeSysctls_PassThrough(t *testing.T) {
	cases := map[string]string{
		"net":        `{"HostConfig":{"Sysctls":{"net.ipv4.ip_forward":"1"}}}`,
		"kernel shm": `{"HostConfig":{"Sysctls":{"kernel.shmmax":"65536"}}}`,
		"fs mqueue":  `{"HostConfig":{"Sysctls":{"fs.mqueue.msg_max":"10"}}}`,
	}
	for name, body := range cases {
		rr, sink := postCreate(t, body)
		if rr.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", name, rr.Code)
		}
		if !sink.reached {
			t.Errorf("%s: safe sysctl was not forwarded", name)
		}
	}
}

func TestProxy_CgroupParent_Rejected(t *testing.T) {
	rr, sink := postCreate(t, `{"HostConfig":{"CgroupParent":"/evil"}}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for CgroupParent, got %d", rr.Code)
	}
	if sink.reached {
		t.Fatal("CgroupParent create reached the daemon")
	}
}

func TestProxy_Tmpfs_Rejected(t *testing.T) {
	cases := map[string]string{
		"root": `{"HostConfig":{"Tmpfs":{"/":"rw"}}}`,
		"proc": `{"HostConfig":{"Tmpfs":{"/proc":"rw"}}}`,
		"sys":  `{"HostConfig":{"Tmpfs":{"/sys/fs/cgroup":"rw"}}}`,
		"etc":  `{"HostConfig":{"Tmpfs":{"/etc":"rw"}}}`,
	}
	for name, body := range cases {
		rr, sink := postCreate(t, body)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s: expected 403, got %d", name, rr.Code)
		}
		if sink.reached {
			t.Errorf("%s: sensitive tmpfs reached the daemon", name)
		}
	}
}

func TestProxy_SafeTmpfs_PassesThrough(t *testing.T) {
	// A tmpfs at an app path (the normal /tmp/ramdisk case) must pass.
	rr, sink := postCreate(t, `{"HostConfig":{"Tmpfs":{"/app/cache":"rw,size=64m"}}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for safe tmpfs, got %d", rr.Code)
	}
	if !sink.reached {
		t.Fatal("safe tmpfs create was not forwarded")
	}
}
