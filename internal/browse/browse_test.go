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

	resp, err := http.Get(srv.URL + "/browse/ls?volumeID=" + volID + "&path=")
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

	resp, err := http.Get(srv.URL + "/browse/ls?volumeID=" + volID + "&path=")
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

	resp, err := http.Get(srv.URL + "/browse/get?volumeID=" + volID + "&path=data.bin")
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

	req, _ := http.NewRequest("POST", srv.URL+"/browse/put?volumeID="+volID+"&path=upload.txt", &buf)
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

	req, _ := http.NewRequest("DELETE", srv.URL+"/browse/delete?volumeID="+volID+"&path=gone.txt", nil)
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
	req, _ := http.NewRequest("PUT", srv.URL+"/browse/rename?volumeID="+volID, bytes.NewReader(body))
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

	resp, err := http.Get(srv.URL + "/browse/ls?volumeID=../etc&path=")
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

	resp, err := http.Get(srv.URL + "/browse/ls?volumeID=safe&path=../../etc/passwd")
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
	req, _ := http.NewRequest("PUT", srv.URL+"/browse/rename?volumeID="+volID, bytes.NewReader(body))
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
	req, _ := http.NewRequest("PUT", srv.URL+"/browse/rename?volumeID="+volID, bytes.NewReader(body))
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

	resp, err := http.Get(srv.URL + "/browse/ls?volumeID=/etc&path=")
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

	resp, err := http.Get(srv.URL + "/browse/ls?volumeID=foo/bar&path=")
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

	resp, err := http.Get(srv.URL + "/browse/ls?volumeID=.hidden&path=")
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
	resp, err := http.Get(srv.URL + "/browse/get?volumeID=" + volID + "&path=escape/secret.txt")
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

	req, _ := http.NewRequest("POST", srv.URL+"/browse/put?volumeID="+volID+"&path=subdir", &buf)
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
