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

var dockerListEndpoints = map[string]string{
	"/containers": "/containers/json",
	"/images":     "/images/json",
	"/volumes":    "/volumes",
	"/networks":   "/networks",
}

func rewriteDockerPath(path string) string {
	for short, full := range dockerListEndpoints {
		if path == short {
			return full
		}
	}
	return path
}
