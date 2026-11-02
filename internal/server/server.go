// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"

	"github.com/imogenlabs/eightly-agent/internal/auth"
	"github.com/imogenlabs/eightly-agent/internal/browse"
	"github.com/imogenlabs/eightly-agent/internal/config"
	"github.com/imogenlabs/eightly-agent/internal/dockerdial"
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
	state, unsaved, err := resume(cfg, serverTLS)
	refused := errors.Is(err, register.ErrEnrollmentRefused)
	if err != nil && !refused {
		return err
	}
	if refused {
		logEnrollmentRefused(cfg, err)
	}
	if state != nil {
		states.Store(state)
	}
	if cfg.ServerURL != "" && !refused {
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
			register.StartHeartbeat(ctx, states, unsaved, cfg.StatePath, cfg.SocketPath, cfg.Port, cfg.Version, cfg.AdvertiseIP, serverTLS, log.Logger)
		}()
	}

	certSANs := detectCertSANs(cfg)

	certPath, keyPath, err := tlsutil.EnsureCert(cfg.DataPath, certSANs...)
	if err != nil {
		return fmt.Errorf("failed to generate TLS cert: %w", err)
	}

	bindRoots, err := cfg.BindRoots()
	if err != nil {
		return err
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
		conn, err := dockerdial.DialTimeout(cfg.SocketPath, 2*time.Second)
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
	targets := proxy.NewTargetChecker(cfg.SocketPath, bindRoots)
	mux.Handle("/websocket/attach", authed(websocket.AttachHandler(cfg.SocketPath, targets, authEnabled, allowedOrigins)))
	mux.Handle("/websocket/exec", authed(websocket.ExecHandler(cfg.SocketPath, targets, authEnabled, allowedOrigins)))
	volumes := cfg.VolumesRoot()
	if volumes == "" {
		log.Info().Msg("file browser unavailable: Docker keeps its volumes out of the agent's reach on this host; set --volumes-path if they can be reached")
	} else {
		log.Info().Str("path", volumes).Msg("file browser reads volumes from here")
	}
	mux.Handle("/browse/", authed(browse.Handler(volumes, browse.ReadOnly(cfg.BrowseReadOnly))))
	mux.Handle("/host", authed(hostinfo.Handler()))
	mux.Handle("/", authed(proxy.NewDockerProxy(cfg.SocketPath, bindRoots)))

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
// registration, and returns nil when ctx ends or retrying cannot help: the
// server refused the enrollment token, or the saved state became unusable.
func registerWithRetry(ctx context.Context, cfg *config.Config, serverTLS *tls.Config) (*register.State, bool) {
	wait := registerRetryMin
	for {
		log.Warn().Dur("retry_in", wait).Msg("agent is not registered; authenticated routes answer 503 until it is")
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(wait):
		}
		state, unsaved, err := resume(cfg, serverTLS)
		if state != nil {
			return state, unsaved
		}
		if errors.Is(err, register.ErrEnrollmentRefused) {
			logEnrollmentRefused(cfg, err)
			return nil, false
		}
		if err != nil {
			log.Error().Err(err).Msg("stopped trying to register; authenticated routes answer 503 until the agent is restarted")
			return nil, false
		}
		wait = min(wait*2, registerRetryMax)
	}
}

// logEnrollmentRefused tells the operator what to do once the server has
// refused the enrollment token. The likeliest cause is a token that was
// already spent: the server takes it on first use, so if its answer was lost
// on the way back, every later attempt is refused.
func logEnrollmentRefused(cfg *config.Config, err error) {
	ev := log.Error().Err(err).Str("server_url", cfg.ServerURL)
	if cfg.EnrollToken == "" {
		ev.Msg("the server refused to enroll this agent and no enrollment token is configured: generate an install command on the Agents page and run it on this host. Authenticated routes answer 503 until then")
		return
	}
	ev.Msg("the server refused the enrollment token; it is single-use and has expired or already been used, so retrying cannot help: generate a new install command on the Agents page and run it on this host. Authenticated routes answer 503 until then")
}

