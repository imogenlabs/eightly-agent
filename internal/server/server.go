// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

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

	state, err := register.LoadState(cfg.StatePath)
	if err != nil {
		log.Warn().Err(err).Msg("failed to load registration state")
	}
	if state == nil && cfg.ServerURL != "" {
		info := register.DetectInfo(cfg.SocketPath, cfg.Port, cfg.Version, cfg.AdvertiseIP)
		state, err = register.Register(cfg.ServerURL, cfg.EnrollToken, info, serverTLS)
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
		go register.StartHeartbeat(ctx, state, cfg.StatePath, cfg.SocketPath, cfg.Version, cfg.AdvertiseIP, serverTLS, log.Logger)
	}

	// Build extra SANs for the self-signed cert from hostname, detected IP, and --tls-san flag
	agentInfo := register.DetectInfo(cfg.SocketPath, cfg.Port, cfg.Version, cfg.AdvertiseIP)
	var certSANs []string
	if agentInfo.Hostname != "" {
		certSANs = append(certSANs, agentInfo.Hostname)
	}
	if agentInfo.IP != "" {
		certSANs = append(certSANs, agentInfo.IP)
	}
	if cfg.TLSSANs != "" {
		for _, san := range strings.Split(cfg.TLSSANs, ",") {
			san = strings.TrimSpace(san)
			if san != "" {
				certSANs = append(certSANs, san)
			}
		}
	}

	certPath, keyPath, err := tlsutil.EnsureCert(cfg.DataPath, certSANs...)
	if err != nil {
		return fmt.Errorf("failed to generate TLS cert: %w", err)
	}

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
		},
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 30 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	errCh := make(chan error, 1)
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

// headerMiddleware sets agent version headers on responses. These headers are
// not written for WebSocket connections — the WS upgrade hijacks the connection
// before response headers are flushed. This is a known limitation; callers
// should read version info from the /ping endpoint instead.
func headerMiddleware(next http.Handler, version string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Eightly-Agent", version)
		w.Header().Set("Eightly-Agent-API-Version", "2")
		next.ServeHTTP(w, r)
	})
}
