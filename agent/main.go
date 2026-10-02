package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Version is injected at build time via -ldflags "-X main.Version=...",
// matching the panel binary's own build (see .github/workflows/release.yml).
var Version = "dev"

func main() {
	configPath := flag.String("config", "/etc/server-panel/agent.json", "配置文件路径")
	showVersion := flag.Bool("version", false, "打印版本并退出")
	selfUpdate := flag.Bool("self-update", false, "处理排队的自更新请求（由 root systemd 单元调用）")
	flag.Parse()

	if *showVersion {
		fmt.Println(Version)
		return
	}

	cfg, err := LoadAgentConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if *selfUpdate {
		if err := newSelfUpdater(cfg).run(); err != nil {
			log.Fatalf("Self-update failed: %v", err)
		}
		return
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
	paths := defaultUpdatePaths
	status := UpdateStatus{AutoUpdate: paths.updaterInstalled(), Error: paths.lastUpdateError()}
	target, err := Report(cfg.CenterURL, cfg.APIKey, Version, snapshot, status, cfg.TLSSkipVerify)
	if err != nil {
		log.Printf("Report failed: %v", err)
		return
	}
	log.Printf("Report OK — CPU: %.1f%%, MEM: %.1f%%, LOAD: %.2f",
		snapshot.CPUPercent, snapshot.MemoryPercent, snapshot.LoadAvg1)
	paths.markReportOK()
	if target != "" {
		paths.requestUpdate(target, time.Now())
	}
}
