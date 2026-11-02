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
	"path/filepath"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"

	"github.com/imogenlabs/eightly-agent/internal/auth"
	"github.com/imogenlabs/eightly-agent/internal/browse"
	"github.com/imogenlabs/eightly-agent/internal/config"
	"github.com/imogenlabs/eightly-agent/internal/hostinfo"
	"github.com/imogenlabs/eightly-agent/internal/metrics"
	"github.com/imogenlabs/eightly-agent/internal/proxy"
	"github.com/imogenlabs/eightly-agent/internal/register"
	"github.com/imogenlabs/eightly-agent/internal/tlsutil"
	"github.com/imogenlabs/eightly-agent/internal/websocket"
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

	states := register.NewStateHolder(nil)
	state, unsaved := resume(cfg, serverTLS)
	if state != nil {
		states.Store(state)
	}
	if cfg.ServerURL != "" {
		// Started here, not after a successful registration: an agent that
		// could not register at startup keeps trying in the background, and
		// until it does its authenticated routes answer 503.
		go func() {
			if state == nil {
				if state, unsaved = registerWithRetry(ctx, cfg, serverTLS); state == nil {
					return
				}
				states.Store(state)
			}
			register.StartHeartbeat(ctx, states, unsaved, cfg.StatePath, cfg.SocketPath, cfg.Version, cfg.AdvertiseIP, serverTLS, log.Logger)
		}()
	}

	certSANs := detectCertSANs(cfg)

	certPath, keyPath, err := tlsutil.EnsureCert(cfg.DataPath, certSANs...)
	if err != nil {
		return fmt.Errorf("failed to generate TLS cert: %w", err)
	}

	mux := http.NewServeMux()
	authed := authMiddleware(cfg, states)
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

// authMiddleware checks tokens on every route that needs one. The agent ID is
// read per request from states, because the agent learns it only once
// registration succeeds and the heartbeat swaps the state when it renews.
//
// An agent that is meant to register (a server URL is set) and enforces
// tokens answers 503 on those routes until it knows its own ID: without it the
// token subject cannot be checked, and any agent's token would open this one.
// Local-only mode (no server URL) never has an ID and checks only the
// signature, and --insecure-no-auth without a secret checks nothing, both as
// documented.
func authMiddleware(cfg *config.Config, states *register.StateHolder) func(http.Handler) http.Handler {
	check := auth.Middleware(cfg.Secret, cfg.InsecureNoAuth, states.AgentID)
	if cfg.Secret == "" || cfg.ServerURL == "" {
		return check
	}
	return func(next http.Handler) http.Handler {
		checked := check(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if states.AgentID() == "" {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"message":"service unavailable: agent is not registered with the server yet"}`)) // #nosec G104 -- HTTP response write
				return
			}
			checked.ServeHTTP(w, r)
		})
	}
}

// Registration retry pacing, variables so a test need not wait minutes.
var (
	registerRetryMin = 5 * time.Second
	registerRetryMax = 5 * time.Minute
)

// registerWithRetry repeats resume with a growing pause until it yields a
// registration or ctx ends, in which case it returns nil.
func registerWithRetry(ctx context.Context, cfg *config.Config, serverTLS *tls.Config) (*register.State, bool) {
	wait := registerRetryMin
	for {
		log.Warn().Dur("retry_in", wait).Msg("agent is not registered; authenticated routes answer 503 until it is")
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(wait):
		}
		if state, unsaved := resume(cfg, serverTLS); state != nil {
			return state, unsaved
		}
		wait = min(wait*2, registerRetryMax)
	}
}

// loadOrRegister is resume without the saved flag.
func loadOrRegister(cfg *config.Config, serverTLS *tls.Config) *register.State {
	state, _ := resume(cfg, serverTLS)
	return state
}

// resume resumes from the saved registration when there is one and only
// enrolls when there is not. Enrolling again on every start is not a harmless
// retry: the enrollment token is single-use, so a second register with it is
// refused and the agent is left with no identity at all. unsaved is true when
// a new registration could not be written to disk; the heartbeat retries it.
func resume(cfg *config.Config, serverTLS *tls.Config) (state *register.State, unsaved bool) {
	if tightened, err := register.PrepareStateDir(cfg.StatePath, cfg.DataPath); err != nil {
		log.Warn().Err(err).Msg("registration state directory is too open and could not be tightened; saved state will be refused")
	} else if tightened {
		log.Info().Str("dir", filepath.Dir(cfg.StatePath)).Msg("tightened registration state directory to 0700")
	}
	state, err := register.LoadState(cfg.StatePath)
	if err != nil {
		log.Warn().Err(err).Msg("failed to load registration state")
	}
	if state != nil && (state.AgentID == "" || state.Token == "") {
		// Older agents saved whatever a successful register returned, even
		// an empty identity. An agent with no ID cannot check whose token it
		// was handed, so the saved state is as good as none.
		log.Error().Str("state_path", cfg.StatePath).Msg("saved registration has no agent ID or token; ignoring it and enrolling again")
		state = nil
	}
	if state != nil && register.TokenExpired(state.Token, time.Now()) {
		// A lapsed token can never be renewed. Re-enrolling is
		// running the install command again with a fresh enrollment token, so
		// when one is configured, use it instead of the dead saved state.
		if cfg.EnrollToken == "" {
			log.Error().Msg("saved agent token has expired, so this agent must be re-enrolled: generate an install command on the Agents page and run it on this host")
			return state, false
		}
		log.Warn().Msg("saved agent token has expired; enrolling again with the configured enrollment token")
		if sameServer(state.ServerURL, cfg.ServerURL) {
			// The token lapsed, not the trust: the server must still present
			// the key this agent pinned when it first enrolled.
			register.SeedServerPin(serverTLS, state.ServerPin)
		}
		state = nil
	}
	if state != nil || cfg.ServerURL == "" {
		return state, false
	}
	// A registration that cannot be saved is lost on the next restart, and
	// the single-use enrollment token with it, so it is not spent until the
	// state file is known to be writable.
	if err := register.CheckWritable(cfg.StatePath); err != nil {
		log.Error().Err(err).Str("state_path", cfg.StatePath).Msg("not registering: the registration state cannot be saved; set EIGHTLY_STATE_PATH to a writable file")
		return nil, false
	}
	info := register.DetectInfo(cfg.SocketPath, cfg.Port, cfg.Version, cfg.AdvertiseIP)
	state, err = register.Register(cfg.ServerURL, cfg.EnrollToken, info, serverTLS)
	if err != nil {
		log.Warn().Err(err).Msg("registration failed")
		return nil, false
	}
	if err := state.Save(cfg.StatePath); err != nil {
		log.Error().Err(err).Msg("failed to save registration state; will retry")
		unsaved = true
	}
	log.Info().Str("agent_id", state.AgentID).Msg("registered with server")
	return state, unsaved
}

func sameServer(a, b string) bool {
	return a != "" && strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
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
