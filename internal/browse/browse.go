// SPDX-License-Identifier: Apache-2.0

package browse

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var validIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type fileEntry struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Dir     bool      `json:"dir"`
	ModTime time.Time `json:"modTime"`
}

// Handler returns an HTTP handler for listing, downloading, uploading, deleting, and renaming files in Docker volumes.
func Handler(dataPath string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /browse/ls", func(w http.ResponseWriter, r *http.Request) {
		full, ok := resolvePath(w, dataPath, r.URL.Query().Get("volumeID"), r.URL.Query().Get("path"))
		if !ok {
			return
		}
		entries, err := os.ReadDir(full)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		result := make([]fileEntry, 0, len(entries))
		for _, e := range entries {
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
		http.ServeFile(w, r, full) // #nosec G304 -- path is validated against base directory with filepath.Rel containment check
	})

	mux.HandleFunc("POST /browse/put", func(w http.ResponseWriter, r *http.Request) {
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

		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil { // #nosec G301 -- volume data directories need 0755 for Docker container access
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		dst, err := os.Create(full) // #nosec G304 -- path validated with filepath.Rel containment check
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer dst.Close()

		if _, err := io.Copy(dst, file); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("DELETE /browse/delete", func(w http.ResponseWriter, r *http.Request) {
		full, ok := resolvePath(w, dataPath, r.URL.Query().Get("volumeID"), r.URL.Query().Get("path"))
		if !ok {
			return
		}
		if err := os.RemoveAll(full); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("PUT /browse/rename", func(w http.ResponseWriter, r *http.Request) {
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
		if err := os.Rename(full, dest); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	return mux
}

func resolvePath(w http.ResponseWriter, dataPath, volumeID, userPath string) (string, bool) {
	if !validIdentifier.MatchString(volumeID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid volume ID"})
		return "", false
	}

	base := filepath.Join(dataPath, "volumes", volumeID, "_data")
	full := filepath.Clean(filepath.Join(base, userPath))

	rel, err := filepath.Rel(base, full)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path traversal rejected"})
		return "", false
	}

	// Resolve symlinks and re-check containment
	if resolved, err := filepath.EvalSymlinks(full); err == nil {
		cleanBase := filepath.Clean(base)
		if resolvedBase, err := filepath.EvalSymlinks(cleanBase); err == nil {
			cleanBase = resolvedBase
		}
		relResolved, err := filepath.Rel(cleanBase, resolved)
		if err != nil || strings.HasPrefix(relResolved, "..") || filepath.IsAbs(relResolved) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path traversal rejected"})
			return "", false
		}
	}

	return full, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) // #nosec G104 -- HTTP response write
}
