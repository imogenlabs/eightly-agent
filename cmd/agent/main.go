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

	"github.com/smashingtags/eightly-agent/internal/config"
	"github.com/smashingtags/eightly-agent/internal/server"
)

var version = "dev"

func main() {
	// Check for subcommands before flag parsing
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Println(version)
			return
		case "install", "uninstall", "start", "stop", "status":
			handleServiceCommand(os.Args[1])
			return
		}
	}

	cfg := config.Parse(version)

	// Handle --version flag
	if cfg.Version == version {
		for _, arg := range os.Args[1:] {
			if arg == "--version" || arg == "-version" {
				fmt.Println(version)
				return
			}
		}
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
