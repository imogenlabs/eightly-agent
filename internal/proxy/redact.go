// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
)

const redacted = "<redacted>"

// secretFlags are the agent's own flags whose value is a credential.
var secretFlags = map[string]struct{}{
	"-secret": {}, "--secret": {}, "-enroll-token": {}, "--enroll-token": {},
}

// redactInspect rewrites a container inspect response on its way back. The
// agent's own container carries EIGHTLY_AGENT_SECRET, the key every token for
// this agent is signed with, and any container holding the Docker socket is
// as good as the host. For those every environment value is removed; for all
// others the EIGHTLY_* values are. Values of the agent's secret flags are
// removed from the command line everywhere. A response that cannot be read
// is not passed on.
func (f *filter) redactInspect(resp *http.Response) error {
	req := resp.Request
	if req == nil || req.Method != http.MethodGet || resp.StatusCode != http.StatusOK {
		return nil
	}
	if rt, _, ok := matchRoute(http.MethodGet, dockerPath(req.URL.Path)); !ok || rt.pattern != "/containers/{id}/json" {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLookupBody+1))
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	if len(body) > maxLookupBody {
		return errors.New("container inspect response too large to redact")
	}
	out, err := redactContainerJSON(body, f.isSelf)
	if err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	return nil
}

func redactContainerJSON(body []byte, isSelf func(string) bool) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var c map[string]any
	if err := dec.Decode(&c); err != nil {
		return nil, err
	}
	id, _ := c["Id"].(string)
	all := isSelf(id)
	if mounts, ok := c["Mounts"].([]any); ok {
		for _, m := range mounts {
			mp, _ := m.(map[string]any)
			src, _ := mp["Source"].(string)
			dst, _ := mp["Destination"].(string)
			if path.Base(src) == "docker.sock" || path.Base(dst) == "docker.sock" {
				all = true
			}
		}
	}
	redactArgs(c["Args"])
	if cfg, ok := c["Config"].(map[string]any); ok {
		redactArgs(cfg["Cmd"])
		redactArgs(cfg["Entrypoint"])
		if env, ok := cfg["Env"].([]any); ok {
			for i, e := range env {
				s, _ := e.(string)
				k, _, _ := strings.Cut(s, "=")
				if all || strings.HasPrefix(strings.ToUpper(k), "EIGHTLY_") {
					env[i] = k + "=" + redacted
				}
			}
		}
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// redactArgs removes the value of a secret flag, given as "--secret value" or
// "--secret=value".
func redactArgs(v any) {
	args, ok := v.([]any)
	if !ok {
		return
	}
	for i := range args {
		s, _ := args[i].(string)
		if name, _, found := strings.Cut(s, "="); found {
			if _, secret := secretFlags[name]; secret {
				args[i] = name + "=" + redacted
			}
			continue
		}
		if _, secret := secretFlags[s]; secret && i+1 < len(args) {
			args[i+1] = redacted
		}
	}
}
