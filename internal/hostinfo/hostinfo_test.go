package hostinfo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerReturnsValidJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/hostinfo", nil)
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var raw json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}

func TestHandlerDeserializesIntoHostInfo(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/hostinfo", nil)
	Handler().ServeHTTP(rec, req)

	var info HostInfo
	if err := json.NewDecoder(rec.Body).Decode(&info); err != nil {
		t.Fatalf("failed to decode into HostInfo: %v", err)
	}
}

func TestHandlerContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/hostinfo", nil)
	Handler().ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

func TestHandlerArraysNonNil(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/hostinfo", nil)
	Handler().ServeHTTP(rec, req)

	var info HostInfo
	if err := json.NewDecoder(rec.Body).Decode(&info); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if info.PCIDevices == nil {
		t.Error("PCIDevices should not be nil")
	}
	if info.PhysicalDisks == nil {
		t.Error("PhysicalDisks should not be nil")
	}
}
