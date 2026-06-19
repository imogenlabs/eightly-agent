// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

var versionPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

// maxInspectBody caps how much of a create body we buffer for inspection. Real
// container/exec create bodies are small (a few KB); anything beyond this is
// either abuse or non-create traffic, so we cap to avoid unbounded memory use.
const maxInspectBody = 1 << 20 // 1 MiB

// NewDockerProxy returns a reverse proxy that forwards HTTP requests to the Docker daemon via a Unix socket.
func NewDockerProxy(socketPath string) http.Handler {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
		DisableCompression: true,
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = "docker"
			req.URL.Path = versionPrefix.ReplaceAllString(req.URL.Path, "")
			req.URL.Path = rewriteDockerPath(req.URL.Path)
		},
		Transport:     transport,
		FlushInterval: 100 * time.Millisecond,
	}

	// Wrap the proxy with a defense-in-depth deny filter. The agent forwards to
	// the Docker socket, which is host root: a malicious create body can grant a
	// container full host takeover (privileged, host namespaces, sensitive bind
	// mounts). Auth gates *who* can reach the proxy; this gates *what* they can do.
	return denyDangerousCreates(proxy)
}

// denyDangerousCreates inspects mutating Docker create calls and rejects the
// ones that would let a container escape to the host. It is intentionally
// conservative: only POSTs to container/exec create endpoints are parsed, and
// only the well-documented escape vectors are denied. Everything else —
// read/list, normal creates, start/stop/logs — passes through untouched.
func denyDangerousCreates(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isCreateEndpoint(r) {
			next.ServeHTTP(w, r)
			return
		}

		// Buffer the body so we can both inspect it and replay it to the daemon.
		body, err := io.ReadAll(io.LimitReader(r.Body, maxInspectBody))
		_ = r.Body.Close()
		if err != nil {
			deny(w, r, "unreadable request body")
			return
		}
		// Restore the body for the downstream proxy regardless of outcome.
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))

		if reason := inspectCreateBody(body); reason != "" {
			deny(w, r, reason)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// isCreateEndpoint reports whether the request targets a container-create or
// exec-create endpoint. The version prefix (/v1.45/...) is stripped before the
// Director runs, but this filter executes BEFORE the Director, so we match the
// raw path and tolerate the optional version prefix.
func isCreateEndpoint(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	p := versionPrefix.ReplaceAllString(r.URL.Path, "")
	// POST /containers/create  and  POST /containers/{id}/exec  ... wait: exec
	// is created via /containers/{id}/exec (config carries Privileged). Docker
	// also exposes /exec/{id}/start (no escape config). We inspect the two
	// endpoints whose body carries host-escape configuration.
	if p == "/containers/create" {
		return true
	}
	// /containers/{id}/exec — exec create with a Privileged flag.
	if strings.HasPrefix(p, "/containers/") && strings.HasSuffix(p, "/exec") {
		return true
	}
	return false
}

// createBody is a minimal view of the Docker container/exec create payload —
// only the fields that carry host-escape capability. Unknown fields are ignored
// by encoding/json, so this stays forward-compatible with newer API versions.
type createBody struct {
	// Top-level on exec create.
	Privileged *bool `json:"Privileged"`

	HostConfig *struct {
		Privileged  *bool    `json:"Privileged"`
		PidMode     string   `json:"PidMode"`
		IpcMode     string   `json:"IpcMode"`
		NetworkMode string   `json:"NetworkMode"`
		UsernsMode  string   `json:"UsernsMode"`
		CapAdd      []string `json:"CapAdd"`
		// Binds is the legacy "src:dst[:opts]" form.
		Binds []string `json:"Binds"`
		// Mounts is the modern structured form.
		Mounts []struct {
			Type   string `json:"Type"`
			Source string `json:"Source"`
		} `json:"Mounts"`
	} `json:"HostConfig"`
}

// dangerousCaps are Linux capabilities that effectively grant host control or
// break the container boundary. Granting any of these is treated as an escape.
var dangerousCaps = map[string]struct{}{
	"ALL":             {},
	"SYS_ADMIN":       {},
	"SYS_PTRACE":      {},
	"SYS_MODULE":      {},
	"SYS_RAWIO":       {},
	"DAC_READ_SEARCH": {},
	"DAC_OVERRIDE":    {},
	"NET_ADMIN":       {},
	"SYS_BOOT":        {},
	"SYS_CHROOT":      {},
}

// sensitivePaths are host paths that must never be bind-mounted into a
// container. A mount of any of these (or a parent of them) is a host takeover.
var sensitivePaths = []string{
	"/",
	"/etc",
	"/root",
	"/proc",
	"/sys",
	"/var/run/docker.sock",
	"/run/docker.sock",
}

// inspectCreateBody returns a non-empty reason string if the create body
// requests a host-escape capability, or "" if the body is safe. A body that
// fails to parse as JSON is allowed through (the daemon will reject malformed
// input itself); we only act on fields we can positively identify as dangerous.
func inspectCreateBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var c createBody
	if err := json.Unmarshal(body, &c); err != nil {
		// Not JSON we understand — don't block normal traffic on a parse miss.
		return ""
	}

	if c.Privileged != nil && *c.Privileged {
		return "privileged exec/create rejected"
	}

	hc := c.HostConfig
	if hc == nil {
		return ""
	}

	if hc.Privileged != nil && *hc.Privileged {
		return "HostConfig.Privileged=true rejected"
	}
	if isHostNamespace(hc.PidMode) {
		return "host PID namespace rejected"
	}
	if isHostNamespace(hc.IpcMode) {
		return "host IPC namespace rejected"
	}
	if isHostNamespace(hc.NetworkMode) {
		return "host network namespace rejected"
	}
	if hc.UsernsMode == "host" {
		return "host user namespace rejected"
	}
	for _, cap := range hc.CapAdd {
		norm := strings.TrimPrefix(strings.ToUpper(cap), "CAP_")
		if _, bad := dangerousCaps[norm]; bad {
			return "dangerous capability rejected: " + cap
		}
	}
	for _, bind := range hc.Binds {
		// Binds: "source:target[:opts]" — the source is the host path.
		src := bind
		if i := strings.IndexByte(bind, ':'); i >= 0 {
			src = bind[:i]
		}
		if isSensitiveHostPath(src) {
			return "sensitive host bind mount rejected: " + src
		}
	}
	for _, m := range hc.Mounts {
		if strings.EqualFold(m.Type, "bind") && isSensitiveHostPath(m.Source) {
			return "sensitive host bind mount rejected: " + m.Source
		}
	}
	return ""
}

