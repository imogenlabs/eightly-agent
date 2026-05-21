// SPDX-License-Identifier: Apache-2.0

package hostinfo

import (
	"os/exec"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
)

func readPCIDevices() []PCIDevice {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-PnpDevice -PresentOnly | Where-Object { $_.InstanceId -like 'PCI*' } | Select-Object InstanceId,FriendlyName,Manufacturer,Class | ConvertTo-Csv -NoTypeInformation",
	).Output()
	if err != nil {
		log.Debug().Err(err).Msg("powershell Get-PnpDevice failed")
		return []PCIDevice{}
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return []PCIDevice{}
	}

	devs := make([]PCIDevice, 0, len(lines)-1)
	for _, line := range lines[1:] {
		fields := parseCSVLine(line)
		if len(fields) < 4 {
			continue
		}
		devs = append(devs, PCIDevice{
			Address: fields[0],
			Device:  fields[1],
			Vendor:  fields[2],
			Class:   fields[3],
		})
	}
	return devs
}

func readDisks() []PhysicalDisk {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-PhysicalDisk | Select-Object DeviceId,FriendlyName,Size,Manufacturer | ConvertTo-Csv -NoTypeInformation",
	).Output()
	if err != nil {
		log.Debug().Err(err).Msg("powershell Get-PhysicalDisk failed")
		return []PhysicalDisk{}
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return []PhysicalDisk{}
	}

	disks := make([]PhysicalDisk, 0, len(lines)-1)
	for _, line := range lines[1:] {
		fields := parseCSVLine(line)
		if len(fields) < 4 {
			continue
		}
		sizeBytes, _ := strconv.ParseInt(fields[2], 10, 64)
		disks = append(disks, PhysicalDisk{
			Name:   fields[1],
			SizeGB: sizeBytes / (1024 * 1024 * 1024),
			Vendor: strings.TrimSpace(fields[3]),
		})
	}
	return disks
}

func parseCSVLine(line string) []string {
	var fields []string
	var current strings.Builder
	inQuotes := false

	for _, r := range line {
		switch {
		case r == '"':
			inQuotes = !inQuotes
		case r == ',' && !inQuotes:
			fields = append(fields, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	fields = append(fields, strings.TrimSpace(current.String()))
	return fields
}
