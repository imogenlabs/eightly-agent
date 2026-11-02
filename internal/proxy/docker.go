// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/imogenlabs/eightly-agent/internal/dockerdial"
	"github.com/rs/zerolog/log"
)

var versionPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

// NewDockerProxy returns a reverse proxy that forwards HTTP requests to the
// Docker daemon via a Unix socket. Only the endpoints on the allow-list pass,
// and containers may bind-mount host paths only under bindRoots.
func NewDockerProxy(socketPath string, bindRoots []string) http.Handler {
	transport := dockerTransport(socketPath)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Rewrite strips Forwarded and X-Forwarded-* from the outbound request
			// and drops unparsable query parameters. The old Director passed the
			// client's values through and appended the client IP to X-Forwarded-For,
			// so restore exactly that.
			for _, h := range []string{"Forwarded", "X-Forwarded-Host", "X-Forwarded-Proto"} {
				if v, ok := pr.In.Header[h]; ok {
					pr.Out.Header[h] = v
				}
			}
			pr.Out.Header["X-Forwarded-For"] = pr.In.Header["X-Forwarded-For"]
			if clientIP, _, err := net.SplitHostPort(pr.In.RemoteAddr); err == nil {
				if prior := pr.Out.Header["X-Forwarded-For"]; len(prior) > 0 {
					clientIP = strings.Join(prior, ", ") + ", " + clientIP
				}
				pr.Out.Header.Set("X-Forwarded-For", clientIP)
			}

			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "docker"
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			// The decoded path the filter matched, never the client's own
			// escaping: an escaped slash would otherwise reach the daemon as
			// a different path from the one that was checked.
			pr.Out.URL.Path = dockerPath(pr.In.URL.Path)
			pr.Out.URL.RawPath = ""
		},
		Transport:     transport,
		FlushInterval: 100 * time.Millisecond,
	}

	// The Docker socket is host root: one unchecked request can hand a
	// container the host. Auth gates *who* reaches the proxy; the filter
	// gates *what* they can ask for.
	f := newFilter(proxy, bindRoots, &dockerAPI{client: &http.Client{Transport: transport.Clone()}})
	proxy.ModifyResponse = f.redactInspect
	return f
}

// dockerTransport reaches the daemon on socketPath.
func dockerTransport(socketPath string) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dockerdial.DialContext(ctx, socketPath)
		},
		DisableCompression: true,
	}
}

// dockerPath is the path the daemon will route on. The filter and the proxy
// both use it, so what is inspected is exactly what is forwarded: a path that
// is cleaned, with every leading /vX.Y prefix removed (a single strip would
// let /v1.45/v1.45/containers/create reach the daemon as a create while the
// filter saw something else).
func dockerPath(p string) string {
	p = path.Clean("/" + p)
	for {
		stripped := versionPrefix.ReplaceAllString(p, "")
		if stripped == p {
			break
		}
		p = path.Clean("/" + stripped)
	}
	return rewriteDockerPath(p)
}

func rewriteDockerPath(path string) string {
	switch path {
	case "/containers":
		return "/containers/json"
	case "/images":
		return "/images/json"
	case "/volumes":
		return "/volumes"
	case "/networks":
		return "/networks"
	default:
		return path
	}
}

func deny(w http.ResponseWriter, r *http.Request, status int, reason string) {
	log.Warn().Msgf("[SECURITY] proxy.denied method=%s path=%s ip=%s status=%d reason=%q",
		r.Method, r.URL.Path, r.RemoteAddr, status, reason)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Encoded, not concatenated: the reason may echo attacker-supplied text.
	msg := "forbidden: "
	if status == http.StatusRequestEntityTooLarge {
		msg = "too large: "
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg + reason})
}
