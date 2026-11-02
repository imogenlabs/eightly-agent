package browse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Codex 9: os.Root follows a symlink that stays inside the volume, so
// "alias -> .ssh" reached a hidden directory by a name that is not hidden.
func TestSymlinkAliasToHiddenRefused(t *testing.T) {
	f := newHiddenFixture(t)
	if err := os.Symlink(".ssh", filepath.Join(f.dataDir, "alias")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	if err := os.Symlink("app", filepath.Join(f.dataDir, "applink")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, ep, path string }{
		{"GET", "get", "alias/id_rsa"},
		{"GET", "ls", "alias"},
		{"GET", "ls", "alias/"},
		{"GET", "get", "applink/visible.txt"},
		{"POST", "put", "alias/new"},
		{"DELETE", "delete", "alias/id_rsa"},
	} {
		code, body := f.req(t, f.rw, c.method, c.ep, c.path, nil)
		if code != http.StatusForbidden || strings.Contains(body, "PRIVATE KEY") {
			t.Errorf("%s %q = %d %q, want 403", c.ep, c.path, code, body)
		}
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, ".ssh", "id_rsa")); err != nil {
		t.Errorf("the hidden key was touched: %v", err)
	}
	// The link itself can still be removed; that does not follow it.
	if code, _ := f.req(t, f.rw, "DELETE", "delete", "alias", nil); code != http.StatusNoContent {
		t.Errorf("delete of the link itself = %d", code)
	}
}

// Codex 2 / Opus O5: a volume is opened only after the same admission a
// container's mount passes.
func TestAdmitVolume(t *testing.T) {
	dataPath, volID := setupVolume(t)
	if err := os.WriteFile(filepath.Join(dataPath, "volumes", volID, "_data", "key.pem"), []byte("KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	var asked string
	admit := AdmitVolume(func(_ context.Context, name string) (int, string) {
		asked = name
		if name == volID {
			return http.StatusForbidden, "volume " + name + " holds the agent's own data"
		}
		return 0, ""
	})
	srv := httptest.NewServer(Handler(filepath.Join(dataPath, "volumes"), admit))
	t.Cleanup(srv.Close)
	for _, ep := range []string{"ls?path=/", "get?path=key.pem"} {
		req, _ := http.NewRequest("GET", srv.URL+"/browse/"+ep+"&volumeID="+volID, nil) //nolint:noctx // test
		code, body := do(t, req)
		if code != http.StatusForbidden || strings.Contains(body, "KEY\"") || body == "KEY" {
			t.Errorf("%s = %d %q, want 403", ep, code, body)
		}
	}
	if asked != volID {
		t.Errorf("admission asked about %q, want %q", asked, volID)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/browse/ls?path=/&volumeID=other", nil) //nolint:noctx // test
	if code, _ := do(t, req); code != http.StatusNotFound {
		t.Errorf("an admitted volume that does not exist = %d, want 404", code)
	}
}
