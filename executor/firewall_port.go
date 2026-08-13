package executor

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var runFirewallCommand = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// AllowPanelPort opens the new panel port in an active host firewall before
// the service moves to that port. It does not enable or install a firewall.
func AllowPanelPort(port int) (string, error) {
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid panel port: %d", port)
	}
	portSpec := fmt.Sprintf("%d/tcp", port)

	if output, err := runFirewallCommand("ufw", "status"); err == nil {
		if strings.Contains(string(output), "Status: active") {
			if output, err = runFirewallCommand("ufw", "allow", portSpec); err != nil {
				return "", fmt.Errorf("ufw allow %s failed: %s: %w", portSpec, strings.TrimSpace(string(output)), err)
			}
			return "ufw", nil
		}
	} else if !errors.Is(err, exec.ErrNotFound) {
		return "", fmt.Errorf("check ufw status failed: %w", err)
	}

	if _, err := runFirewallCommand("firewall-cmd", "--state"); err == nil {
		if output, err := runFirewallCommand("firewall-cmd", "--permanent", "--add-port="+portSpec); err != nil {
			return "", fmt.Errorf("firewalld allow %s failed: %s: %w", portSpec, strings.TrimSpace(string(output)), err)
		}
		if output, err := runFirewallCommand("firewall-cmd", "--reload"); err != nil {
			return "", fmt.Errorf("firewalld reload failed: %s: %w", strings.TrimSpace(string(output)), err)
		}
		return "firewalld", nil
	}

	return "", nil
}
