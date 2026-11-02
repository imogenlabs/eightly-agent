// SPDX-License-Identifier: Apache-2.0

package register

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func registerAgainst(t *testing.T, status int, body string) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	_, err := Register(srv.URL, "eos_enroll_x", AgentInfo{Hostname: "h", IP: "10.0.0.2"}, nil)
	if err == nil {
		t.Fatalf("status %d: Register succeeded", status)
	}
	return err
}

// The server takes an enrollment token on first use. If its answer is lost,
// every later attempt with that token is refused, and the agent used to retry
// it forever. A refusal is now told apart from a failure that may pass.
func TestRegister_DefinitiveRefusalIsErrEnrollmentRefused(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusGone} {
		err := registerAgainst(t, status, `{"success":false,"message":"valid enrollment token or agent token required"}`)
		if !errors.Is(err, ErrEnrollmentRefused) {
			t.Errorf("status %d: got %v, want ErrEnrollmentRefused", status, err)
		}
		if !strings.Contains(err.Error(), "valid enrollment token or agent token required") {
			t.Errorf("status %d: the server's reason is missing from %q", status, err)
		}
	}
}

// 400 does not spend the token (the server checks the body first, and a host
// with no network yet reports no IP), 429 and 5xx pass: all worth retrying.
func TestRegister_TransientFailureIsNotRefusal(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		if err := registerAgainst(t, status, `{"message":"x"}`); errors.Is(err, ErrEnrollmentRefused) {
			t.Errorf("status %d was treated as a definitive refusal: %v", status, err)
		}
	}
}

func TestRegister_RefusalMessageIsOneShortLine(t *testing.T) {
	err := registerAgainst(t, http.StatusForbidden, `{"message":"bad\nline`+strings.Repeat("x", 500)+`"}`)
	if strings.Contains(err.Error(), "\n") || len(err.Error()) > 300 {
		t.Fatalf("server message not cleaned: %q", err)
	}
}

func TestBackupState_CopiesStatePrivately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "eightly-agent.json")
	s := &State{AgentID: "agent-old", Token: "old-token", ServerURL: "https://a.example", ServerPin: "sha256/x"}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(path)

	bak, err := BackupState(path)
	if err != nil {
		t.Fatalf("BackupState: %v", err)
	}
	if bak != path+".bak" {
		t.Errorf("backup at %s, want %s.bak", bak, path)
	}
	got, err := os.ReadFile(bak)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("backup holds %q (%v), want %q", got, err, want)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(bak); fi.Mode().Perm() != 0o600 {
			t.Errorf("backup mode %04o, want 0600", fi.Mode().Perm())
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the state file itself was moved: %v", err)
	}
}

func TestBackupState_MissingStateFails(t *testing.T) {
	if _, err := BackupState(filepath.Join(t.TempDir(), "none.json")); err == nil {
		t.Fatal("backing up a missing state file succeeded")
	}
}
