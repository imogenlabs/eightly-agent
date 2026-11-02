// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/imogenlabs/eightly-agent/internal/config"
	"github.com/imogenlabs/eightly-agent/internal/server"
)

var version = "dev"

func main() {
	// Check for subcommands before flag parsing
	if len(os.Args) > 1 {
		switch arg := os.Args[1]; {
		case arg == "version":
			fmt.Println(version)
			return
		case arg == "healthcheck":
			healthcheckMain()
			return
		case arg == "install", arg == "uninstall", arg == "start", arg == "stop", arg == "status":
			handleServiceCommand(arg)
			return
		case arg == "help":
			printUsage(os.Stdout)
			return
		case !strings.HasPrefix(arg, "-"):
			os.Exit(badArgument(arg, os.Stderr))
		}
	}

	// -h and a bad flag print the same usage, to stderr, through the flag
	// package: -h exits 0 and a bad flag 2.
	flag.CommandLine.Usage = func() { printUsage(flag.CommandLine.Output()) }
	cfg := config.Parse(version)
	if flag.NArg() > 0 {
		os.Exit(badArgument(flag.Arg(0), os.Stderr))
	}

	if cfg.ShowVersion {
		fmt.Println(version)
		return
	}

	if err := cfg.Validate(); err != nil {
		log.Fatal().Err(err).Msg("invalid configuration")
	}
	if cfg.InsecureNoAuth && cfg.Secret == "" {
		log.Warn().Str("port", cfg.Port).Msg("Authentication is DISABLED. The Docker socket is accessible to anyone who can reach this port.")
	}

	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	if level == zerolog.DebugLevel || level == zerolog.TraceLevel {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	}

	if isWindowsService() {
		runAsService(cfg)
		return
	}

	// Interactive mode
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx, cfg); err != nil {
		log.Fatal().Err(err).Msg("server error")
	}
}
