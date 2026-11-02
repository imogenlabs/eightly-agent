// SPDX-License-Identifier: Apache-2.0

package hostinfo

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

const (
	// commandTimeout bounds each reader (PCI, disks). On macOS and Windows
	// they run system_profiler, diskutil or PowerShell, which can hang.
	commandTimeout = 15 * time.Second
	// waitDelay is how long to wait for a killed command's children to let
	// go of its output pipe (system_profiler forks helpers).
	waitDelay = 2 * time.Second
	// cacheTTL reuses one collection across requests, so a dashboard polling
	// /host does not start a new set of host commands every time.
	cacheTTL = 30 * time.Second
)

var (
	cacheMu  sync.Mutex
	cached   MinimalHostInfo
	cachedAt time.Time
	// collect gathers the host data; a variable so tests can count calls.
	collect = collectHost
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

// collectHost reads PCI devices and disks, each under its own timeout. It
// does not use the request's context: a client hanging up must not leave an
// empty result in the cache.
func collectHost() HostInfo {
	pciCtx, cancelPCI := context.WithTimeout(context.Background(), commandTimeout)
	defer cancelPCI()
	diskCtx, cancelDisks := context.WithTimeout(context.Background(), commandTimeout)
	defer cancelDisks()
	return HostInfo{
		PCIDevices:    readPCIDevices(pciCtx),
		PhysicalDisks: readDisks(diskCtx),
	}
}

// summary returns the cached summary, collecting it when older than cacheTTL.
// The lock is held while collecting, so concurrent requests wait for one
// collection instead of each starting its own commands.
func summary() MinimalHostInfo {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if !cachedAt.IsZero() && time.Since(cachedAt) < cacheTTL {
		return cached
	}
	cached = minimize(collect())
	cachedAt = time.Now()
	return cached
}

// runCommand runs a host command under ctx and returns its stdout. When ctx
// ends the command is killed, and Output returns at most waitDelay later even
// if a child process still holds the pipe.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- fixed commands; arguments come from the host's own output, not request input
	cmd.WaitDelay = waitDelay
	return cmd.Output()
}

// Handler returns an HTTP handler that responds with minimal JSON-encoded host
// hardware summary info — counts and totals only, never full enumeration.
func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(summary()) // #nosec G104 -- HTTP response write
	}
}
