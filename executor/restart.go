package executor

import (
	"fmt"
	"log"
	"os/exec"
	"time"

	"github.com/naibabiji/server-panel/config"
)

// RestartPanelService restarts the systemd service shortly after being called,
// so settings that require a restart (port, random suffix, TLS mode,
// certificates, panel binary updates) take effect without the admin having to
// SSH in manually.
func RestartPanelService() {
	serviceName := PanelServiceName(config.AppConfig)
	go func() {
		time.Sleep(800 * time.Millisecond)
		if err := RestartPanelServiceSync(serviceName); err != nil {
			log.Printf("自动重启服务失败: %v，请手动执行 systemctl restart %s", err, serviceName)
		}
	}()
}

func PanelServiceName(cfg *config.Config) string {
	if cfg != nil && cfg.Systemd.ServiceName != "" {
		return cfg.Systemd.ServiceName
	}
	return "server-panel"
}

func RestartPanelServiceSync(serviceName string) error {
	if serviceName == "" {
		serviceName = "server-panel"
	}
	if err := exec.Command("systemctl", "restart", serviceName).Run(); err != nil {
		return fmt.Errorf("systemctl restart %s failed: %w", serviceName, err)
	}
	return nil
}

func StopPanelServiceSync(serviceName string) error {
	if serviceName == "" {
		serviceName = "server-panel"
	}
	if err := exec.Command("systemctl", "stop", serviceName).Run(); err != nil {
		return fmt.Errorf("systemctl stop %s failed: %w", serviceName, err)
	}
	return nil
}

func StartPanelServiceSync(serviceName string) error {
	if serviceName == "" {
		serviceName = "server-panel"
	}
	if err := exec.Command("systemctl", "start", serviceName).Run(); err != nil {
		return fmt.Errorf("systemctl start %s failed: %w", serviceName, err)
	}
	return nil
}
