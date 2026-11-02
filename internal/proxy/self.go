// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"strings"
)

// Option configures the proxy and the target checker.
type Option func(*filter)

// ProtectPaths names host files the agent keeps its secrets and state in:
// the state file, the TLS key and certificate, a secret file. No bind, and no
// local volume bound to a host path, may reach them, whatever the bind roots
// say. They are host paths only where the agent runs on the host itself, so
// they are ignored when it runs in a container; there its own mounts are
// what is protected (see selfInfo).
func ProtectPaths(paths ...string) Option {
	return func(f *filter) {
		for _, p := range paths {
			if p == "" {
				continue
			}
			if abs, err := filepath.Abs(p); err == nil && strings.HasPrefix(abs, "/") {
				f.protectedPaths = append(f.protectedPaths, path.Clean(abs))
			}
		}
	}
}

// knownAgentRepos are the repositories the agent's image is published to.
// Tagging into them is refused even when the agent runs on the host, where
// there is no container to read its image from.
var knownAgentRepos = []string{
	"ghcr.io/imogenlabs/eightly-agent",
	"docker.io/smashingtags/eightly-agent",
}

// selfInfo is what the agent protects of its own: the named volumes and host
// paths mounted into its container, and the repositories its image comes
// from.
type selfInfo struct {
	volumes map[string]struct{}
	paths   []string
	repos   []string
}

// selfInfo reads the agent's own container once and keeps the answer: a
// container's mounts and image are fixed when it is created, so they cannot
// change while the agent runs in it. Until the read succeeds every check that
// needs it refuses.
func (f *filter) selfInfo(ctx context.Context) (*selfInfo, string) {
	f.selfMu.Lock()
	defer f.selfMu.Unlock()
	if f.self != nil {
		return f.self, ""
	}
	s := &selfInfo{volumes: map[string]struct{}{}, repos: append([]string(nil), knownAgentRepos...)}
	if f.hostFS {
		s.paths = append(s.paths, f.protectedPaths...)
	}
	if id := f.selfLookupID(); id != "" {
		c, err := f.daemon.container(ctx, id)
		switch {
		case errors.Is(err, errNotFound):
			// Not a container the daemon knows (a hostname that only looks
			// like an ID): nothing more to protect.
		case err != nil:
			return nil, "the agent's own container could not be checked: " + err.Error()
		default:
			for _, m := range c.Mounts {
				switch strings.ToLower(m.Type) {
				case "volume":
					if m.Name != "" {
						s.volumes[m.Name] = struct{}{}
					}
				case "bind":
					if base := path.Base(m.Source); base != "docker.sock" && strings.HasPrefix(m.Source, "/") {
						s.paths = append(s.paths, path.Clean(m.Source))
					}
				}
			}
			if r := normalizeRepo(c.Config.Image); r != "" {
				s.repos = append(s.repos, r)
			}
		}
	}
	f.self = s
	return s, ""
}

// selfLookupID is the ID to inspect the agent's own container by: a full ID
// when one was found, otherwise the hostname's short one.
func (f *filter) selfLookupID() string {
	id := ""
	for _, s := range f.selfIDs {
		if len(s) > len(id) {
			id = s
		}
	}
	return id
}

// overlapsProtected reports whether a host path is, holds, or lies inside a
// path the agent protects.
func (s *selfInfo) overlapsProtected(p string) string {
	for _, q := range s.paths {
		if p == q || strings.HasPrefix(q, p+"/") || strings.HasPrefix(p, q+"/") || p == "/" {
			return q
		}
	}
	return ""
}

// normalizeRepo turns an image reference into its full repository name, as
// the daemon reads it: lower case, no tag or digest, docker.io for an image
// with no registry, and library/ for an official image. It returns "" for an
// image ID.
func normalizeRepo(ref string) string {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if ref == "" || strings.HasPrefix(ref, "sha256:") {
		return ""
	}
	ref, _, _ = strings.Cut(ref, "@")
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	domain, rest, found := strings.Cut(ref, "/")
	if !found || (!strings.ContainsAny(domain, ".:") && domain != "localhost") {
		domain, rest = "docker.io", ref
	}
	if domain == "index.docker.io" {
		domain = "docker.io"
	}
	if domain == "docker.io" && !strings.Contains(rest, "/") {
		rest = "library/" + rest
	}
	return domain + "/" + rest
}
