package browse

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// escapeFixture is a volume with a victim directory beside it and several
// ways of pointing at that directory from inside the volume.
type escapeFixture struct {
	srv     *httptest.Server
	volID   string
	dataDir string
	outside string // directory outside the volume
	victim  string // file inside outside
}

const victimContent = "outside-secret"

func newEscapeFixture(t *testing.T) *escapeFixture {
	t.Helper()
	dataPath, volID := setupVolume(t)
	dataDir := filepath.Join(dataPath, "volumes", volID, "_data")
	outside := filepath.Join(dataPath, "outside")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victim, []byte(victimContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "inside.txt"), []byte("inside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dataDir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dataDir, "inner"), 0755); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"escape":           outside,                                    // directory symlink to outside
		"escape-file":      victim,                                     // file symlink to outside
		"escape-rel":       filepath.Join("..", "..", "..", "outside"), // relative symlink to outside
		"inner/escape-mid": outside,                                    // symlinked directory in the middle of a path
		"dangling":         filepath.Join(outside, "newdir"),           // dangling symlink to outside
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(dataDir, name)); err != nil {
			t.Skipf("symlinks not supported: %v", err)
		}
	}
	f := &escapeFixture{srv: httptest.NewServer(Handler(dataPath)), volID: volID, dataDir: dataDir, outside: outside, victim: victim}
	t.Cleanup(f.srv.Close)
	return f
}

// escapePaths are user paths that must never reach outside the volume. The
// raw ones are put into the query string verbatim, so their percent-encoding
// is decoded once by the server exactly as a client would send it.
//
// inside marks a path that may legitimately succeed as long as the effect
// stays inside the volume: an absolute path is read relative to the volume
// root, and a mutation of a symlink acts on the link, not its target.
type escapeCase struct {
	name, path string
	inside     bool
}

func escapePaths(f *escapeFixture) []escapeCase {
	return []escapeCase{
		{"dotdot", "../../../outside/victim.txt", false},
		{"dotdot_inside_volume", "sub/../inside.txt", false},
		{"dotdot_after_symlink", "escape/../inside.txt", false},
		{"absolute", f.victim, true},
		{"symlink_dir", "escape/victim.txt", false},
		{"symlink_relative", "escape-rel/victim.txt", false},
		{"symlink_file", "escape-file", true},
		{"symlink_dir_in_middle", "inner/escape-mid/victim.txt", false},
		{"dangling_symlink_dir", "dangling/x.txt", false},
		{"encoded_separators", "%2e%2e%2f%2e%2e%2f%2e%2e%2foutside%2fvictim.txt", false},
		{"encoded_mixed", "..%2F..%2F..%2Foutside/victim.txt", false},
	}
}

func (f *escapeFixture) url(endpoint, rawPath string) string {
	return f.srv.URL + "/browse/" + endpoint + "?volumeID=" + f.volID + "&path=" + rawPathQuery(rawPath)
}

// rawPathQuery leaves already-encoded test paths alone and escapes the rest.
func rawPathQuery(p string) string {
	if strings.Contains(p, "%") {
		return p
	}
	return url.QueryEscape(p)
}

func decoded(p string) string {
	if d, err := url.QueryUnescape(p); err == nil {
		return d
	}
	return p
}

func (f *escapeFixture) assertOutsideUntouched(t *testing.T) {
	t.Helper()
	got, err := os.ReadFile(f.victim)
	if err != nil || string(got) != victimContent {
		t.Errorf("victim outside the volume changed: %q %v", got, err)
	}
	entries, _ := os.ReadDir(f.outside)
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("outside directory gained entries: %v", names)
	}
	if _, err := os.Stat(f.dataDir); err != nil {
		t.Errorf("volume root is gone: %v", err)
	}
}

func do(t *testing.T, req *http.Request) (int, string) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestEscapeLs(t *testing.T) {
	f := newEscapeFixture(t)
	for _, tc := range escapePaths(f) {
		t.Run(tc.name, func(t *testing.T) {
			p := strings.TrimSuffix(tc.path, "victim.txt")
			req, _ := http.NewRequest("GET", f.url("ls", p), nil) //nolint:noctx // test
			code, body := do(t, req)
			if code == http.StatusOK && !tc.inside {
				t.Errorf("ls %q answered 200: %s", p, body)
			}
			if strings.Contains(body, "victim.txt") {
				t.Errorf("ls %q listed the outside directory: %s", p, body)
			}
		})
	}
}

