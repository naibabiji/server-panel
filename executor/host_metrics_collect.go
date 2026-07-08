package executor

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// HostMetricSnapshot is a point-in-time reading of the machine the panel
// process itself is running on. Collected the same way agent/collector.go
// collects for managed servers (same /proc files, same delta-based
// CPU%/network-throughput technique), but duplicated rather than shared:
// agent/collector.go is part of the already-deployed Agent reporting path
// used by every managed server, and isn't worth touching for a DRY win here.
type HostMetricSnapshot struct {
	CPUPercent    float64
	MemoryPercent float64
	MemoryUsed    int64
	MemoryTotal   int64
	DiskPercent   float64
	DiskUsed      int64
	DiskTotal     int64
	NetRXBytes    int64
	NetTXBytes    int64
	LoadAvg1      float64
	LoadAvg5      float64
	LoadAvg15     float64
	UptimeSeconds int64
}

var prevHostCPUIdle, prevHostCPUTotal int64
var prevHostNetRX, prevHostNetTX int64

// CollectHostMetrics reads the current snapshot. CPUPercent and
// NetRX/TXBytes are deltas since the previous call, so the first call after
// process start always reports 0 for those fields - same cold-start
// behavior as a freshly-started Agent.
func CollectHostMetrics() *HostMetricSnapshot {
	s := &HostMetricSnapshot{}

	s.collectCPU()
	s.collectMemory()
	s.collectDisk()
	s.collectNetwork()
	s.collectLoad()
	s.collectUptime()

	return s
}

func (s *HostMetricSnapshot) collectCPU() {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 || fields[0] != "cpu" {
			return
		}
		user, _ := strconv.ParseInt(fields[1], 10, 64)
		nice, _ := strconv.ParseInt(fields[2], 10, 64)
		system, _ := strconv.ParseInt(fields[3], 10, 64)
		idle, _ := strconv.ParseInt(fields[4], 10, 64)
		iowait, _ := strconv.ParseInt(fields[5], 10, 64)
		irq, _ := strconv.ParseInt(fields[6], 10, 64)
		softirq, _ := strconv.ParseInt(fields[7], 10, 64)

		total := user + nice + system + idle + iowait + irq + softirq
		idleTotal := idle + iowait

		if prevHostCPUTotal > 0 {
			totalDelta := total - prevHostCPUTotal
			idleDelta := idleTotal - prevHostCPUIdle
			if totalDelta > 0 {
				s.CPUPercent = float64(totalDelta-idleDelta) / float64(totalDelta) * 100
			}
		}
		prevHostCPUIdle = idleTotal
		prevHostCPUTotal = total
	}
}

func (s *HostMetricSnapshot) collectMemory() {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()

	var total, available int64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				total, _ = strconv.ParseInt(fields[1], 10, 64)
			}
		}
		if strings.HasPrefix(line, "MemAvailable:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				available, _ = strconv.ParseInt(fields[1], 10, 64)
			}
		}
	}
	if total > 0 {
		s.MemoryTotal = total * 1024
		s.MemoryUsed = (total - available) * 1024
		s.MemoryPercent = float64(total-available) / float64(total) * 100
	}
}

func (s *HostMetricSnapshot) collectDisk() {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return
	}

	bsize := uint64(stat.Bsize)
	total := stat.Blocks * bsize
	free := stat.Bfree * bsize
	if total == 0 {
		return
	}
	used := total - free

	s.DiskTotal = int64(total)
	s.DiskUsed = int64(used)
	s.DiskPercent = float64(used) / float64(total) * 100
}

func (s *HostMetricSnapshot) collectNetwork() {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return
	}
	defer f.Close()

	var totalRX, totalTX int64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) < 2 {
			continue
		}
		iface := strings.TrimSpace(parts[0])
		if iface == "lo" {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}
		rx, _ := strconv.ParseInt(fields[0], 10, 64)
		tx, _ := strconv.ParseInt(fields[8], 10, 64)
		totalRX += rx
		totalTX += tx
	}

	if prevHostNetRX > 0 {
		s.NetRXBytes = totalRX - prevHostNetRX
		s.NetTXBytes = totalTX - prevHostNetTX
	}
	prevHostNetRX = totalRX
	prevHostNetTX = totalTX
}

func (s *HostMetricSnapshot) collectLoad() {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 3 {
		s.LoadAvg1, _ = strconv.ParseFloat(fields[0], 64)
		s.LoadAvg5, _ = strconv.ParseFloat(fields[1], 64)
		s.LoadAvg15, _ = strconv.ParseFloat(fields[2], 64)
	}
}

func (s *HostMetricSnapshot) collectUptime() {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 1 {
		uptime, _ := strconv.ParseFloat(fields[0], 64)
		s.UptimeSeconds = int64(uptime)
	}
}
