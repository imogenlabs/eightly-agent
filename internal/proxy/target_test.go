package proxy

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The WebSocket bridges ask the TargetChecker; it must give the answers the
// proxy gives for exec and attach.
func TestTargetChecker_MatchesTheProxyRules(t *testing.T) {
	tc := &TargetChecker{f: testFilter(nil, testRoots)}
	ctx := context.Background()

	full, reason := tc.Container(ctx, "app")
	if reason != "" || full != idApp {
		t.Fatalf("ordinary container: (%q, %q), want its full ID and no reason", full, reason)
	}
	for _, id := range []string{"eightly-agent", idAgent[:12], "priv", "hostnet", "joiner", "pidjoin", "caps", "etc", "device", "hostvol", "outside"} {
		if full, reason := tc.Container(ctx, id); reason == "" || full != "" {
			t.Errorf("container %s: (%q, %q), want a refusal", id, full, reason)
		}
	}
	if full, reason := tc.Container(ctx, "missing"); reason != "" || full != "" {
		t.Errorf("missing container: (%q, %q), want it left to the daemon", full, reason)
	}

	if reason := tc.Exec(ctx, "exec-app"); reason != "" {
		t.Errorf("exec in an ordinary container refused: %s", reason)
	}
	for _, id := range []string{"exec-agent", "exec-priv", "exec-hostp"} {
		if reason := tc.Exec(ctx, id); reason == "" {
			t.Errorf("exec %s allowed, want a refusal", id)
		}
	}
}

// The real checker asks the daemon over the socket; one it cannot reach is a
// refusal, never a pass.
func TestNewTargetChecker_UnreachableDaemonRefuses(t *testing.T) {
	tc := NewTargetChecker(filepath.Join(t.TempDir(), "missing.sock"), nil)
	if _, reason := tc.Container(context.Background(), idApp); !strings.Contains(reason, "could not be checked") {
		t.Errorf("container: reason %q, want 'could not be checked'", reason)
	}
	if reason := tc.Exec(context.Background(), "0123456789ab"); !strings.Contains(reason, "could not be checked") {
		t.Errorf("exec: reason %q, want 'could not be checked'", reason)
	}
}
