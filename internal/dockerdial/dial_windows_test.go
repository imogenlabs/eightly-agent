//go:build windows

// SPDX-License-Identifier: Apache-2.0

package dockerdial

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

// TestDialNamedPipe serves a real named pipe and dials it in both spellings the
// agent accepts, the way it reaches Docker Desktop.
func TestDialNamedPipe(t *testing.T) {
	name := fmt.Sprintf("eightly-agent-test-%d", os.Getpid())
	ln, err := winio.ListenPipe(`\\.\pipe\`+name, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("ok"))
			_ = c.Close()
		}
	}()
	for _, path := range []string{`//./pipe/` + name, `\\.\pipe\` + name} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		conn, err := DialContext(ctx, path)
		cancel()
		if err != nil {
			t.Fatalf("dial %s: %v", path, err)
		}
		buf := make([]byte, 2)
		_, err = conn.Read(buf)
		_ = conn.Close()
		if err != nil || string(buf) != "ok" {
			t.Fatalf("%s: read %q, %v", path, buf, err)
		}
	}
}