// resume resumes from the saved registration when there is one and only
// enrolls when there is not. Enrolling again on every start is not a harmless
// retry: the enrollment token is single-use, so a second register with it is
// refused and the agent is left with no identity at all. unsaved is true when
// a new registration could not be written to disk; the heartbeat retries it.
//
// state == nil with err == nil is a failure worth retrying. An error is not:
// it wraps register.ErrEnrollmentRefused when the server refused the
// enrollment token, and otherwise means the agent must not start (the saved
// state cannot be read, or belongs to another server).
func resume(cfg *config.Config, serverTLS *tls.Config) (state *register.State, unsaved bool, err error) {
	if tightened, err := register.PrepareStateDir(cfg.StatePath, cfg.DataPath); err != nil {
		log.Warn().Err(err).Msg("registration state directory is too open and could not be tightened; saved state will be refused")
	} else if tightened {
		log.Info().Str("dir", filepath.Dir(cfg.StatePath)).Msg("tightened registration state directory to 0700")
	}
	state, err = register.LoadState(cfg.StatePath)
	if err != nil {
		// A missing file is the only case that means "enroll". A file that
		// is there but cannot be read or parsed still holds this agent's
		// identity and server pin, and enrolling would overwrite both.
		return nil, false, fmt.Errorf("cannot use the saved registration at %s, so the agent will not start: %w. "+
			"The file was left as it is. Fix its permissions or owner if that is the problem; "+
			"if it is damaged, move it aside and re-enroll by running a new install command from the Agents page", cfg.StatePath, err)
	}
	if state != nil && (state.AgentID == "" || state.Token == "") {
		// Older agents saved whatever a successful register returned, even
		// an empty identity. An agent with no ID cannot check whose token it
		// was handed, so the saved state is as good as none.
		log.Error().Str("state_path", cfg.StatePath).Msg("saved registration has no agent ID or token; ignoring it and enrolling again")
		state = nil
	}
	switching := false
	if state != nil && cfg.ServerURL != "" && state.ServerURL != "" && !sameServer(state.ServerURL, cfg.ServerURL) {
		// The saved identity and pin belong to another server. Using them
		// would send this agent's token to a server that did not issue it,
		// with a pin that server cannot match.
		if cfg.EnrollToken == "" {
			return nil, false, fmt.Errorf("the saved registration at %s is for server %s, but the configured server URL is %s, so the agent will not start. "+
				"To keep the old server, set the server URL back to %s. "+
				"To move this agent, generate an install command on the new server's Agents page and run it on this host: "+
				"its enrollment token tells the agent to enroll there, and the old registration is kept in %s.bak",
				cfg.StatePath, state.ServerURL, cfg.ServerURL, state.ServerURL, cfg.StatePath)
		}
		log.Warn().Str("saved_server", state.ServerURL).Str("server_url", cfg.ServerURL).
			Msg("saved registration is for another server; an enrollment token is configured, so enrolling with the configured server")
		switching = true
		state = nil
	}
	if state != nil && register.TokenExpired(state.Token, time.Now()) {
		// A lapsed token can never be renewed. Re-enrolling is
		// running the install command again with a fresh enrollment token, so
		// when one is configured, use it instead of the dead saved state.
		if cfg.EnrollToken == "" {
			log.Error().Msg("saved agent token has expired, so this agent must be re-enrolled: generate an install command on the Agents page and run it on this host")
			return state, false, nil
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
		return state, false, nil
	}
	// A registration that cannot be saved is lost on the next restart, and
	// the single-use enrollment token with it, so it is not spent until the
	// state file is known to be writable.
	if err := register.CheckWritable(cfg.StatePath); err != nil {
		log.Error().Err(err).Str("state_path", cfg.StatePath).Msg("not registering: the registration state cannot be saved; set EIGHTLY_STATE_PATH to a writable file")
		return nil, false, nil
	}
	if switching {
		// Taken before the token is spent: once the new registration is
		// saved, the old one exists only in the backup.
		bak, err := register.BackupState(cfg.StatePath)
		if err != nil {
			log.Error().Err(err).Str("state_path", cfg.StatePath).Msg("not registering with the new server: the old registration could not be backed up")
			return nil, false, nil
		}
		log.Info().Str("backup", bak).Msg("backed up the registration with the previous server")
	}
	info := register.DetectInfo(cfg.SocketPath, cfg.Port, cfg.Version, cfg.AdvertiseIP)
	state, err = register.Register(cfg.ServerURL, cfg.EnrollToken, info, serverTLS)
	if errors.Is(err, register.ErrEnrollmentRefused) {
		return nil, false, err
	}
	if err != nil {
		log.Warn().Err(err).Msg("registration failed")
		return nil, false, nil
	}
	if err := state.Save(cfg.StatePath); err != nil {
		log.Error().Err(err).Msg("failed to save registration state; will retry")
		unsaved = true
	}
	log.Info().Str("agent_id", state.AgentID).Msg("registered with server")
	return state, unsaved, nil
}

// sameServer reports whether two server URLs name the same server: scheme and
// host compared without case, a default port the same as none, and a
// trailing slash ignored. An empty or unparsable URL matches nothing.
func sameServer(a, b string) bool {
	na, nb := normalizeServerURL(a), normalizeServerURL(b)
	return na != "" && na == nb
}

func normalizeServerURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host + strings.TrimRight(u.EscapedPath(), "/")
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
