package hostinfo

// Windows host info not yet implemented — returns empty results.
// TODO: Use WMI queries (Win32_PnPEntity, Win32_DiskDrive) to populate.

func readPCIDevices() []PCIDevice {
	return []PCIDevice{}
}

func readDisks() []PhysicalDisk {
	return []PhysicalDisk{}
}
