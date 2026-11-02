package browse

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hiddenFixture is a volume with a hidden directory holding a "key", a hidden
// file at the root, and a visible file, served read-write and read-only.
type hiddenFixture struct {
	dataDir, volID string
	rw, ro         *httptest.Server
}

func newHiddenFixture(t *testing.T) *hiddenFixture {
	t.Helper()
	dataPath, volID := setupVolume(t)
	dataDir := filepath.Join(dataPath, "volumes", volID, "_data")
	for _, d := range []string{".ssh", "app/.git"} {
		if err := os.MkdirAll(filepath.Join(dataDir, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		".ssh/id_rsa":     "PRIVATE KEY",
		".env":            "SECRET=1",
		"app/.git/config": "token",
		"app/visible.txt": "ok",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dataDir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	f := &hiddenFixture{
		dataDir: dataDir,
		volID:   volID,
		rw:      httptest.NewServer(Handler(filepath.Join(dataPath, "volumes"))),
		ro:      httptest.NewServer(Handler(filepath.Join(dataPath, "volumes"), ReadOnly(true))),
	}
	t.Cleanup(f.rw.Close)
	t.Cleanup(f.ro.Close)
	return f
}

func (f *hiddenFixture) req(t *testing.T, srv *httptest.Server, method, endpoint, path string, body []byte) (int, string) {
	t.Helper()
	target := srv.URL + "/browse/" + endpoint + "?volumeID=" + f.volID + "&path=" + path
	req, err := http.NewRequest(method, target, bytes.NewReader(body)) //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	return do(t, req)
}

func TestHiddenSegmentsRefusedOnReads(t *testing.T) {
	f := newHiddenFixture(t)
	paths := []string{".ssh", ".ssh/", "/.ssh", ".ssh/id_rsa", "/.ssh/id_rsa", ".env", "app/.git", "app/.git/config", "app/./.git/config", "app//.git/config"}
	for _, srv := range []*httptest.Server{f.rw, f.ro} {
		for _, p := range paths {
			for _, ep := range []string{"ls", "get"} {
				code, body := f.req(t, srv, "GET", ep, p, nil)
				if code != http.StatusForbidden {
					t.Errorf("%s %q = %d, want 403", ep, p, code)
				}
				if strings.Contains(body, "PRIVATE KEY") || strings.Contains(body, "SECRET") || strings.Contains(body, "token") || strings.Contains(body, "id_rsa") {
					t.Errorf("%s %q leaked hidden content: %q", ep, p, body)
				}
			}
		}
	}
	// The visible file next to the hidden directory still works.
	if code, body := f.req(t, f.ro, "GET", "get", "app/visible.txt", nil); code != http.StatusOK || body != "ok" {
		t.Errorf("get app/visible.txt = %d %q", code, body)
	}
}

func TestHiddenSegmentsRefusedOnMutations(t *testing.T) {
	f := newHiddenFixture(t)
	for _, p := range []string{".ssh/id_rsa", ".ssh/authorized_keys", "app/.git/config", ".env"} {
		code, _ := do(t, putRequest(t, f.rw.URL+"/browse/put?volumeID="+f.volID+"&path="+p))
		if code != http.StatusForbidden {
			t.Errorf("put %q = %d, want 403", p, code)
		}
		if code, _ := f.req(t, f.rw, "DELETE", "delete", p, nil); code != http.StatusForbidden {
			t.Errorf("delete %q = %d, want 403", p, code)
		}
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, ".ssh", "authorized_keys")); err == nil {
		t.Error("put created a file in a hidden directory")
	}
	for name, want := range map[string]string{".ssh/id_rsa": "PRIVATE KEY", ".env": "SECRET=1", "app/.git/config": "token"} {
		got, err := os.ReadFile(filepath.Join(f.dataDir, name))
		if err != nil || string(got) != want {
			t.Errorf("%s changed: %q %v", name, got, err)
		}
	}
}

// TestRenameHiddenSourceRefused covers renaming .env to env and then
// downloading it: the source name is checked, not only the new name.
func TestRenameHiddenSourceRefused(t *testing.T) {
	f := newHiddenFixture(t)
	cases := []struct{ path, newName string }{
		{".env", "env"},
		{"/.env", "env"},
		{".ssh/id_rsa", "key"},
		{".ssh", "ssh"},
		{"app/.git", "git"},
		{"app/visible.txt", ".hidden"}, // destination hidden
		{"app/visible.txt", ".."},
	}
	for _, c := range cases {
		body, _ := json.Marshal(map[string]string{"path": c.path, "newName": c.newName})
		req, _ := http.NewRequest("PUT", f.rw.URL+"/browse/rename?volumeID="+f.volID, bytes.NewReader(body)) //nolint:noctx // test
		code, _ := do(t, req)
		if code != http.StatusForbidden && code != http.StatusBadRequest {
			t.Errorf("rename %q -> %q = %d, want 403 or 400", c.path, c.newName, code)
		}
	}
	for _, name := range []string{"env", "ssh", "key", "app/git", "app/.hidden"} {
		if _, err := os.Lstat(filepath.Join(f.dataDir, name)); err == nil {
			t.Errorf("rename produced %s", name)
		}
	}
	if code, _ := f.req(t, f.rw, "GET", "get", "env", nil); code == http.StatusOK {
		t.Error("renamed .env became downloadable")
	}
}

func TestHasHiddenSegment(t *testing.T) {
	for name, want := range map[string]bool{
		".":                           false,
		"a":                           false,
		"a.b/c.d":                     false,
		"a..b":                        false,
		filepath.FromSlash(".a"):      true,
		filepath.FromSlash("a/.b"):    true,
		filepath.FromSlash("a/.b/c"):  true,
		filepath.FromSlash("a/b/..."): true,
	} {
		if got := hasHiddenSegment(name); got != want {
			t.Errorf("hasHiddenSegment(%q) = %v, want %v", name, got, want)
		}
	}
}
