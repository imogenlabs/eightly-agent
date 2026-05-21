// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"regexp"
	"time"
)

var versionPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

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

	return proxy
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
