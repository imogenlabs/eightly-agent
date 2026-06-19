// SPDX-License-Identifier: Apache-2.0

package hostinfo

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
)

func readPCIDevices() []PCIDevice {
	out, err := exec.Command("system_profiler", "SPPCIDataType", "-json").Output()
	if err != nil {
		log.Debug().Err(err).Msg("system_profiler SPPCIDataType failed")
		return []PCIDevice{}
	}

	var raw map[string][]map[string]interface{}
	if err := json.Unmarshal(out, &raw); err != nil {
		return []PCIDevice{}
	}

	items, ok := raw["SPPCIDataType"]
	if !ok {
		return []PCIDevice{}
	}

	devs := make([]PCIDevice, 0, len(items))
	for _, item := range items {
		devs = append(devs, PCIDevice{
			Address: strVal(item, "sppci_bus"),
			Vendor:  strVal(item, "sppci_vendor"),
			Device:  strVal(item, "_name"),
			Class:   strVal(item, "sppci_device_type"),
		})
	}
	return devs
}

func readDisks() []PhysicalDisk {
	out, err := exec.Command("diskutil", "list").Output()
	if err != nil {
		log.Debug().Err(err).Msg("diskutil list failed")
		return []PhysicalDisk{}
	}

	var disks []PhysicalDisk
	for _, line := range strings.Split(string(out), "\n") {
		// Lines like: /dev/disk0 (internal, physical):
		if !strings.HasPrefix(line, "/dev/disk") {
			continue
		}
		// Skip synthesized/virtual disks
		if strings.Contains(line, "synthesized") || strings.Contains(line, "virtual") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		name := parts[0]
		name = strings.TrimPrefix(name, "/dev/")

		sizeGB := diskSizeGB(name)
		disks = append(disks, PhysicalDisk{
			Name:   name,
			SizeGB: sizeGB,
			Vendor: diskVendor(name),
		})
	}
	if disks == nil {
		return []PhysicalDisk{}
	}
	return disks
}

func diskSizeGB(name string) int64 {
	out, err := exec.Command("diskutil", "info", name).Output() // #nosec G204 -- name from diskutil output, not request input
	if err != nil {
		log.Debug().Err(err).Str("disk", name).Msg("diskutil info failed (size)")
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Disk Size:") || strings.HasPrefix(line, "Total Size:") {
			// Format: "Disk Size:  500.1 GB (500107862016 Bytes)..."
			// Extract bytes from parentheses
			if start := strings.Index(line, "("); start != -1 {
				rest := line[start+1:]
				if end := strings.Index(rest, " Bytes"); end != -1 {
					bytes, err := strconv.ParseInt(rest[:end], 10, 64)
					if err == nil {
						return bytes / (1024 * 1024 * 1024)
					}
				}
			}
		}
	}
	return 0
}

func diskVendor(name string) string {
	out, err := exec.Command("diskutil", "info", name).Output() // #nosec G204 -- name from diskutil output, not request input
	if err != nil {
		log.Debug().Err(err).Str("disk", name).Msg("diskutil info failed (vendor)")
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Device / Media Name:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Device / Media Name:"))
		}
	}
	return ""
}

func strVal(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
