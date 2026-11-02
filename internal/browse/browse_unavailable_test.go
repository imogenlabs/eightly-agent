package browse

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// A host where the agent cannot see Docker's volumes says so, instead of
// answering 404 for every volume as if each one were missing.
func TestHandler_SaysWhyTheBrowserIsUnavailable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-bound")
	for _, tc := range []struct {
		name, root string
		status     int
		want       string
	}{
		{"Docker Desktop: no volumes folder", "", http.StatusNotImplemented, "virtual machine"},
		{"volumes folder not bound", missing, http.StatusServiceUnavailable, missing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(Handler(tc.root))
			defer srv.Close()
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/browse/ls?volumeID=myvol&path=/", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body := new(bytes.Buffer)
			_, _ = body.ReadFrom(resp.Body)
			if resp.StatusCode != tc.status || !strings.Contains(body.String(), tc.want) {
				t.Errorf("got %d %s, want %d mentioning %q", resp.StatusCode, body, tc.status, tc.want)
			}
		})
	}
}