func TestEscapeGet(t *testing.T) {
	f := newEscapeFixture(t)
	for _, tc := range escapePaths(f) {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", f.url("get", tc.path), nil) //nolint:noctx // test
			code, body := do(t, req)
			if code == http.StatusOK && !tc.inside {
				t.Errorf("get %q answered 200", tc.path)
			}
			if strings.Contains(body, victimContent) {
				t.Errorf("get %q returned the outside file", tc.path)
			}
		})
	}
}

func putRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", "x")
	_, _ = part.Write([]byte("pwned"))
	_ = w.Close()
	req, _ := http.NewRequest("POST", target, &buf) //nolint:noctx // test
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestEscapePut(t *testing.T) {
	f := newEscapeFixture(t)
	for _, tc := range escapePaths(f) {
		t.Run(tc.name, func(t *testing.T) {
			code, _ := do(t, putRequest(t, f.url("put", tc.path)))
			if code == http.StatusCreated && !tc.inside {
				t.Errorf("put %q answered 201", tc.path)
			}
			f.assertOutsideUntouched(t)
		})
	}
	// A new file beside the victim, reached through a symlinked directory.
	t.Run("new_file_through_symlink", func(t *testing.T) {
		code, _ := do(t, putRequest(t, f.url("put", "inner/escape-mid/new.txt")))
		if code == http.StatusCreated {
			t.Errorf("put answered 201")
		}
		f.assertOutsideUntouched(t)
	})
}

func TestEscapeDelete(t *testing.T) {
	f := newEscapeFixture(t)
	for _, tc := range escapePaths(f) {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("DELETE", f.url("delete", tc.path), nil) //nolint:noctx // test
			code, _ := do(t, req)
			if code == http.StatusNoContent && !tc.inside {
				t.Errorf("delete %q answered 204", tc.path)
			}
			f.assertOutsideUntouched(t)
		})
	}
	// The volume root itself is not something browse may delete.
	for _, p := range []string{"", ".", "/", "sub/.."} {
		t.Run("root_"+p, func(t *testing.T) {
			f := newEscapeFixture(t)
			req, _ := http.NewRequest("DELETE", f.url("delete", p), nil) //nolint:noctx // test
			code, _ := do(t, req)
			// 400 names the refusal; os.Root also refuses it (with a 500).
			if code != http.StatusBadRequest {
				t.Errorf("delete of the volume root %q = %d, want 400", p, code)
			}
			f.assertOutsideUntouched(t)
		})
	}
}

func renameRequest(t *testing.T, f *escapeFixture, path, newName string) *http.Request {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"path": path, "newName": newName})
	req, _ := http.NewRequest("PUT", f.srv.URL+"/browse/rename?volumeID="+f.volID, bytes.NewReader(body)) //nolint:noctx // test
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestEscapeRename(t *testing.T) {
	f := newEscapeFixture(t)
	for _, tc := range escapePaths(f) {
		t.Run(tc.name, func(t *testing.T) {
			// The rename body is JSON, so nothing is URL-decoded: send the
			// decoded form, which is what a client would put in the body.
			code, _ := do(t, renameRequest(t, f, decoded(tc.path), "renamed.txt"))
			if code == http.StatusOK && !tc.inside {
				t.Errorf("rename %q answered 200", tc.path)
			}
			f.assertOutsideUntouched(t)
		})
	}
	// Renaming the volume root moves it to volumes/<id>/<newName>, outside _data.
	for _, p := range []string{"", ".", "/", "sub/.."} {
		t.Run("root_"+p, func(t *testing.T) {
			f := newEscapeFixture(t)
			code, _ := do(t, renameRequest(t, f, p, "moved"))
			if code != http.StatusBadRequest {
				t.Errorf("rename of the volume root %q = %d, want 400", p, code)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(f.dataDir), "moved")); err == nil {
				t.Errorf("rename %q moved the volume root out of _data", p)
			}
			f.assertOutsideUntouched(t)
		})
	}
}

