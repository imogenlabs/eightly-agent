package browse

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type fileEntry struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Dir     bool      `json:"dir"`
	ModTime time.Time `json:"modTime"`
}

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
		http.ServeFile(w, r, full)
	})

	mux.HandleFunc("POST /browse/put", func(w http.ResponseWriter, r *http.Request) {
		full, ok := resolvePath(w, dataPath, r.URL.Query().Get("volumeID"), r.URL.Query().Get("path"))
		if !ok {
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file field"})
			return
		}
		defer file.Close()

		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		dst, err := os.Create(full)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer dst.Close()

		buf := make([]byte, 32*1024*1024)
		if _, err := io.CopyBuffer(dst, file, buf); err != nil {
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
		full, ok := resolvePath(w, dataPath, volumeID, body.Path)
		if !ok {
			return
		}
		dest := filepath.Join(filepath.Dir(full), body.NewName)
		if strings.Contains(body.NewName, "..") || strings.Contains(body.NewName, "/") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid new name"})
			return
		}
		if err := os.Rename(full, dest); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	return mux
}

func resolvePath(w http.ResponseWriter, dataPath, volumeID, userPath string) (string, bool) {
	if strings.Contains(volumeID, "..") || strings.Contains(userPath, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path traversal rejected"})
		return "", false
	}
	full := filepath.Join(dataPath, "volumes", volumeID, "_data", userPath)
	return full, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
