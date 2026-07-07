package models

type MetricSample struct {
	ID               int64   `json:"id"`
	ServerID         int64   `json:"server_id"`
	CPUPercent       float64 `json:"cpu_percent"`
	MemoryPercent    float64 `json:"memory_percent"`
	MemoryUsed       int64   `json:"memory_used_bytes"`
	MemoryTotal      int64   `json:"memory_total_bytes"`
	DiskPercent      float64 `json:"disk_percent"`
	DiskUsed         int64   `json:"disk_used_bytes"`
	DiskTotal        int64   `json:"disk_total_bytes"`
	NetRXBytes       int64   `json:"net_rx_bytes"`
	NetTXBytes       int64   `json:"net_tx_bytes"`
	LoadAvg1         float64 `json:"load_avg_1"`
	LoadAvg5         float64 `json:"load_avg_5"`
	LoadAvg15        float64 `json:"load_avg_15"`
	UptimeSeconds    int64   `json:"uptime_seconds"`
	IngestLatencyUs  int64   `json:"ingest_latency_us"`
	RecordedAt       string  `json:"recorded_at"`
}

// AgentMetricPayload agent 上报的 JSON body（不包含 vps_id，身份由 API Key 确定）
type AgentMetricPayload struct {
	AgentVersion   string  `json:"agent_version"`
	CPUPercent     float64 `json:"cpu_percent"`
	MemoryPercent  float64 `json:"memory_percent"`
	MemoryUsed     int64   `json:"memory_used_bytes"`
	MemoryTotal    int64   `json:"memory_total_bytes"`
	DiskPercent    float64 `json:"disk_percent"`
	DiskUsed       int64   `json:"disk_used_bytes"`
	DiskTotal      int64   `json:"disk_total_bytes"`
	NetRXBytes     int64   `json:"net_rx_bytes"`
	NetTXBytes     int64   `json:"net_tx_bytes"`
	LoadAvg1       float64 `json:"load_avg_1"`
	LoadAvg5       float64 `json:"load_avg_5"`
	LoadAvg15      float64 `json:"load_avg_15"`
	UptimeSeconds  int64   `json:"uptime_seconds"`
}
