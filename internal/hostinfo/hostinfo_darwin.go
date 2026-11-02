// SPDX-License-Identifier: Apache-2.0

package hostinfo

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
)

func readPCIDevices(ctx context.Context) []PCIDevice {
	out, err := runCommand(ctx, "system_profiler", "SPPCIDataType", "-json")
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

func readDisks(ctx context.Context) []PhysicalDisk {
	out, err := runCommand(ctx, "diskutil", "list")
	if err != nil {
		log.Debug().Err(err).Msg("diskutil list failed")
		return []PhysicalDisk{}
	}

	disks := []PhysicalDisk{}
	for _, name := range parseDiskList(string(out)) {
		// One diskutil info per disk gives both size and vendor.
		info, err := runCommand(ctx, "diskutil", "info", name)
		if err != nil {
			log.Debug().Err(err).Str("disk", name).Msg("diskutil info failed")
			disks = append(disks, PhysicalDisk{Name: name})
			continue
		}
		sizeGB, vendor := parseDiskInfo(string(info))
		disks = append(disks, PhysicalDisk{Name: name, SizeGB: sizeGB, Vendor: vendor})
	}
	return disks
}

// parseDiskList returns the physical disk names (disk0, disk1, ...) from
// `diskutil list`, skipping synthesized and virtual disks.
func parseDiskList(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		// Lines like: /dev/disk0 (internal, physical):
		if !strings.HasPrefix(line, "/dev/disk") {
			continue
		}
		if strings.Contains(line, "synthesized") || strings.Contains(line, "virtual") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		names = append(names, strings.TrimPrefix(parts[0], "/dev/"))
	}
	return names
}

// parseDiskInfo reads the size in GiB and the media name from `diskutil info`.
func parseDiskInfo(out string) (sizeGB int64, vendor string) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Disk Size:"), strings.HasPrefix(line, "Total Size:"):
			// Format: "Disk Size:  500.1 GB (500107862016 Bytes)..."
			if sizeGB != 0 {
				continue
			}
			if start := strings.Index(line, "("); start != -1 {
				rest := line[start+1:]
				if end := strings.Index(rest, " Bytes"); end != -1 {
					if bytes, err := strconv.ParseInt(rest[:end], 10, 64); err == nil {
						sizeGB = bytes / (1024 * 1024 * 1024)
					}
				}
			}
		case strings.HasPrefix(line, "Device / Media Name:"):
			vendor = strings.TrimSpace(strings.TrimPrefix(line, "Device / Media Name:"))
		}
	}
	return sizeGB, vendor
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
