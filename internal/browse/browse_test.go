package browse

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func setupVolume(t *testing.T) (string, string) {
	t.Helper()
	dataPath := t.TempDir()
	volumeID := "testvol"
	dataDir := filepath.Join(dataPath, "volumes", volumeID, "_data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	return dataPath, volumeID
}

func TestLsListsFiles(t *testing.T) {
	dataPath, volID := setupVolume(t)
	dataDir := filepath.Join(dataPath, "volumes", volID, "_data")

	os.WriteFile(filepath.Join(dataDir, "hello.txt"), []byte("hi"), 0644)
	os.Mkdir(filepath.Join(dataDir, "subdir"), 0755)

	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/browse/ls?volumeID=" + volID + "&path=") //nolint:noctx // test helper, no meaningful context
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var entries []fileEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
		if e.Name == "hello.txt" {
			if e.Dir {
				t.Error("hello.txt should not be a directory")
			}
			if e.Size != 2 {
				t.Errorf("expected size 2, got %d", e.Size)
			}
		}
		if e.Name == "subdir" && !e.Dir {
			t.Error("subdir should be a directory")
		}
	}
	if !names["hello.txt"] || !names["subdir"] {
		t.Errorf("missing expected entries: %v", names)
	}
}

func TestLsEmptyDir(t *testing.T) {
	dataPath, volID := setupVolume(t)

	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/browse/ls?volumeID=" + volID + "&path=") //nolint:noctx // test helper, no meaningful context
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var entries []fileEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestGetDownloadsFile(t *testing.T) {
	dataPath, volID := setupVolume(t)
	dataDir := filepath.Join(dataPath, "volumes", volID, "_data")
	content := "file content here"
	os.WriteFile(filepath.Join(dataDir, "data.bin"), []byte(content), 0644)

	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/browse/get?volumeID=" + volID + "&path=data.bin") //nolint:noctx // test helper, no meaningful context
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != content {
		t.Errorf("expected %q, got %q", content, string(body))
	}
}

func TestPutUploadsFile(t *testing.T) {
	dataPath, volID := setupVolume(t)

	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "upload.txt")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("uploaded data"))
	writer.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/browse/put?volumeID="+volID+"&path=upload.txt", &buf) //nolint:noctx // test helper, no meaningful context
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	written, err := os.ReadFile(filepath.Join(dataPath, "volumes", volID, "_data", "upload.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != "uploaded data" {
		t.Errorf("expected %q, got %q", "uploaded data", string(written))
	}
}

func TestDeleteRemovesFile(t *testing.T) {
	dataPath, volID := setupVolume(t)
	dataDir := filepath.Join(dataPath, "volumes", volID, "_data")
	target := filepath.Join(dataDir, "gone.txt")
	os.WriteFile(target, []byte("bye"), 0644)

	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	req, _ := http.NewRequest("DELETE", srv.URL+"/browse/delete?volumeID="+volID+"&path=gone.txt", nil) //nolint:noctx // test helper, no meaningful context
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("file should have been deleted")
	}
}

func TestRenameFile(t *testing.T) {
	dataPath, volID := setupVolume(t)
	dataDir := filepath.Join(dataPath, "volumes", volID, "_data")
	os.WriteFile(filepath.Join(dataDir, "old.txt"), []byte("data"), 0644)

	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"path": "old.txt", "newName": "new.txt"})
	req, _ := http.NewRequest("PUT", srv.URL+"/browse/rename?volumeID="+volID, bytes.NewReader(body)) //nolint:noctx // test helper, no meaningful context
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if _, err := os.Stat(filepath.Join(dataDir, "old.txt")); !os.IsNotExist(err) {
		t.Error("old file should not exist")
	}
	content, err := os.ReadFile(filepath.Join(dataDir, "new.txt"))
	if err != nil {
		t.Fatal("new file should exist")
	}
	if string(content) != "data" {
		t.Errorf("expected %q, got %q", "data", string(content))
	}
}

func TestPathTraversalVolumeID(t *testing.T) {
	dataPath := t.TempDir()
	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/browse/ls?volumeID=../etc&path=") //nolint:noctx // test helper, no meaningful context
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestPathTraversalPath(t *testing.T) {
	dataPath := t.TempDir()
	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/browse/ls?volumeID=safe&path=../../etc/passwd") //nolint:noctx // test helper, no meaningful context
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestRenameTraversalDotDot(t *testing.T) {
	dataPath, volID := setupVolume(t)
	dataDir := filepath.Join(dataPath, "volumes", volID, "_data")
	os.WriteFile(filepath.Join(dataDir, "a.txt"), []byte("x"), 0644)

	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"path": "a.txt", "newName": "../escape.txt"})
	req, _ := http.NewRequest("PUT", srv.URL+"/browse/rename?volumeID="+volID, bytes.NewReader(body)) //nolint:noctx // test helper, no meaningful context
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestRenameTraversalSlash(t *testing.T) {
	dataPath, volID := setupVolume(t)
	dataDir := filepath.Join(dataPath, "volumes", volID, "_data")
	os.WriteFile(filepath.Join(dataDir, "a.txt"), []byte("x"), 0644)

	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"path": "a.txt", "newName": "sub/escape.txt"})
	req, _ := http.NewRequest("PUT", srv.URL+"/browse/rename?volumeID="+volID, bytes.NewReader(body)) //nolint:noctx // test helper, no meaningful context
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestVolumeIDAbsolutePath(t *testing.T) {
	dataPath := t.TempDir()
	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/browse/ls?volumeID=/etc&path=") //nolint:noctx // test helper, no meaningful context
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for absolute path volumeID, got %d", resp.StatusCode)
	}
}

