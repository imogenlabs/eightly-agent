// SPDX-License-Identifier: Apache-2.0

package hostinfo

import (
	"encoding/json"
	"net/http"
)

// HostInfo aggregates PCI device and physical disk data collected from the
// host. It is the internal collection model — see MinimalHostInfo for the
// fields actually exposed on the wire.
type HostInfo struct {
	PCIDevices    []PCIDevice    `json:"pci_devices"`
	PhysicalDisks []PhysicalDisk `json:"physical_disks"`
}

// PCIDevice represents a single PCI device discovered on the host.
type PCIDevice struct {
	Address string `json:"address"`
	Vendor  string `json:"vendor"`
	Device  string `json:"device"`
	Class   string `json:"class"`
}

// PhysicalDisk represents a block device discovered on the host.
type PhysicalDisk struct {
	Name   string `json:"name"`
	SizeGB int64  `json:"size_gb"`
	Vendor string `json:"vendor"`
}

// MinimalHostInfo is the minimized response shape exposed on the wire. It
// deliberately drops full PCI vendor/device enumeration and per-disk
// vendor/model strings — those overshare host hardware fingerprints — and
// keeps only aggregate counts and totals.
type MinimalHostInfo struct {
	PCICount    int   `json:"pci_count"`
	DiskCount   int   `json:"disk_count"`
	DiskTotalGB int64 `json:"disk_total_gb"`
}

// minimize collapses the full collection into the safe summary shape.
func minimize(info HostInfo) MinimalHostInfo {
	var total int64
	for _, d := range info.PhysicalDisks {
		total += d.SizeGB
	}
	return MinimalHostInfo{
		PCICount:    len(info.PCIDevices),
		DiskCount:   len(info.PhysicalDisks),
		DiskTotalGB: total,
	}
}

// Handler returns an HTTP handler that responds with minimal JSON-encoded host
// hardware summary info — counts and totals only, never full enumeration.
func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info := HostInfo{
			PCIDevices:    readPCIDevices(),
			PhysicalDisks: readDisks(),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(minimize(info)) // #nosec G104 -- HTTP response write
	}
}
