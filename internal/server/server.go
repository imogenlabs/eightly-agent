// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"

	"github.com/smashingtags/eightly-agent/internal/auth"
	"github.com/smashingtags/eightly-agent/internal/browse"
	"github.com/smashingtags/eightly-agent/internal/config"
	"github.com/smashingtags/eightly-agent/internal/hostinfo"
	"github.com/smashingtags/eightly-agent/internal/metrics"
	"github.com/smashingtags/eightly-agent/internal/proxy"
	"github.com/smashingtags/eightly-agent/internal/register"
	"github.com/smashingtags/eightly-agent/internal/tlsutil"
	"github.com/smashingtags/eightly-agent/internal/websocket"
)

// Run starts the agent's HTTPS server and blocks until ctx is cancelled or a fatal error occurs.
func Run(ctx context.Context, cfg *config.Config) error {
	log.Info().
		Str("version", cfg.Version).
		Str("port", cfg.Port).
		Str("socket", cfg.SocketPath).
		Msg("eightly-agent starting")

	serverTLS, err := register.BuildServerTLSConfig(cfg.ServerCA, cfg.InsecureServerTLS, log.Logger)
	if err != nil {
		return fmt.Errorf("failed to build server TLS config: %w", err)
	}

	state := loadOrRegister(cfg, serverTLS)
	if state != nil && cfg.ServerURL != "" {
		go register.StartHeartbeat(ctx, state, cfg.StatePath, cfg.SocketPath, cfg.Version, cfg.AdvertiseIP, serverTLS, log.Logger)
	}

	certSANs := detectCertSANs(cfg)

	certPath, keyPath, err := tlsutil.EnsureCert(cfg.DataPath, certSANs...)
	if err != nil {
		return fmt.Errorf("failed to generate TLS cert: %w", err)
	}

	mux := http.NewServeMux()
	// The registration state pointer can still be nil here and gains an ID later,
	// so the ID is read at request time (EA-35).
	authed := auth.Middleware(cfg.Secret, cfg.InsecureNoAuth, func() string {
		if state == nil {
			return ""
		}
		return state.AgentID
	})
	authEnabled := cfg.Secret != ""

	var allowedOrigins []string
	if cfg.AllowedOrigins != "" {
		for _, o := range strings.Split(cfg.AllowedOrigins, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				allowedOrigins = append(allowedOrigins, o)
			}
		}
	}

	if cfg.MetricsEnabled {
		mux.Handle("GET /metrics", authed(promhttp.Handler()))
	}

	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) // #nosec G104 -- response already committed
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		conn, err := net.DialTimeout("unix", cfg.SocketPath, 2*time.Second)
		if err != nil {
			metrics.DockerSocketReachable.Set(0)
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "error": "docker socket unreachable"}) // #nosec G104 -- response already committed
			return
		}
		conn.Close() // #nosec G104 -- probe connection, close is best-effort
		metrics.DockerSocketReachable.Set(1)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"}) // #nosec G104 -- response already committed
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{ // #nosec G104 -- response already committed
			"version":     cfg.Version,
			"api_version": "2",
		})
	})
	mux.Handle("/websocket/attach", authed(websocket.AttachHandler(cfg.SocketPath, authEnabled, allowedOrigins)))
	mux.Handle("/websocket/exec", authed(websocket.ExecHandler(cfg.SocketPath, authEnabled, allowedOrigins)))
	mux.Handle("/browse/", authed(browse.Handler(cfg.DataPath, browse.ReadOnly(cfg.BrowseReadOnly))))
	mux.Handle("/host", authed(hostinfo.Handler()))
	mux.Handle("/", authed(proxy.NewDockerProxy(cfg.SocketPath)))

	handler := headerMiddleware(mux, cfg.Version)

	addr := fmt.Sprintf("%s:%s", cfg.Addr, cfg.Port)
	tlsCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return fmt.Errorf("failed to load TLS cert: %w", err)
	}

	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			MinVersion:   tls.VersionTLS12,
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
				tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
			},
		},
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Minute,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	errCh := make(chan error, 1)
	// Stopped by srv.Shutdown below when ctx is cancelled.
	go func() {
		log.Info().Str("addr", addr).Msg("listening (TLS)")
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info().Msg("shutting down")
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// loadOrRegister resumes from the saved registration when there is one and
// only enrolls when there is not. Enrolling again on every start is not a
// harmless retry: the enrollment token is single-use, so a second register with
// it is refused and the agent is left with no identity at all.
func loadOrRegister(cfg *config.Config, serverTLS *tls.Config) *register.State {
	state, err := register.LoadState(cfg.StatePath)
	if err != nil {
		log.Warn().Err(err).Msg("failed to load registration state")
	}
	if state != nil || cfg.ServerURL == "" {
		return state
	}
	info := register.DetectInfo(cfg.SocketPath, cfg.Port, cfg.Version, cfg.AdvertiseIP)
	state, err = register.Register(cfg.ServerURL, cfg.EnrollToken, info, serverTLS)
	if err != nil {
		log.Warn().Err(err).Msg("registration failed, will retry via heartbeat")
		return nil
	}
	if err := state.Save(cfg.StatePath); err != nil {
		log.Error().Err(err).Msg("failed to save registration state")
	}
	log.Info().Msg("registered with server")
	return state
}

func detectCertSANs(cfg *config.Config) []string {
	var sans []string
	if h, err := os.Hostname(); err == nil && h != "" {
		sans = append(sans, h)
	}
	if cfg.AdvertiseIP != "" {
		sans = append(sans, cfg.AdvertiseIP)
	}
	if cfg.TLSSANs != "" {
		for _, san := range strings.Split(cfg.TLSSANs, ",") {
			san = strings.TrimSpace(san)
			if san != "" {
				sans = append(sans, san)
			}
		}
	}
	return sans
}

// headerMiddleware sets agent version headers on responses. These headers are
// not written for WebSocket connections — the WS upgrade hijacks the connection
// before response headers are flushed. This is a known limitation; callers
// should read version info from the /ping endpoint instead.
func headerMiddleware(next http.Handler, version string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Eightly-Agent", version)
		w.Header().Set("Eightly-Agent-API-Version", "2")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
