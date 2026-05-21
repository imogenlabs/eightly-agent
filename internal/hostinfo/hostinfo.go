// SPDX-License-Identifier: Apache-2.0

package hostinfo

import (
	"encoding/json"
	"net/http"
)

// HostInfo aggregates PCI device and physical disk data from the host.
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

// Handler returns an HTTP handler that responds with JSON-encoded host hardware info.
func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info := HostInfo{
			PCIDevices:    readPCIDevices(),
			PhysicalDisks: readDisks(),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(info)
	}
}
