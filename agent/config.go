package main

import (
	"encoding/json"
	"os"
)

type AgentConfig struct {
	CenterURL      string `json:"center_url"`
	APIKey         string `json:"api_key"`
	IntervalSeconds int   `json:"interval_seconds"`
	TLSSkipVerify  bool   `json:"tls_skip_verify"`
}

func LoadAgentConfig(path string) (*AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg AgentConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.IntervalSeconds <= 0 {
		cfg.IntervalSeconds = 60
	}
	return &cfg, nil
}