// isHostNamespace reports whether a namespace mode shares the host namespace
// ("host" exactly, or "host:..." which Docker does not use but we guard anyway).
func isHostNamespace(mode string) bool {
	return mode == "host" || strings.HasPrefix(mode, "host:")
}

// isSensitiveHostPath reports whether host path p is, or lives under, a path we
// refuse to expose. Paths are cleaned and compared with a trailing separator so
// "/etc" matches "/etc/shadow" but not "/etchmm".
func isSensitiveHostPath(p string) bool {
	if p == "" {
		return false
	}
	clean := cleanPath(p)
	for _, s := range sensitivePaths {
		if clean == s {
			return true
		}
		// "/" as a sensitive root would match everything via the prefix test
		// below; handle it explicitly: any absolute mount of the root.
		if s == "/" {
			if clean == "/" {
				return true
			}
			continue
		}
		if strings.HasPrefix(clean, s+"/") {
			return true
		}
	}
	return false
}

// cleanPath normalizes a host path for comparison: trims whitespace and any
// trailing slash (except for the lone root "/").
func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	return p
}

func deny(w http.ResponseWriter, r *http.Request, reason string) {
	log.Warn().Msgf("[SECURITY] proxy.denied method=%s path=%s ip=%s reason=%q",
		r.Method, r.URL.Path, r.RemoteAddr, reason)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	// Encode rather than string-concat: the reason may echo an attacker-supplied
	// bind path or cap name, so it must be JSON-escaped.
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "forbidden: " + reason})
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
