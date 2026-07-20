package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func Report(centerURL, apiKey, version string, snapshot *MetricSnapshot, skipVerify bool) error {
	payload := map[string]interface{}{
		"agent_version":      version,
		"cpu_percent":        snapshot.CPUPercent,
		"memory_percent":     snapshot.MemoryPercent,
		"memory_used_bytes":  snapshot.MemoryUsed,
		"memory_total_bytes": snapshot.MemoryTotal,
		"disk_percent":       snapshot.DiskPercent,
		"disk_used_bytes":    snapshot.DiskUsed,
		"disk_total_bytes":   snapshot.DiskTotal,
		"net_rx_bytes":       snapshot.NetRXBytes,
		"net_tx_bytes":       snapshot.NetTXBytes,
		"load_avg_1":         snapshot.LoadAvg1,
		"load_avg_5":         snapshot.LoadAvg5,
		"load_avg_15":        snapshot.LoadAvg15,
		"uptime_seconds":     snapshot.UptimeSeconds,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal failed: %w", err)
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipVerify},
		DialContext:     resilientDialContext(),
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
	}

	endpoint := strings.TrimRight(centerURL, "/") + "/agent/metrics"
	backoffs := []time.Duration{0, 5 * time.Second, 10 * time.Second}
	var lastErr error
	for attempt, backoff := range backoffs {
		if backoff > 0 {
			time.Sleep(backoff)
		}

		req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build request failed: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Agent-API-Key", apiKey)

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return nil
		}
		lastErr = fmt.Errorf("unexpected status: %d", resp.StatusCode)
		resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			break
		}
		if attempt == len(backoffs)-1 {
			break
		}
	}

	return lastErr
}
