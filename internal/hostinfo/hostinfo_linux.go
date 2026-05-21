package hostinfo

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func readPCIDevices() []PCIDevice {
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

func readDisks() []PhysicalDisk {
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
		disks = append(disks, PhysicalDisk{
			Name:   name,
			SizeGB: sectors * 512 / (1024 * 1024 * 1024),
			Vendor: readSysfs(filepath.Join(base, "device", "vendor")),
		})
	}
	return disks
}

func readSysfs(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