// The ordinary cases must keep working once paths are confined.
func TestConfinedPathsStillWork(t *testing.T) {
	f := newEscapeFixture(t)
	if err := os.WriteFile(filepath.Join(f.dataDir, "sub", "a.txt"), []byte("A"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"", "/", "sub", "/sub", "sub/"} {
		req, _ := http.NewRequest("GET", f.url("ls", p), nil) //nolint:noctx // test
		if code, body := do(t, req); code != http.StatusOK {
			t.Errorf("ls %q = %d %s", p, code, body)
		}
	}
	req, _ := http.NewRequest("GET", f.url("get", "/sub/a.txt"), nil) //nolint:noctx // test
	if code, body := do(t, req); code != http.StatusOK || body != "A" {
		t.Errorf("get /sub/a.txt = %d %q", code, body)
	}
	if code, _ := do(t, putRequest(t, f.url("put", "new/deep/b.txt"))); code != http.StatusCreated {
		t.Errorf("put into a new directory = %d", code)
	}
	if code, _ := do(t, renameRequest(t, f, "sub/a.txt", "c.txt")); code != http.StatusOK {
		t.Errorf("rename sub/a.txt = %d", code)
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "sub", "c.txt")); err != nil {
		t.Errorf("rename did not land: %v", err)
	}
	// Deleting a symlink removes the link, never what it points at.
	req, _ = http.NewRequest("DELETE", f.url("delete", "escape"), nil) //nolint:noctx // test
	code, _ := do(t, req)
	if code != http.StatusNoContent && code != http.StatusBadRequest {
		t.Errorf("delete of a symlink = %d", code)
	}
	f.assertOutsideUntouched(t)
	req, _ = http.NewRequest("DELETE", f.url("delete", "sub"), nil) //nolint:noctx // test
	if code, _ := do(t, req); code != http.StatusNoContent {
		t.Errorf("delete sub = %d", code)
	}
}

// TestEscapeRaceSwapDirectoryForSymlink swaps a directory for a symlink to
// outside while requests run. A check-then-open implementation lets some
// requests through the window between the check and the open; opening
// relative to the volume root cannot be redirected.
func TestEscapeRaceSwapDirectoryForSymlink(t *testing.T) {
	f := newEscapeFixture(t)
	flip := filepath.Join(f.dataDir, "flip")
	parked := filepath.Join(f.dataDir, "flip-real")
	if err := os.Mkdir(parked, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parked, "victim.txt"), []byte("inside"), 0644); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tmp := filepath.Join(f.dataDir, "flip-tmp")
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Real directory in place, then a symlink to outside in place.
			_ = os.Remove(tmp)
			_ = os.Symlink("flip-real", tmp)
			_ = os.Rename(tmp, flip)
			_ = os.Remove(tmp)
			_ = os.Symlink(f.outside, tmp)
			_ = os.Rename(tmp, flip)
		}
	}()

	h := f.srv.Config.Handler
	// Today's code fails within ~40 rounds; a second is hundreds of rounds.
	deadline := time.Now().Add(time.Second)
	for i := 0; time.Now().Before(deadline); i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/browse/get?volumeID="+f.volID+"&path=flip/victim.txt", nil))
		if strings.Contains(rec.Body.String(), victimContent) {
			close(stop)
			<-done
			t.Fatalf("get read the outside file on iteration %d", i)
		}
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, putRequest(t, "/browse/put?volumeID="+f.volID+"&path=flip/raced.txt"))
		if _, err := os.Stat(filepath.Join(f.outside, "raced.txt")); err == nil {
			close(stop)
			<-done
			t.Fatalf("put wrote outside the volume on iteration %d", i)
		}
	}
	close(stop)
	<-done
}

// A symlink at the final component is refused for get and put even when it
// points inside the volume (O_NOFOLLOW is honoured through the volume root).
func TestFinalSymlinkInsideVolumeRefused(t *testing.T) {
	f := newEscapeFixture(t)
	if err := os.Symlink("inside.txt", filepath.Join(f.dataDir, "alias")); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", f.url("get", "alias"), nil) //nolint:noctx // test
	if code, body := do(t, req); code == http.StatusOK {
		t.Errorf("get through a final symlink = 200 %q", body)
	}
	if code, _ := do(t, putRequest(t, f.url("put", "alias"))); code == http.StatusCreated {
		t.Errorf("put through a final symlink = 201")
	}
	if got, _ := os.ReadFile(filepath.Join(f.dataDir, "inside.txt")); string(got) != "inside" {
		t.Errorf("put wrote through the symlink: %q", got)
	}
}

