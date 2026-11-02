// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/imogenlabs/eightly-agent/internal/config"
)

// commands are the words the agent takes as its first argument.
const commands = `Usage:
  eightly-agent [flags]           run the agent
  eightly-agent version           print the version
  eightly-agent healthcheck       exit 0 when this agent answers, 1 otherwise
  eightly-agent install|uninstall|start|stop|status
                                  manage the service
  eightly-agent help              show this help

Flags:
`

// printUsage writes the commands and every flag with its default to w.
func printUsage(w io.Writer) {
	fs := flag.NewFlagSet("eightly-agent", flag.ContinueOnError)
	_, _ = config.ParseArgs(fs, nil, version) // registers the flags
	fs.SetOutput(w)
	_, _ = io.WriteString(w, commands)
	fs.PrintDefaults()
}

// badArgument reports an argument the agent does not know, with the usage,
// and returns the exit status. Starting anyway would drop every flag after
// it: flag parsing stops at the first word that is not a flag.
func badArgument(arg string, w io.Writer) int {
	_, _ = fmt.Fprintf(w, "eightly-agent: unknown command or argument %q\n\n", arg)
	printUsage(w)
	return 2
}
