// SPDX-License-Identifier: Apache-2.0

package browse

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path"
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
		root, name, ok := openPath(w, dataPath, r.URL.Query().Get("volumeID"), r.URL.Query().Get("path"))
		if !ok {
			return
		}
		defer root.Close()
		dir, err := root.Open(name)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read failed"})
			return
		}
		defer dir.Close()
		entries, err := dir.ReadDir(-1)
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
		root, name, ok := openPath(w, dataPath, r.URL.Query().Get("volumeID"), r.URL.Query().Get("path"))
		if !ok {
			return
		}
		defer root.Close()
		// Refuse dotfiles on download.
		if strings.HasPrefix(filepath.Base(name), ".") {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		// The Root keeps any symlink inside the volume; a symlink at the final
		// component is refused as well, as it always has been here.
		if isSymlink(root, name) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		f, err := root.OpenFile(name, os.O_RDONLY|syscallNoFollow, 0)
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
		http.ServeContent(w, r, filepath.Base(name), info.ModTime(), f)
	})

	mux.HandleFunc("POST /browse/put", func(w http.ResponseWriter, r *http.Request) {
		if cfg.readOnly {
			rejectReadOnly(w, r, "put")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 100*1024*1024) // 100 MB limit

		root, name, ok := openPath(w, dataPath, r.URL.Query().Get("volumeID"), r.URL.Query().Get("path"))
		if !ok {
			return
		}
		defer root.Close()

		// Reject if target is an existing directory (the volume root included).
		if info, err := root.Stat(name); err == nil && info.IsDir() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target is a directory"})
			return
		}

		file, _, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file field"})
			return
		}
		defer file.Close()

		if err := root.MkdirAll(filepath.Dir(name), 0755); err != nil { // #nosec G301 -- volume data directories need 0755 for Docker container access
			audit(r, "put", name, "error")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
			return
		}

		if isSymlink(root, name) {
			audit(r, "put", name, "rejected")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target is a symlink"})
			return
		}
		dst, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscallNoFollow, 0644) // #nosec G302 -- files in a volume are world-readable like the rest of its data
		if err != nil {
			audit(r, "put", name, "error")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
			return
		}
		defer dst.Close()

		if _, err := io.Copy(dst, file); err != nil {
			audit(r, "put", name, "error")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
			return
		}
		audit(r, "put", name, "ok")
		writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("DELETE /browse/delete", func(w http.ResponseWriter, r *http.Request) {
		if cfg.readOnly {
			rejectReadOnly(w, r, "delete")
			return
		}
		root, name, ok := openPath(w, dataPath, r.URL.Query().Get("volumeID"), r.URL.Query().Get("path"))
		if !ok {
			return
		}
		defer root.Close()
		if name == "." {
			audit(r, "delete", name, "rejected")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot delete the volume root"})
			return
		}
		if err := root.RemoveAll(name); err != nil {
			audit(r, "delete", name, "error")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
			return
		}
		audit(r, "delete", name, "ok")
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("PUT /browse/rename", func(w http.ResponseWriter, r *http.Request) {
		if cfg.readOnly {
			rejectReadOnly(w, r, "rename")
			return
		}
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

		root, name, ok := openPath(w, dataPath, r.URL.Query().Get("volumeID"), body.Path)
		if !ok {
			return
		}
		defer root.Close()
		if name == "." {
			audit(r, "rename", name, "rejected")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot rename the volume root"})
			return
		}
		dest := filepath.Join(filepath.Dir(name), body.NewName)

		if err := root.Rename(name, dest); err != nil {
			audit(r, "rename", name, "error")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "rename failed"})
			return
		}
		audit(r, "rename", dest, "ok")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	return mux
}

// openPath is the one way a request reaches the filesystem. It opens the
// volume's data directory as an os.Root and turns userPath into a name
// relative to it. Every operation then goes through the Root, which resolves
// each component with openat and refuses any symlink or ".." that leads out of
// the volume at the moment of the operation, so there is no window between a
// check and the use for a swapped-in symlink to slip through. It writes the
// error response itself and returns ok=false when the request is refused.
//
// The caller must Close the returned Root.
func openPath(w http.ResponseWriter, dataPath, volumeID, userPath string) (*os.Root, string, bool) {
	name, ok := volumeName(userPath)
	if !ok || !validIdentifier.MatchString(volumeID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path traversal rejected"})
		return nil, "", false
	}
	root, err := os.OpenRoot(filepath.Join(dataPath, "volumes", volumeID, "_data"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "volume not found"})
		return nil, "", false
	}
	return root, name, true
}

// volumeName turns a request path into a clean name relative to the volume
// root: "" and "/" are the root itself ("."), a leading "/" is relative to the
// root, and any ".." segment is refused outright rather than cleaned away.
func volumeName(userPath string) (string, bool) {
	if strings.ContainsRune(userPath, 0) {
		return "", false
	}
	for _, seg := range strings.Split(userPath, "/") {
		if seg == ".." {
			return "", false
		}
	}
	// Cleaning a "/"-rooted path can never climb above "/".
	name := strings.TrimPrefix(path.Clean("/"+userPath), "/")
	if name == "" {
		return ".", true
	}
	if !filepath.IsLocal(name) {
		return "", false
	}
	return filepath.FromSlash(name), true
}

// isSymlink reports whether the final component of name is a symlink. A
// symlink swapped in after this check can only point inside the volume,
// because the Root refuses to follow one that leads out.
func isSymlink(root *os.Root, name string) bool {
	info, err := root.Lstat(name)
	return err == nil && info.Mode()&os.ModeSymlink != 0
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
