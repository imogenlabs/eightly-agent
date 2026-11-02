// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"net/http"
	"regexp"
	"strings"
)

// check names what the filter does with a request on an allowed endpoint
// before forwarding it.
type check int

const (
	checkNone            check = iota
	checkContainerCreate       // body: container config
	checkContainerUpdate       // body: resources
	checkExecCreate            // body, then the target container
	checkExecStart             // the exec's container
	checkTarget                // the target container (attach, archive upload)
	checkPrivateRead           // refuse on the agent's own or a socket-holding container
	checkStart                 // bind sources re-checked for symlinks
	checkVolumeCreate          // body: driver and options
	checkNetworkCreate         // body: driver
	checkNetworkConnect        // the network named in the path
	checkImagePull             // query: no import from a URL or tarball
)

type route struct {
	method  string
	pattern string // segments; {id} is one Docker name or ID, {name+} one or more segments
	check   check
}

// routes is the allow-list. A method and path not listed here is refused.
// GET routes also answer HEAD. Paths are as dockerPath returns them.
//
// Refused by omission, among others: /build, /session, /grpc, /commit,
// /containers/{id}/export, /containers/{id}/archive (GET and HEAD),
// /images/load, /images/get, /images/{name}/push, /auth, and every path
// under /swarm, /services, /tasks, /nodes, /secrets, /configs, /plugins and
// /distribution.
var routes = []route{
	{http.MethodGet, "/_ping", checkNone},
	{http.MethodGet, "/version", checkNone},
	{http.MethodGet, "/info", checkNone},
	{http.MethodGet, "/events", checkNone},
	{http.MethodGet, "/system/df", checkNone},

	{http.MethodGet, "/containers/json", checkNone},
	{http.MethodPost, "/containers/create", checkContainerCreate},
	{http.MethodPost, "/containers/prune", checkNone},
	{http.MethodGet, "/containers/{id}/json", checkNone}, // Env redacted on the way back
	{http.MethodGet, "/containers/{id}/logs", checkNone},
	{http.MethodGet, "/containers/{id}/stats", checkNone},
	{http.MethodGet, "/containers/{id}/changes", checkNone},
	{http.MethodGet, "/containers/{id}/top", checkPrivateRead},
	{http.MethodPost, "/containers/{id}/start", checkStart},
	{http.MethodPost, "/containers/{id}/restart", checkStart},
	{http.MethodPost, "/containers/{id}/stop", checkNone},
	{http.MethodPost, "/containers/{id}/kill", checkNone},
	{http.MethodPost, "/containers/{id}/pause", checkNone},
	{http.MethodPost, "/containers/{id}/unpause", checkNone},
	{http.MethodPost, "/containers/{id}/wait", checkNone},
	{http.MethodPost, "/containers/{id}/rename", checkNone},
	{http.MethodPost, "/containers/{id}/resize", checkNone},
	{http.MethodPost, "/containers/{id}/update", checkContainerUpdate},
	{http.MethodPost, "/containers/{id}/exec", checkExecCreate},
	{http.MethodPost, "/containers/{id}/attach", checkTarget},
	{http.MethodGet, "/containers/{id}/attach/ws", checkTarget},
	{http.MethodPut, "/containers/{id}/archive", checkTarget},
	{http.MethodDelete, "/containers/{id}", checkNone},

	{http.MethodGet, "/exec/{id}/json", checkNone},
	{http.MethodPost, "/exec/{id}/start", checkExecStart},
	{http.MethodPost, "/exec/{id}/resize", checkNone},

	{http.MethodGet, "/images/json", checkNone},
	{http.MethodGet, "/images/search", checkNone},
	{http.MethodPost, "/images/create", checkImagePull},
	{http.MethodPost, "/images/prune", checkNone},
	{http.MethodGet, "/images/{name+}/json", checkNone},
	{http.MethodGet, "/images/{name+}/history", checkNone},
	{http.MethodPost, "/images/{name+}/tag", checkNone},
	{http.MethodDelete, "/images/{name+}", checkNone},

	{http.MethodGet, "/volumes", checkNone},
	{http.MethodPost, "/volumes", checkVolumeCreate}, // what the Eight.ly OS dashboard posts to
	{http.MethodPost, "/volumes/create", checkVolumeCreate},
	{http.MethodPost, "/volumes/prune", checkNone},
	{http.MethodGet, "/volumes/{id}", checkNone},
	{http.MethodDelete, "/volumes/{id}", checkNone},

	{http.MethodGet, "/networks", checkNone},
	{http.MethodPost, "/networks", checkNetworkCreate}, // what the Eight.ly OS dashboard posts to
	{http.MethodPost, "/networks/create", checkNetworkCreate},
	{http.MethodPost, "/networks/prune", checkNone},
	{http.MethodGet, "/networks/{id}", checkNone},
	{http.MethodPost, "/networks/{id}/connect", checkNetworkConnect},
	{http.MethodPost, "/networks/{id}/disconnect", checkNone},
	{http.MethodDelete, "/networks/{id}", checkNone},
}

// dockerName is Docker's own charset for container, volume and network names
// and for IDs. A Binds source that does not match it is a host path.
var dockerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// matchRoute finds the allowed route for method and p. id is the {id}
// segment when the pattern has one.
func matchRoute(method, p string) (rt route, id string, ok bool) {
	if method == http.MethodHead {
		method = http.MethodGet
	}
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for _, r := range routes {
		if r.method != method {
			continue
		}
		if id, ok := matchPattern(strings.Split(strings.TrimPrefix(r.pattern, "/"), "/"), segs); ok {
			return r, id, true
		}
	}
	return route{}, "", false
}

func matchPattern(pat, segs []string) (string, bool) {
	var id string
	for i, ps := range pat {
		if ps == "{name+}" {
			// One or more segments, then exactly the rest of the pattern.
			rest := pat[i+1:]
			n := len(segs) - i - len(rest)
			if n < 1 {
				return "", false
			}
			for _, s := range segs[i : i+n] {
				if s == "" {
					return "", false
				}
			}
			id, ok := matchPattern(rest, segs[i+n:])
			return id, ok
		}
		if i >= len(segs) {
			return "", false
		}
		switch ps {
		case "{id}":
			if !dockerName.MatchString(segs[i]) {
				return "", false
			}
			id = segs[i]
		default:
			if ps != segs[i] {
				return "", false
			}
		}
	}
	return id, len(segs) == len(pat)
}
