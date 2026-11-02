package websocket

import (
	"net/http"
	"strings"
	"testing"

	"github.com/imogenlabs/eightly-agent/internal/proxy"
)

// A container that does not exist is refused with 404, never attached to by
// name: a container renamed to that name in the meantime would be reached
// unchecked. An unknown exec instance is refused too.
func TestAttach_MissingTargetRefused(t *testing.T) {
	sock, started := fakeDaemon(t)
	targets := proxy.NewTargetChecker(sock, nil)
	if code, body := dialSession(t, AttachHandler(sock, targets, false, nil), "/websocket/attach", strings.Repeat("9", 64)); code != http.StatusNotFound {
		t.Errorf("attach to a missing container = %d %q, want 404", code, body)
	}
	if code, body := dialSession(t, ExecHandler(sock, targets, false, nil), "/websocket/exec", strings.Repeat("9", 12)); code != http.StatusForbidden {
		t.Errorf("start of a missing exec = %d %q, want 403", code, body)
	}
	select {
	case s := <-started:
		t.Errorf("Docker was asked to %s", s)
	default:
	}
}
