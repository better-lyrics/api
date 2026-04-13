package main

import (
	"fmt"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/notifier"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	memMonitorNormalInterval   = 30 * time.Minute
	memMonitorDetailedInterval = 1 * time.Minute
	memWatchThresholdBytes     = 4 * 1024 * 1024 * 1024  // 4 GB — early warning
	memAlertThresholdBytes     = 20 * 1024 * 1024 * 1024 // 20 GB — detailed mode
)

var (
	memAlertNotified atomic.Bool
	memWatchNotified atomic.Bool
	memProfileDumped atomic.Bool
)

// startMemoryMonitor launches a background goroutine that periodically logs memory stats.
//
// Three modes:
//   - Normal (RSS < 4GB): one-liner every 30 minutes
//   - Watch (RSS >= 4GB): one-time detailed snapshot logged, then continues normal interval
//   - Alert (RSS >= 20GB): detailed breakdown every 1 minute, heap profile dumped to disk,
//     one-time notification fired through the alert system
func startMemoryMonitor(cacheDBPath string) {
	// Determine where to write heap profiles (same directory as cache DB)
	profileDir := filepath.Dir(cacheDBPath)

	go func() {
		for {
			rssBytes := getProcessRSS()

			if rssBytes >= memAlertThresholdBytes {
				// === ALERT MODE ===
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				logDetailedMemStats(&m, cacheDBPath, rssBytes)

				// Dump heap profile once (survives restart on Railway volume)
				if memProfileDumped.CompareAndSwap(false, true) {
					dumpHeapProfile(profileDir)
				}

				// Send notification once
				if memAlertNotified.CompareAndSwap(false, true) {
					dbSizeMB := getDBFileSizeMB(cacheDBPath)
					notifier.PublishMemoryThresholdExceeded(rssBytes/1024/1024, map[string]interface{}{
						"heap_alloc_mb":  m.HeapAlloc / 1024 / 1024,
						"heap_inuse_mb":  m.HeapInuse / 1024 / 1024,
						"sys_mb":         m.Sys / 1024 / 1024,
						"db_file_mb":     dbSizeMB,
						"goroutines":     runtime.NumGoroutine(),
						"vm_size_mb":     getVmSizeBytes() / 1024 / 1024,
						"gc_cycles":      m.NumGC,
						"stack_inuse_mb": m.StackInuse / 1024 / 1024,
					})
				}

				time.Sleep(memMonitorDetailedInterval)

			} else {
				var m runtime.MemStats
				runtime.ReadMemStats(&m)

				// === WATCH MODE === (one-time snapshot when crossing 4GB)
				if rssBytes >= memWatchThresholdBytes {
					if memWatchNotified.CompareAndSwap(false, true) {
						log.Warnf("%s Memory crossed watch threshold (4GB) — logging snapshot",
							logcolors.LogMemoryAlert)
						logDetailedMemStats(&m, cacheDBPath, rssBytes)
					}
				} else {
					// Below watch threshold — reset all flags
					memWatchNotified.Store(false)
					memAlertNotified.Store(false)
					memProfileDumped.Store(false)
				}

				// === NORMAL MODE ===
				log.Infof("%s RSS: %dMB | Heap: %dMB | Sys: %dMB | DB: %.0fMB | GCs: %d",
					logcolors.LogMemory,
					rssBytes/1024/1024,
					m.HeapAlloc/1024/1024,
					m.Sys/1024/1024,
					getDBFileSizeMB(cacheDBPath),
					m.NumGC,
				)

				time.Sleep(memMonitorNormalInterval)
			}
		}
	}()

	log.Infof("%s Memory monitor started (watch: %dGB, alert: %dGB, normal: %v, detailed: %v)",
		logcolors.LogMemory,
		memWatchThresholdBytes/1024/1024/1024,
		memAlertThresholdBytes/1024/1024/1024,
		memMonitorNormalInterval,
		memMonitorDetailedInterval,
	)
}

