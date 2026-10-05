// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/smashingtags/eightly-agent/internal/config"
	"github.com/smashingtags/eightly-agent/internal/server"
)

// healthcheckTimeout sits under the image's HEALTHCHECK --timeout (5s) so the
// check reports its own failure instead of being killed for it.
const healthcheckTimeout = 3 * time.Second

// runHealthcheck is `eightly-agent healthcheck`: exit 0 when this agent's own
// listener answers /healthz, 1 otherwise. It reads the same --addr/--port flags
// and AGENT_PORT variable as the agent so it asks the port the agent serves,
// and it skips Validate because a liveness probe needs no secret.
func runHealthcheck(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfg, err := config.ParseArgs(fs, args, version)
	if err != nil {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()
	if err := server.Healthcheck(ctx, cfg.Addr, cfg.Port); err != nil {
		_, _ = fmt.Fprintln(stderr, "unhealthy:", err)
		return 1
	}
	return 0
}

func healthcheckMain() {
	os.Exit(runHealthcheck(os.Args[2:], os.Stderr))
}
