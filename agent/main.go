package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	configPath := flag.String("config", "/etc/server-panel/agent.json", "配置文件路径")
	flag.Parse()

	cfg, err := LoadAgentConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Printf("Agent started, reporting to %s every %ds", cfg.CenterURL, cfg.IntervalSeconds)

	ticker := time.NewTicker(time.Duration(cfg.IntervalSeconds) * time.Second)
	defer ticker.Stop()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// 立刻上报一次
	doReport(cfg)

	for {
		select {
		case <-ticker.C:
			doReport(cfg)
		case sig := <-quit:
			log.Printf("Received %v, shutting down", sig)
			return
		}
	}
}

func doReport(cfg *AgentConfig) {
	snapshot := Collect()
	if err := Report(cfg.CenterURL, cfg.APIKey, snapshot, cfg.TLSSkipVerify); err != nil {
		log.Printf("Report failed: %v", err)
	} else {
		log.Printf("Report OK — CPU: %.1f%%, MEM: %.1f%%, LOAD: %.2f",
			snapshot.CPUPercent, snapshot.MemoryPercent, snapshot.LoadAvg1)
	}
}


