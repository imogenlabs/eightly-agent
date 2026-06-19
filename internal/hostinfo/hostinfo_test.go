package hostinfo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerReturnsValidJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/host", nil)
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var raw json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}

func TestHandlerDeserializesIntoMinimalHostInfo(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/host", nil)
	Handler().ServeHTTP(rec, req)

	var info MinimalHostInfo
	if err := json.NewDecoder(rec.Body).Decode(&info); err != nil {
		t.Fatalf("failed to decode into MinimalHostInfo: %v", err)
	}
}

func TestHandlerContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/host", nil)
	Handler().ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

// TestHandlerDoesNotLeakEnumeration verifies the wire output exposes only the
// minimal summary fields and never the oversharing full enumeration (PCI
// vendor/device strings, per-disk vendor/model).
func TestHandlerDoesNotLeakEnumeration(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/host", nil)
	Handler().ServeHTTP(rec, req)

	var generic map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&generic); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	leaked := []string{"pci_devices", "physical_disks", "vendor", "device", "address", "class", "name"}
	for _, k := range leaked {
		if _, ok := generic[k]; ok {
			t.Errorf("response must not expose oversharing field %q", k)
		}
	}

	for _, want := range []string{"pci_count", "disk_count", "disk_total_gb"} {
		if _, ok := generic[want]; !ok {
			t.Errorf("response missing expected minimal field %q", want)
		}
	}
}

// TestMinimizeCollapsesCollection checks the count/total math directly.
func TestMinimizeCollapsesCollection(t *testing.T) {
	in := HostInfo{
		PCIDevices: []PCIDevice{
			{Address: "0000:00:00.0", Vendor: "0x8086", Device: "0x1234"},
			{Address: "0000:01:00.0", Vendor: "0x10de", Device: "0x5678"},
		},
		PhysicalDisks: []PhysicalDisk{
			{Name: "sda", SizeGB: 500, Vendor: "Samsung"},
			{Name: "sdb", SizeGB: 1000, Vendor: "WD"},
		},
	}
	got := minimize(in)
	if got.PCICount != 2 {
		t.Errorf("pci_count = %d, want 2", got.PCICount)
	}
	if got.DiskCount != 2 {
		t.Errorf("disk_count = %d, want 2", got.DiskCount)
	}
	if got.DiskTotalGB != 1500 {
		t.Errorf("disk_total_gb = %d, want 1500", got.DiskTotalGB)
	}
}
