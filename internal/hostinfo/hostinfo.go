package hostinfo

import (
	"encoding/json"
	"net/http"
)

type HostInfo struct {
	PCIDevices    []PCIDevice    `json:"pci_devices"`
	PhysicalDisks []PhysicalDisk `json:"physical_disks"`
}

type PCIDevice struct {
	Address string `json:"address"`
	Vendor  string `json:"vendor"`
	Device  string `json:"device"`
	Class   string `json:"class"`
}

type PhysicalDisk struct {
	Name   string `json:"name"`
	SizeGB int64  `json:"size_gb"`
	Vendor string `json:"vendor"`
}

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