// TestRaceSwapFinalComponentForSymlink swaps the requested file for a symlink
// to a hidden file in the same volume. A check before the open leaves a window
// where get reads the hidden file and put overwrites it; the refusal has to be
// part of the open itself.
func TestRaceSwapFinalComponentForSymlink(t *testing.T) {
	f := newEscapeFixture(t)
	const hidden = "hidden-secret"
	if err := os.WriteFile(filepath.Join(f.dataDir, ".secret"), []byte(hidden), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dataDir, "plain-src"), []byte("plain"), 0644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(f.dataDir, "target")

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tmp := filepath.Join(f.dataDir, "target-tmp")
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.Remove(tmp)
			_ = os.Link(filepath.Join(f.dataDir, "plain-src"), tmp)
			_ = os.Rename(tmp, target)
			_ = os.Remove(tmp)
			_ = os.Symlink(".secret", tmp)
			_ = os.Rename(tmp, target)
		}
	}()
	finish := func() {
		close(stop)
		<-done
	}

	h := f.srv.Config.Handler
	deadline := time.Now().Add(time.Second)
	for i := 0; time.Now().Before(deadline); i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/browse/get?volumeID="+f.volID+"&path=target", nil))
		if strings.Contains(rec.Body.String(), hidden) {
			finish()
			t.Fatalf("get followed a swapped-in symlink to a hidden file on round %d", i)
		}
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, putRequest(t, "/browse/put?volumeID="+f.volID+"&path=target"))
		if got, _ := os.ReadFile(filepath.Join(f.dataDir, ".secret")); string(got) != hidden {
			finish()
			t.Fatalf("put wrote through a swapped-in symlink on round %d: %q", i, got)
		}
	}
	finish()
}

// TestOpenVerifiedRace runs the final-component swap against openVerified,
// the Windows no-follow open, which can be exercised here directly.
func TestOpenVerifiedRace(t *testing.T) {
	dir := t.TempDir()
	const hidden = "hidden-secret"
	write := func(n, s string) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(s), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(".secret", hidden)
	write("plain-src", "plain")
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := os.Symlink(".secret", filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	if f, err := openVerified(root, "alias", os.O_RDONLY, 0); err == nil {
		f.Close()
		t.Fatal("openVerified opened a final symlink")
	}
	if f, err := openVerified(root, "alias", os.O_WRONLY|os.O_TRUNC, 0); err == nil {
		f.Close()
		t.Fatal("openVerified opened a final symlink for writing")
	}

	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		target, tmp := filepath.Join(dir, "target"), filepath.Join(dir, "target-tmp")
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.Remove(tmp)
			_ = os.Link(filepath.Join(dir, "plain-src"), tmp)
			_ = os.Rename(tmp, target)
			_ = os.Remove(tmp)
			_ = os.Symlink(".secret", tmp)
			_ = os.Rename(tmp, target)
		}
	}()
	defer func() { close(stop); <-done }()

	deadline := time.Now().Add(time.Second)
	for i := 0; time.Now().Before(deadline); i++ {
		if f, err := openVerified(root, "target", os.O_RDONLY, 0); err == nil {
			b, _ := io.ReadAll(f)
			f.Close()
			if string(b) == hidden {
				t.Fatalf("read the hidden file through a swapped-in symlink on round %d", i)
			}
		}
		if f, err := openVerified(root, "target", os.O_WRONLY|os.O_TRUNC, 0); err == nil {
			f.Close()
		}
		if got, _ := os.ReadFile(filepath.Join(dir, ".secret")); string(got) != hidden {
			t.Fatalf("truncated the hidden file through a swapped-in symlink on round %d", i)
		}
	}
}

// A volume the agent may search but not read answers 403, not a misleading 404.
func TestUnreadableVolumeIsForbidden(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits do not make a folder unreadable on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads any directory")
	}
	f := newEscapeFixture(t)
	if err := os.Chmod(f.dataDir, 0o311); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.dataDir, 0o755) })
	req, _ := http.NewRequest("GET", f.url("get", "inside.txt"), nil) //nolint:noctx // test
	if code, body := do(t, req); code != http.StatusForbidden {
		t.Errorf("get from an unreadable volume = %d %s, want 403", code, body)
	}
}

// openVerified must not create through a dangling symlink before refusing it.
func TestOpenVerifiedDoesNotCreateThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Symlink("made.txt", filepath.Join(dir, "dlink")); err != nil {
		t.Fatal(err)
	}
	if f, err := openVerified(root, "dlink", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644); err == nil {
		f.Close()
		t.Error("openVerified opened a dangling symlink")
	}
	if _, err := os.Lstat(filepath.Join(dir, "made.txt")); err == nil {
		t.Error("openVerified created the symlink's target before refusing")
	}
	// A plain new file is still created.
	f, err := openVerified(root, "new.txt", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("create new.txt: %v", err)
	}
	f.Close()
}
