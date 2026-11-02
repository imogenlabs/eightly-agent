//go:build !windows

package browse

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// specialFixture is a volume holding a named pipe ("pipe") and a regular file.
// Opening the pipe for reading without O_NONBLOCK blocks until a writer comes,
// which before the fix hung the request forever.
func specialFixture(t *testing.T) (dataDir string, srv *httptest.Server, volID string) {
	t.Helper()
	dataPath, volID := setupVolume(t)
	dataDir = filepath.Join(dataPath, "volumes", volID, "_data")
	if err := syscall.Mkfifo(filepath.Join(dataDir, "pipe"), 0644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "plain.txt"), []byte("plain"), 0644); err != nil {
		t.Fatal(err)
	}
	srv = httptest.NewServer(Handler(filepath.Join(dataPath, "volumes")))
	t.Cleanup(srv.Close)
	return dataDir, srv, volID
}

// quickClient fails a request that hangs instead of letting the test hang.
var quickClient = &http.Client{Timeout: 5 * time.Second}

func quick(t *testing.T, req *http.Request) int {
	t.Helper()
	resp, err := quickClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v (request hung?)", req.Method, req.URL, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestGetNamedPipeRefused(t *testing.T) {
	_, srv, volID := specialFixture(t)
	req, _ := http.NewRequest("GET", srv.URL+"/browse/get?volumeID="+volID+"&path=pipe", nil) //nolint:noctx // test
	if code := quick(t, req); code != http.StatusBadRequest {
		t.Errorf("get pipe = %d, want 400", code)
	}
}

func TestLsNamedPipeRefused(t *testing.T) {
	_, srv, volID := specialFixture(t)
	req, _ := http.NewRequest("GET", srv.URL+"/browse/ls?volumeID="+volID+"&path=pipe", nil) //nolint:noctx // test
	if code := quick(t, req); code == http.StatusOK {
		t.Errorf("ls pipe = %d, want an error", code)
	}
}

func TestPutNamedPipeRefused(t *testing.T) {
	dataDir, srv, volID := specialFixture(t)
	if code := quick(t, putRequest(t, srv.URL+"/browse/put?volumeID="+volID+"&path=pipe")); code != http.StatusBadRequest {
		t.Errorf("put pipe = %d, want 400", code)
	}
	info, err := os.Lstat(filepath.Join(dataDir, "pipe"))
	if err != nil || info.Mode()&fs.ModeNamedPipe == 0 {
		t.Errorf("pipe was replaced: %v %v", info, err)
	}
}

// TestOpenNoFollowRefusesPipe calls the open directly, which is what a swap
// between put's Lstat and its open would reach. Read and write-with-truncate
// must both return errNotRegular without blocking.
func TestOpenNoFollowRefusesPipe(t *testing.T) {
	dataDir, _, _ := specialFixture(t)
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	// Hold the pipe open for reading so a non-blocking write open succeeds
	// and reaches the type check instead of failing with ENXIO.
	rd, err := os.OpenFile(filepath.Join(dataDir, "pipe"), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	for _, flag := range []int{os.O_RDONLY, os.O_WRONLY | os.O_CREATE | os.O_TRUNC} {
		done := make(chan error, 1)
		go func() {
			f, err := openNoFollow(root, "pipe", flag, 0644)
			if f != nil {
				_ = f.Close()
			}
			done <- err
		}()
		select {
		case err := <-done:
			if !errors.Is(err, errNotRegular) {
				t.Errorf("openNoFollow(pipe, %#x) = %v, want errNotRegular", flag, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("openNoFollow(pipe, %#x) hung", flag)
		}
	}

	f, err := openNoFollow(root, "plain.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("regular file refused: %v", err)
	}
	defer f.Close()
	if fl, err := unix.FcntlInt(f.Fd(), unix.F_GETFL, 0); err != nil || fl&unix.O_NONBLOCK != 0 {
		t.Errorf("O_NONBLOCK left set on a regular file: %#x %v", fl, err)
	}
}

// TestOpenNoFollowTruncatesOnlyRegular checks O_TRUNC still applies to a
// regular file now that it runs after the type check.
func TestOpenNoFollowTruncatesOnlyRegular(t *testing.T) {
	dataDir, _, _ := specialFixture(t)
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	f, err := openNoFollow(root, "plain.txt", os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("new"))
	_ = f.Close()
	if got, _ := os.ReadFile(filepath.Join(dataDir, "plain.txt")); string(got) != "new" {
		t.Errorf("after truncate+write got %q, want %q", got, "new")
	}
}

// TestOpenVerifiedRefusesPipe covers the Windows open path, which is compiled
// on every OS. It opens a pipe that has a writer, so it does not block.
func TestOpenVerifiedRefusesPipe(t *testing.T) {
	dataDir, _, _ := specialFixture(t)
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	rd, err := os.OpenFile(filepath.Join(dataDir, "pipe"), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	f, err := openVerified(root, "pipe", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if f != nil {
		_ = f.Close()
	}
	if !errors.Is(err, errNotRegular) {
		t.Errorf("openVerified(pipe) = %v, want errNotRegular", err)
	}
}

// TestDeviceNodeRefused needs mknod, which is privileged; it runs as root (CI
// containers) and skips otherwise. TestCheckRegular covers the mode check.
func TestDeviceNodeRefused(t *testing.T) {
	dataDir, srv, volID := specialFixture(t)
	dev := filepath.Join(dataDir, "null")
	// /dev/null's device number: harmless if the check were missing.
	var st unix.Stat_t
	if err := unix.Stat("/dev/null", &st); err != nil {
		t.Skip(err)
	}
	if err := unix.Mknod(dev, unix.S_IFCHR|0666, int(st.Rdev)); err != nil { // #nosec G115 -- test
		t.Skipf("mknod not permitted: %v", err)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/browse/get?volumeID="+volID+"&path=null", nil) //nolint:noctx // test
	if code := quick(t, req); code != http.StatusBadRequest {
		t.Errorf("get device = %d, want 400", code)
	}
	if code := quick(t, putRequest(t, srv.URL+"/browse/put?volumeID="+volID+"&path=null")); code != http.StatusBadRequest {
		t.Errorf("put device = %d, want 400", code)
	}
}

func TestCheckRegular(t *testing.T) {
	for mode, want := range map[fs.FileMode]bool{
		0o644:                    true,
		fs.ModeDir | 0o755:       false,
		fs.ModeNamedPipe | 0o644: false,
		fs.ModeSocket | 0o644:    false,
		fs.ModeDevice | 0o660:    false,
		fs.ModeDevice | fs.ModeCharDevice | 0o666: false,
		fs.ModeSymlink | 0o777:                    false,
		fs.ModeIrregular:                          false,
	} {
		if got := checkRegular(mode) == nil; got != want {
			t.Errorf("checkRegular(%v) ok = %v, want %v", mode, got, want)
		}
	}
}
