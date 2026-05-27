package main

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type MetricSnapshot struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
	MemoryUsed    int64   `json:"memory_used_bytes"`
	MemoryTotal   int64   `json:"memory_total_bytes"`
	DiskPercent   float64 `json:"disk_percent"`
	DiskUsed      int64   `json:"disk_used_bytes"`
	DiskTotal     int64   `json:"disk_total_bytes"`
	NetRXBytes    int64   `json:"net_rx_bytes"`
	NetTXBytes    int64   `json:"net_tx_bytes"`
	LoadAvg1      float64 `json:"load_avg_1"`
	LoadAvg5      float64 `json:"load_avg_5"`
	LoadAvg15     float64 `json:"load_avg_15"`
	UptimeSeconds int64   `json:"uptime_seconds"`
}

var prevCPUIdle, prevCPUTotal int64
var prevNetRX, prevNetTX int64

func Collect() *MetricSnapshot {
	s := &MetricSnapshot{}

	s.collectCPU()
	s.collectMemory()
	s.collectDisk()
	s.collectNetwork()
	s.collectLoad()
	s.collectUptime()

	return s
}

func (s *MetricSnapshot) collectCPU() {
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

		if prevCPUTotal > 0 {
			totalDelta := total - prevCPUTotal
			idleDelta := idleTotal - prevCPUIdle
			if totalDelta > 0 {
				s.CPUPercent = float64(totalDelta-idleDelta) / float64(totalDelta) * 100
			}
		}
		prevCPUIdle = idleTotal
		prevCPUTotal = total
	}
}

func (s *MetricSnapshot) collectMemory() {
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

func (s *MetricSnapshot) collectDisk() {
	out, err := exec.Command("df", "-B1", "-P", "/").Output()
	if err != nil {
		return
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 6 {
		return
	}

	total, _ := strconv.ParseInt(fields[1], 10, 64)
	used, _ := strconv.ParseInt(fields[2], 10, 64)
	if total <= 0 {
		return
	}

	s.DiskTotal = total
	s.DiskUsed = used
	s.DiskPercent = float64(used) / float64(total) * 100
}

func (s *MetricSnapshot) collectNetwork() {
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

	if prevNetRX > 0 {
		s.NetRXBytes = totalRX - prevNetRX
		s.NetTXBytes = totalTX - prevNetTX
	}
	prevNetRX = totalRX
	prevNetTX = totalTX
}

func (s *MetricSnapshot) collectLoad() {
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

func (s *MetricSnapshot) collectUptime() {
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