func TestVolumeIDWithPathSeparator(t *testing.T) {
	dataPath := t.TempDir()
	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/browse/ls?volumeID=foo/bar&path=") //nolint:noctx // test helper, no meaningful context
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for volumeID with separator, got %d", resp.StatusCode)
	}
}

func TestVolumeIDStartingWithDot(t *testing.T) {
	dataPath := t.TempDir()
	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/browse/ls?volumeID=.hidden&path=") //nolint:noctx // test helper, no meaningful context
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for dot-prefixed volumeID, got %d", resp.StatusCode)
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	dataPath, volID := setupVolume(t)
	dataDir := filepath.Join(dataPath, "volumes", volID, "_data")

	// Create a directory outside the volume data path
	outsideDir := filepath.Join(dataPath, "outside")
	os.MkdirAll(outsideDir, 0755)
	os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("sensitive"), 0644)

	// Create a symlink inside the volume data dir that points outside
	symlink := filepath.Join(dataDir, "escape")
	if err := os.Symlink(outsideDir, symlink); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	// Try to read through the symlink
	resp, err := http.Get(srv.URL + "/browse/get?volumeID=" + volID + "&path=escape/secret.txt") //nolint:noctx // test helper, no meaningful context
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("symlink escape should be rejected, but got 200 with body: %q", string(body))
	}
}

func TestPutRejectsOversizeUpload(t *testing.T) {
	dataPath, volID := setupVolume(t)
	handler := Handler(dataPath)

	// Build a multipart body with a file part that streams more than 100MB.
	// We use io.Pipe so the multipart writer runs in a goroutine and produces
	// data on demand without allocating 100MB+ of memory.
	pr, pw := io.Pipe()
	mpWriter := multipart.NewWriter(pw)

	go func() {
		part, err := mpWriter.CreateFormFile("file", "big.bin")
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		// Write 101 MB in 1 MB chunks to exceed the 100 MB limit
		chunk := make([]byte, 1024*1024)
		for i := 0; i < 101; i++ {
			if _, err := part.Write(chunk); err != nil {
				// Expected: the server closes the reader once limit is hit
				pw.CloseWithError(err)
				return
			}
		}
		mpWriter.Close()
		pw.Close()
	}()

	req := httptest.NewRequest("POST", "/browse/put?volumeID="+volID+"&path=big.bin", pr)
	req.Header.Set("Content-Type", mpWriter.FormDataContentType())

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code == http.StatusCreated {
		t.Fatalf("expected rejection for oversize upload, got 201")
	}
}

