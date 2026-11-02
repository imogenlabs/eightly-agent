// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
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
		switch os.Args[1] {
		case "version":
			fmt.Println(version)
			return
		case "healthcheck":
			healthcheckMain()
			return
		case "install", "uninstall", "start", "stop", "status":
			handleServiceCommand(os.Args[1])
			return
		}
	}

	cfg := config.Parse(version)

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
