// SPDX-License-Identifier: Apache-2.0

package hostinfo

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// readPCIDevices reads sysfs, which never blocks; ctx is unused.
func readPCIDevices(_ context.Context) []PCIDevice {
	entries, err := os.ReadDir("/sys/bus/pci/devices")
	if err != nil {
		return []PCIDevice{}
	}
	devs := make([]PCIDevice, 0, len(entries))
	for _, e := range entries {
		base := filepath.Join("/sys/bus/pci/devices", e.Name())
		devs = append(devs, PCIDevice{
			Address: e.Name(),
			Vendor:  readSysfs(filepath.Join(base, "vendor")),
			Device:  readSysfs(filepath.Join(base, "device")),
			Class:   readSysfs(filepath.Join(base, "class")),
		})
	}
	return devs
}

func readDisks(_ context.Context) []PhysicalDisk {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return []PhysicalDisk{}
	}
	disks := make([]PhysicalDisk, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "dm-") {
			continue
		}
		base := filepath.Join("/sys/block", name)
		sectors, _ := strconv.ParseInt(readSysfs(filepath.Join(base, "size")), 10, 64)
		vendor := readSysfs(filepath.Join(base, "device", "vendor"))
		if vendor == "" {
			vendor = readSysfs(filepath.Join(base, "device", "model"))
		}
		disks = append(disks, PhysicalDisk{
			Name:   name,
			SizeGB: sectors * 512 / (1024 * 1024 * 1024),
			Vendor: vendor,
		})
	}
	return disks
}

func readSysfs(path string) string {
	data, err := os.ReadFile(path) // #nosec G304 -- reads from /sys kernel filesystem, not user-supplied paths
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