// TestSymlinkEscapeWriteRejected is the sabotage-proven test: a symlink that
// lives INSIDE the volume but points to a directory OUTSIDE it must not allow
// put/delete/rename to operate on the outside target. If the containment
// recheck is reverted (e.g. only applied when the final target already exists),
// these write paths escape and the assertions below fail.
func TestSymlinkEscapeWriteRejected(t *testing.T) {
	dataPath, volID := setupVolume(t)
	dataDir := filepath.Join(dataPath, "volumes", volID, "_data")

	// A directory outside the volume holding a victim file.
	outsideDir := filepath.Join(dataPath, "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(outsideDir, "victim.txt")
	if err := os.WriteFile(victim, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	// Symlink inside the volume pointing outside.
	symlink := filepath.Join(dataDir, "escape")
	if err := os.Symlink(outsideDir, symlink); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	// PUT through the symlink: must NOT create/overwrite a file outside.
	t.Run("put", func(t *testing.T) {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		part, _ := writer.CreateFormFile("file", "x")
		part.Write([]byte("pwned"))
		writer.Close()

		req, _ := http.NewRequest("POST", srv.URL+"/browse/put?volumeID="+volID+"&path=escape/victim.txt", &buf) //nolint:noctx
		req.Header.Set("Content-Type", writer.FormDataContentType())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusCreated {
			t.Errorf("put through symlink should be rejected, got 201")
		}
		got, _ := os.ReadFile(victim)
		if string(got) != "original" {
			t.Errorf("victim file was overwritten through symlink: %q", string(got))
		}
	})

	// PUT a brand-new (non-existent) file outside via the symlink. This is the
	// exact case the bug skipped: target does not yet exist.
	t.Run("put_new_file", func(t *testing.T) {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		part, _ := writer.CreateFormFile("file", "x")
		part.Write([]byte("pwned"))
		writer.Close()

		req, _ := http.NewRequest("POST", srv.URL+"/browse/put?volumeID="+volID+"&path=escape/newfile.txt", &buf) //nolint:noctx
		req.Header.Set("Content-Type", writer.FormDataContentType())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusCreated {
			t.Errorf("put of new file through symlink should be rejected, got 201")
		}
		if _, err := os.Stat(filepath.Join(outsideDir, "newfile.txt")); err == nil {
			t.Errorf("new file was created outside the volume through symlink")
		}
	})

	// DELETE through the symlink: must NOT remove the outside file.
	t.Run("delete", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", srv.URL+"/browse/delete?volumeID="+volID+"&path=escape/victim.txt", nil) //nolint:noctx
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusNoContent {
			t.Errorf("delete through symlink should be rejected, got 204")
		}
		if _, err := os.Stat(victim); err != nil {
			t.Errorf("victim file was deleted through symlink: %v", err)
		}
	})

	// RENAME with a destination that resolves outside via the symlink dir.
	t.Run("rename_dest_escape", func(t *testing.T) {
		// Create a real file inside the volume to rename.
		os.WriteFile(filepath.Join(dataDir, "movable.txt"), []byte("data"), 0644)
		body, _ := json.Marshal(map[string]string{"path": "escape/victim.txt", "newName": "renamed.txt"})
		req, _ := http.NewRequest("PUT", srv.URL+"/browse/rename?volumeID="+volID, bytes.NewReader(body)) //nolint:noctx
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			t.Errorf("rename through symlink should be rejected, got 200")
		}
		if _, err := os.Stat(filepath.Join(outsideDir, "renamed.txt")); err == nil {
			t.Errorf("file was created outside the volume via rename through symlink")
		}
	})
}

func TestReadOnlyRejectsMutations(t *testing.T) {
	dataPath, volID := setupVolume(t)
	dataDir := filepath.Join(dataPath, "volumes", volID, "_data")
	os.WriteFile(filepath.Join(dataDir, "f.txt"), []byte("x"), 0644)

	srv := httptest.NewServer(Handler(dataPath, ReadOnly(true)))
	defer srv.Close()

	// put
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "u.txt")
	part.Write([]byte("data"))
	writer.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/browse/put?volumeID="+volID+"&path=u.txt", &buf) //nolint:noctx
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("read-only put should be 403, got %d", resp.StatusCode)
	}

	// delete
	req, _ = http.NewRequest("DELETE", srv.URL+"/browse/delete?volumeID="+volID+"&path=f.txt", nil) //nolint:noctx
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("read-only delete should be 403, got %d", resp.StatusCode)
	}

	// rename
	body, _ := json.Marshal(map[string]string{"path": "f.txt", "newName": "g.txt"})
	req, _ = http.NewRequest("PUT", srv.URL+"/browse/rename?volumeID="+volID, bytes.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("read-only rename should be 403, got %d", resp.StatusCode)
	}

	// Read paths still work in read-only mode.
	resp, err = http.Get(srv.URL + "/browse/ls?volumeID=" + volID + "&path=") //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("read-only ls should be 200, got %d", resp.StatusCode)
	}
}

func TestGetRefusesDotfile(t *testing.T) {
	dataPath, volID := setupVolume(t)
	dataDir := filepath.Join(dataPath, "volumes", volID, "_data")
	os.WriteFile(filepath.Join(dataDir, ".secret"), []byte("hidden"), 0644)

	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/browse/get?volumeID=" + volID + "&path=.secret") //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("get of dotfile should be 403, got %d", resp.StatusCode)
	}
}

func TestLsSkipsDotfiles(t *testing.T) {
	dataPath, volID := setupVolume(t)
	dataDir := filepath.Join(dataPath, "volumes", volID, "_data")
	os.WriteFile(filepath.Join(dataDir, "visible.txt"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dataDir, ".hidden"), []byte("y"), 0644)

	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/browse/ls?volumeID=" + volID + "&path=") //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var entries []fileEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == ".hidden" {
			t.Errorf("ls should skip dotfiles, but listed %q", e.Name)
		}
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 visible entry, got %d", len(entries))
	}
}

func TestPutRejectsDirectoryTarget(t *testing.T) {
	dataPath, volID := setupVolume(t)
	dataDir := filepath.Join(dataPath, "volumes", volID, "_data")
	os.MkdirAll(filepath.Join(dataDir, "subdir"), 0755)

	srv := httptest.NewServer(Handler(dataPath))
	defer srv.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "upload.txt")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("data"))
	writer.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/browse/put?volumeID="+volID+"&path=subdir", &buf) //nolint:noctx // test helper, no meaningful context
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for directory target, got %d", resp.StatusCode)
	}
}
