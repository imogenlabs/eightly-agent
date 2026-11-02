//go:build !windows

package hostinfo

import (
	"context"
	"testing"
	"time"
)

// TestRunCommandTimesOut: a hung host command is killed when its context
// ends, and the call returns promptly.
func TestRunCommandTimesOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runCommand(ctx, "sleep", "30")
	if err == nil {
		t.Fatal("expected an error from a timed-out command")
	}
	if d := time.Since(start); d > waitDelay+2*time.Second {
		t.Errorf("runCommand took %v after a 100ms timeout", d)
	}
}

// TestRunCommandChildHoldsPipe: a child that outlives the killed command and
// keeps stdout open does not hold the call past waitDelay.
func TestRunCommandChildHoldsPipe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _ = runCommand(ctx, "sh", "-c", "sleep 6 & sleep 6")
	if d := time.Since(start); d > waitDelay+2*time.Second {
		t.Errorf("runCommand took %v with a child holding the pipe", d)
	}
}

func TestRunCommandOutput(t *testing.T) {
	out, err := runCommand(context.Background(), "echo", "hi")
	if err != nil || string(out) != "hi\n" {
		t.Errorf("runCommand(echo hi) = %q, %v", out, err)
	}
}
