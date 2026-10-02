package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// UpdateStatus is the self-update state reported alongside metrics.
type UpdateStatus struct {
	AutoUpdate bool
	Error      string
}

// Report sends one metrics snapshot and returns the version the panel wants
// this Agent to update to ("" for none).
func Report(centerURL, apiKey, version string, snapshot *MetricSnapshot, status UpdateStatus, skipVerify bool) (string, error) {
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
		"auto_update":        status.AutoUpdate,
		"update_error":       status.Error,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal failed: %w", err)
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
			return "", fmt.Errorf("build request failed: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Agent-API-Key", apiKey)

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var parsed struct {
				Data struct {
					AgentUpdate struct {
						Version string `json:"version"`
					} `json:"agent_update"`
				} `json:"data"`
			}
			// A body that fails to decode (e.g. an older panel) just means
			// no update target; the report itself succeeded.
			_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&parsed)
			resp.Body.Close()
			return parsed.Data.AgentUpdate.Version, nil
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

	return "", lastErr
}
