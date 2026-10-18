// Package memlimit gives the Go runtime the container's memory limit. Without
// GOMEMLIMIT the runtime assumes unlimited memory (checked against go1.27.1's
// readGOMEMLIMIT on 2026-10-18; only GOMAXPROCS follows the cgroup), so the
// collector lets the heap reach twice the live data and the kernel OOM-kills
// the process with no log line before the collector ever reacts.
package memlimit

import (
	"strconv"
	"strings"
)

const (
	cgroupV2Max = "/sys/fs/cgroup/memory.max"
	cgroupV1Max = "/sys/fs/cgroup/memory/memory.limit_in_bytes"

	// heapSharePercent leaves the rest of the container to goroutine stacks,
	// the runtime's own structures and anything the limit does not count.
	heapSharePercent = 85

	// cgroup v1 reports "no limit" as a page-rounded MaxInt64.
	unlimitedFloor = int64(1) << 60
)

// FromCgroup returns the soft limit to give the runtime, a share of the
// container's memory limit. It reports false when explicit (the GOMEMLIMIT
// environment value) is set, which the runtime has already applied, or when
// the process runs under no memory limit.
func FromCgroup(explicit string, readFile func(string) ([]byte, error)) (int64, bool) {
	if explicit != "" {
		return 0, false
	}
	for _, path := range []string{cgroupV2Max, cgroupV1Max} {
		raw, err := readFile(path)
		if err != nil {
			continue
		}
		limit, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil || limit <= 0 || limit >= unlimitedFloor {
			return 0, false
		}
		return limit / 100 * heapSharePercent, true
	}
	return 0, false
}
