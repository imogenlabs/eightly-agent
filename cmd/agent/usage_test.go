// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintUsage_ListsCommandsAndFlags(t *testing.T) {
	var out bytes.Buffer
	printUsage(&out)
	for _, want := range []string{"eightly-agent help", "eightly-agent healthcheck", "-server-url", "-volumes-path", "-secret-file"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("usage does not mention %s:\n%s", want, out.String())
		}
	}
}

// A word the agent does not know used to start it with every flag after the
// word dropped, so `eightly-agent strat --port 9002` ran on 9001.
func TestBadArgument_ExitsNonZeroWithUsage(t *testing.T) {
	var out bytes.Buffer
	if code := badArgument("strat", &out); code == 0 {
		t.Fatal("an unknown argument exited 0")
	}
	if !strings.Contains(out.String(), `"strat"`) || !strings.Contains(out.String(), "Usage:") {
		t.Errorf("no reason or usage:\n%s", out.String())
	}
}
