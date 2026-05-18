package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/smashingtags/eightly-agent/internal/auth"
	"github.com/smashingtags/eightly-agent/internal/browse"
	"github.com/smashingtags/eightly-agent/internal/config"
	"github.com/smashingtags/eightly-agent/internal/hostinfo"
	"github.com/smashingtags/eightly-agent/internal/proxy"
	"github.com/smashingtags/eightly-agent/internal/register"
	"github.com/smashingtags/eightly-agent/internal/tlsutil"
	"github.com/smashingtags/eightly-agent/internal/websocket"
)

var version = "dev"

func main() {
	cfg := config.Parse(version)

	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	if level == zerolog.DebugLevel || level == zerolog.TraceLevel {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	}

	log.Info().
		Str("version", cfg.Version).
		Str("port", cfg.Port).
		Str("socket", cfg.SocketPath).
		Msg("eightly-agent starting")

	// Graceful shutdown context
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Registration and heartbeat
	state, _ := register.LoadState(cfg.StatePath)
	if state == nil && cfg.ServerURL != "" {
		info := register.DetectInfo(cfg.SocketPath, cfg.Port, cfg.Version)
		state, err = register.Register(cfg.ServerURL, cfg.EnrollToken, info)
		if err != nil {
			log.Warn().Err(err).Msg("registration failed, will retry via heartbeat")
		} else {
			if err := state.Save(cfg.StatePath); err != nil {
				log.Error().Err(err).Msg("failed to save registration state")
			}
			log.Info().Msg("registered with server")
		}
	}
	if state != nil && cfg.ServerURL != "" {
		go register.StartHeartbeat(ctx, state, cfg.StatePath, cfg.Version, log.Logger)
	}

	// TLS
	certPath, keyPath, err := tlsutil.EnsureCert(cfg.DataPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to generate TLS cert")
	}

	// Routes
	mux := http.NewServeMux()
	authed := auth.Middleware(cfg.Secret)

	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("/websocket/attach", authed(websocket.AttachHandler(cfg.SocketPath)))
	mux.Handle("/websocket/exec", authed(websocket.ExecHandler(cfg.SocketPath)))
	mux.Handle("/browse/", authed(browse.Handler(cfg.DataPath)))
	mux.Handle("/host", authed(hostinfo.Handler()))
	mux.Handle("/", authed(proxy.NewDockerProxy(cfg.SocketPath)))

	handler := headerMiddleware(mux)

	// Server
	addr := fmt.Sprintf("%s:%s", cfg.Addr, cfg.Port)
	tlsCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load TLS cert")
	}

	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
		},
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 30 * time.Minute,
	}

	go func() {
		log.Info().Str("addr", addr).Msg("listening (TLS)")
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server error")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
}

func headerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Eightly-Agent", version)
		w.Header().Set("Eightly-Agent-API-Version", "2")
		next.ServeHTTP(w, r)
	})
}