// logDetailedMemStats logs a comprehensive memory breakdown for diagnosing OOM.
func logDetailedMemStats(m *runtime.MemStats, cacheDBPath string, rssBytes uint64) {
	dbSizeMB := getDBFileSizeMB(cacheDBPath)
	vmSizeBytes := getVmSizeBytes()

	log.Warnf("%s RSS: %dMB | VmSize: %dMB | DB: %.0fMB | "+
		"Heap(alloc/inuse/idle/released): %d/%d/%d/%dMB | "+
		"Stack: %dMB | Sys: %dMB | GCs: %d | Goroutines: %d",
		logcolors.LogMemoryAlert,
		rssBytes/1024/1024,
		vmSizeBytes/1024/1024,
		dbSizeMB,
		m.HeapAlloc/1024/1024,
		m.HeapInuse/1024/1024,
		m.HeapIdle/1024/1024,
		m.HeapReleased/1024/1024,
		m.StackInuse/1024/1024,
		m.Sys/1024/1024,
		m.NumGC,
		runtime.NumGoroutine(),
	)
}

// dumpHeapProfile writes a heap profile to a dedicated directory on the persistent volume.
// Keeps at most maxHeapProfiles files, deleting the oldest when the limit is reached.
const maxHeapProfiles = 10

func dumpHeapProfile(baseDir string) {
	profileDir := filepath.Join(baseDir, "heap_profiles")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		log.Errorf("%s Failed to create heap profile directory: %v", logcolors.LogMemoryAlert, err)
		return
	}

	// Rotate: delete oldest if at capacity
	rotateHeapProfiles(profileDir)

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	filename := filepath.Join(profileDir, fmt.Sprintf("heap_%s.pprof", timestamp))

	f, err := os.Create(filename)
	if err != nil {
		log.Errorf("%s Failed to create heap profile: %v", logcolors.LogMemoryAlert, err)
		return
	}
	defer f.Close()

	if err := pprof.WriteHeapProfile(f); err != nil {
		log.Errorf("%s Failed to write heap profile: %v", logcolors.LogMemoryAlert, err)
		return
	}

	log.Warnf("%s Heap profile saved to %s", logcolors.LogMemoryAlert, filename)
}

// rotateHeapProfiles keeps at most maxHeapProfiles files in the directory,
// deleting the oldest ones first (FIFO by filename, which embeds the timestamp).
func rotateHeapProfiles(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Filter to only .pprof files
	var profiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".pprof") {
			profiles = append(profiles, e.Name())
		}
	}

	// os.ReadDir returns entries sorted by name; timestamp in the name ensures chronological order
	for len(profiles) >= maxHeapProfiles {
		oldest := filepath.Join(dir, profiles[0])
		if err := os.Remove(oldest); err != nil {
			log.Warnf("%s Failed to remove old heap profile %s: %v", logcolors.LogMemory, oldest, err)
		} else {
			log.Infof("%s Rotated old heap profile: %s", logcolors.LogMemory, profiles[0])
		}
		profiles = profiles[1:]
	}
}

// getProcessRSS reads the resident set size from /proc/self/status (Linux).
// Falls back to runtime.MemStats.Sys on non-Linux systems (e.g. macOS dev).
func getProcessRSS() uint64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		// Fallback for non-Linux (macOS development)
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.Sys
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			var rssKB uint64
			fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "VmRSS:")), "%d", &rssKB)
			return rssKB * 1024
		}
	}
	return 0
}

// getVmSizeBytes reads the virtual memory size from /proc/self/status (Linux).
// This includes the BoltDB mmap region — useful for comparing against RSS
// to see how much of the mmap is actually paged in.
func getVmSizeBytes() uint64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmSize:") {
			var vmKB uint64
			fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "VmSize:")), "%d", &vmKB)
			return vmKB * 1024
		}
	}
	return 0
}

// getDBFileSizeMB returns the BoltDB file size in MB via os.Stat (no page-in).
func getDBFileSizeMB(path string) float64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return float64(info.Size()) / 1024 / 1024
}
