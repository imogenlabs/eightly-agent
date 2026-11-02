// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/imogenlabs/eightly-agent/internal/config"
	"github.com/imogenlabs/eightly-agent/internal/server"
	"github.com/imogenlabs/eightly-agent/internal/tlsutil"
)

// healthcheckTimeout sits under the image's HEALTHCHECK --timeout (5s) so the
// check reports its own failure instead of being killed for it.
const healthcheckTimeout = 3 * time.Second

// runHealthcheck is `eightly-agent healthcheck`: exit 0 when this agent's own
// listener answers /healthz, 1 otherwise. It reads the same --addr/--port/--data
// flags and AGENT_PORT/DATA_PATH variables as the agent so it asks the port the
// agent serves and trusts the certificate the agent loads, and it skips Validate because a liveness probe needs no secret.
func runHealthcheck(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfg, err := config.ParseArgs(fs, args, version)
	if err != nil {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()
	if err := server.Healthcheck(ctx, cfg.Addr, cfg.Port, tlsutil.CertPath(cfg.DataPath)); err != nil {
		_, _ = fmt.Fprintln(stderr, "unhealthy:", err)
		return 1
	}
	return 0
}

func healthcheckMain() {
	os.Exit(runHealthcheck(os.Args[2:], os.Stderr))
}
