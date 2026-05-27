package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func Report(centerURL, apiKey string, snapshot *MetricSnapshot, skipVerify bool) error {
	payload := map[string]interface{}{
		"agent_version":    "1.0.0",
		"cpu_percent":      snapshot.CPUPercent,
		"memory_percent":   snapshot.MemoryPercent,
		"memory_used_bytes":  snapshot.MemoryUsed,
		"memory_total_bytes": snapshot.MemoryTotal,
		"disk_percent":     snapshot.DiskPercent,
		"disk_used_bytes":  snapshot.DiskUsed,
		"disk_total_bytes": snapshot.DiskTotal,
		"net_rx_bytes":     snapshot.NetRXBytes,
		"net_tx_bytes":     snapshot.NetTXBytes,
		"load_avg_1":       snapshot.LoadAvg1,
		"load_avg_5":       snapshot.LoadAvg5,
		"load_avg_15":      snapshot.LoadAvg15,
		"uptime_seconds":   snapshot.UptimeSeconds,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal failed: %w", err)
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipVerify},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
	}

	resp, err := client.Post(centerURL+"/agent/metrics", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	return nil
}
