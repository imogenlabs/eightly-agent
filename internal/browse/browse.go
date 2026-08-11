// SPDX-License-Identifier: Apache-2.0

package browse

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

var validIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type fileEntry struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Dir     bool      `json:"dir"`
	ModTime time.Time `json:"modTime"`
}

// options holds Handler configuration set via functional options.
type options struct {
	readOnly bool
}

// Option configures the browse Handler.
type Option func(*options)

// ReadOnly toggles read-only mode. When enabled, all mutating verbs
// (put, delete, rename) are rejected with 403 and the rejection is audited.
func ReadOnly(ro bool) Option {
	return func(o *options) { o.readOnly = ro }
}

// Handler returns an HTTP handler for listing, downloading, uploading, deleting, and renaming files in Docker volumes.
//
// The variadic opts keep the existing call site (browse.Handler(cfg.DataPath))
// compiling unchanged while allowing ReadOnly(true) and future options.
func Handler(dataPath string, opts ...Option) http.Handler {
	var cfg options
	for _, opt := range opts {
		opt(&cfg)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /browse/ls", func(w http.ResponseWriter, r *http.Request) {
		full, ok := resolvePath(w, dataPath, r.URL.Query().Get("volumeID"), r.URL.Query().Get("path"))
		if !ok {
			return
		}
		entries, err := os.ReadDir(full)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read failed"})
			return
		}
		result := make([]fileEntry, 0, len(entries))
		for _, e := range entries {
			// Skip dotfiles — don't leak hidden files in directory listings.
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			result = append(result, fileEntry{
				Name:    e.Name(),
				Size:    info.Size(),
				Dir:     e.IsDir(),
				ModTime: info.ModTime(),
			})
		}
		writeJSON(w, http.StatusOK, result)
	})

	mux.HandleFunc("GET /browse/get", func(w http.ResponseWriter, r *http.Request) {
		full, ok := resolvePath(w, dataPath, r.URL.Query().Get("volumeID"), r.URL.Query().Get("path"))
		if !ok {
			return
		}
		// Refuse dotfiles on download.
		if strings.HasPrefix(filepath.Base(full), ".") {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		// Open with O_NOFOLLOW so a symlink at the final component cannot redirect
		// the read outside the volume, then serve via ServeContent.
		f, err := os.OpenFile(full, os.O_RDONLY|syscallNoFollow, 0) // #nosec G304 -- path validated against base directory; O_NOFOLLOW prevents symlink redirection
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "stat failed"})
			return
		}
		if info.IsDir() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target is a directory"})
			return
		}
		http.ServeContent(w, r, filepath.Base(full), info.ModTime(), f)
	})

	mux.HandleFunc("POST /browse/put", func(w http.ResponseWriter, r *http.Request) {
		if cfg.readOnly {
			rejectReadOnly(w, r, "put")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 100*1024*1024) // 100 MB limit

		full, ok := resolvePath(w, dataPath, r.URL.Query().Get("volumeID"), r.URL.Query().Get("path"))
		if !ok {
			return
		}

		// Reject if target is an existing directory
		if info, err := os.Stat(full); err == nil && info.IsDir() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target is a directory"})
			return
		}

		file, _, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file field"})
			return
		}
		defer file.Close()

		mkErr := os.MkdirAll(filepath.Dir(full), 0755) // #nosec G301 -- volume data directories need 0755 for Docker container access
		if mkErr != nil {
			audit(r, "put", full, "error")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
			return
		}

		// Re-validate after MkdirAll: directory creation may have followed a symlink
		// that now resolves outside the volume.
		if _, ok := containedPath(dataPath, r.URL.Query().Get("volumeID"), r.URL.Query().Get("path")); !ok {
			audit(r, "put", full, "rejected")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path traversal rejected"})
			return
		}

		// O_NOFOLLOW so a symlink planted at the final path component cannot redirect the write.
		dst, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscallNoFollow, 0644) // #nosec G302 G304 -- path validated against base directory; O_NOFOLLOW prevents symlink redirection
		if err != nil {
			audit(r, "put", full, "error")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
			return
		}
		defer dst.Close()

		if _, err := io.Copy(dst, file); err != nil {
			audit(r, "put", full, "error")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
			return
		}
		audit(r, "put", full, "ok")
		writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("DELETE /browse/delete", func(w http.ResponseWriter, r *http.Request) {
		if cfg.readOnly {
			rejectReadOnly(w, r, "delete")
			return
		}
		full, ok := resolvePath(w, dataPath, r.URL.Query().Get("volumeID"), r.URL.Query().Get("path"))
		if !ok {
			return
		}
		if err := os.RemoveAll(full); err != nil {
			audit(r, "delete", full, "error")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
			return
		}
		audit(r, "delete", full, "ok")
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("PUT /browse/rename", func(w http.ResponseWriter, r *http.Request) {
		if cfg.readOnly {
			rejectReadOnly(w, r, "rename")
			return
		}
		volumeID := r.URL.Query().Get("volumeID")
		var body struct {
			Path    string `json:"path"`
			NewName string `json:"newName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json body"})
			return
		}
		if !validIdentifier.MatchString(body.NewName) || strings.Contains(body.NewName, "..") || strings.Contains(body.NewName, "/") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid new name"})
			return
		}

		full, ok := resolvePath(w, dataPath, volumeID, body.Path)
		if !ok {
			return
		}
		dest := filepath.Join(filepath.Dir(full), body.NewName)

		// Re-validate the DESTINATION through the same symlink-aware containment check.
		destRel := filepath.Join(filepath.Dir(body.Path), body.NewName)
		if _, ok := containedPath(dataPath, volumeID, destRel); !ok {
			audit(r, "rename", dest, "rejected")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path traversal rejected"})
			return
		}

		if err := os.Rename(full, dest); err != nil {
			audit(r, "rename", full, "error")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "rename failed"})
			return
		}
		audit(r, "rename", dest, "ok")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	return mux
}

// resolvePath validates volumeID + userPath and returns the absolute on-disk
// path, writing an error response and returning ok=false when validation fails.
func resolvePath(w http.ResponseWriter, dataPath, volumeID, userPath string) (string, bool) {
	full, ok := containedPath(dataPath, volumeID, userPath)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path traversal rejected"})
		return "", false
	}
	return full, true
}

// containedPath resolves the target path and verifies — unconditionally, for
// both reads and writes — that it stays beneath the symlink-resolved volume
// root. It tolerates non-existent leaf/suffix components (needed for put,
// rename destinations, and fresh files) by resolving the DEEPEST EXISTING
// ancestor of the target and recomposing the non-existent suffix on top.
//
// Any EvalSymlinks error other than "does not exist" is treated as NOT safe.
func containedPath(dataPath, volumeID, userPath string) (string, bool) {
	if !validIdentifier.MatchString(volumeID) {
		return "", false
	}

	base := filepath.Join(dataPath, "volumes", volumeID, "_data")
	full := filepath.Clean(filepath.Join(base, userPath))

	// Lexical containment first (cheap, catches ../ before touching the FS).
	rel, err := filepath.Rel(base, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}

	// Resolve the real volume root. The root must exist and resolve cleanly.
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", false
	}

	// Resolve the deepest existing ancestor of the target, then recompose the
	// non-existent suffix on top of the resolved ancestor.
	resolvedFull, ok := resolveExisting(full)
	if !ok {
		return "", false
	}

	// Unconditional containment check against the resolved root.
	relResolved, err := filepath.Rel(resolvedBase, resolvedFull)
	if err != nil || relResolved == ".." || strings.HasPrefix(relResolved, ".."+string(filepath.Separator)) || filepath.IsAbs(relResolved) {
		return "", false
	}

	return full, true
}

// resolveExisting returns the symlink-resolved absolute path for target,
// tolerating a non-existent leaf/suffix by walking up to the deepest existing
// ancestor, resolving that, and rejoining the missing components. It returns
// ok=false on any non-ENOENT error (e.g. permission, ELOOP) — never proceed
// when the filesystem can't be trusted to have been fully resolved.
func resolveExisting(target string) (string, bool) {
	target = filepath.Clean(target)
	var suffix []string
	cur := target
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			// Rejoin the non-existent suffix (innermost component last).
			parts := append([]string{resolved}, reverse(suffix)...)
			return filepath.Join(parts...), true
		}
		if !os.IsNotExist(err) {
			// Non-ENOENT (permission denied, too many links, etc.) => not safe.
			return "", false
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Reached the root and still nothing resolved — cannot trust it.
			return "", false
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}

func reverse(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}

// rejectReadOnly responds 403 to a mutating request in read-only mode and audits it.
func rejectReadOnly(w http.ResponseWriter, r *http.Request, verb string) {
	audit(r, verb, r.URL.Query().Get("path"), "rejected_readonly")
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "read-only mode"})
}

// audit emits a structured record for a mutating operation. The actor is a
// best-effort read of the verified bearer token's `sub` claim.
func audit(r *http.Request, verb, path, result string) {
	log.Info().
		Str("event", "browse.mutate").
		Str("actor", actorFromRequest(r)).
		Str("verb", verb).
		Str("path", path).
		Str("result", result).
		Str("remote", r.RemoteAddr).
		Msg("browse mutate")
}

// actorFromRequest extracts the `sub` claim from the Bearer token best-effort.
// The auth middleware has already verified the signature upstream; here we only
// decode the payload for attribution and never fail the request on a bad token.
func actorFromRequest(r *http.Request) string {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" || token == r.Header.Get("Authorization") {
		return "anonymous"
	}
	parts := strings.SplitN(token, ".", 2)
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "unknown"
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Sub == "" {
		return "unknown"
	}
	return claims.Sub
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // #nosec G104 -- response already committed; nothing to report to
}
