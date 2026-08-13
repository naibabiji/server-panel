package executor

import (
	"errors"
	"os/exec"
	"reflect"
	"testing"
)

func TestAllowPanelPortWithActiveUFW(t *testing.T) {
	original := runFirewallCommand
	t.Cleanup(func() { runFirewallCommand = original })

	var calls [][]string
	runFirewallCommand = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		if name == "ufw" && len(args) == 1 && args[0] == "status" {
			return []byte("Status: active\n"), nil
		}
		return nil, nil
	}

	manager, err := AllowPanelPort(443)
	if err != nil {
		t.Fatalf("AllowPanelPort() error = %v", err)
	}
	if manager != "ufw" {
		t.Fatalf("manager = %q, want ufw", manager)
	}
	want := [][]string{{"ufw", "status"}, {"ufw", "allow", "443/tcp"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestAllowPanelPortFallsBackToFirewalld(t *testing.T) {
	original := runFirewallCommand
	t.Cleanup(func() { runFirewallCommand = original })

	var calls [][]string
	runFirewallCommand = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		if name == "ufw" {
			return nil, exec.ErrNotFound
		}
		return nil, nil
	}

	manager, err := AllowPanelPort(8443)
	if err != nil {
		t.Fatalf("AllowPanelPort() error = %v", err)
	}
	if manager != "firewalld" {
		t.Fatalf("manager = %q, want firewalld", manager)
	}
	want := [][]string{
		{"ufw", "status"},
		{"firewall-cmd", "--state"},
		{"firewall-cmd", "--permanent", "--add-port=8443/tcp"},
		{"firewall-cmd", "--reload"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestAllowPanelPortStopsWhenUFWRuleFails(t *testing.T) {
	original := runFirewallCommand
	t.Cleanup(func() { runFirewallCommand = original })

	runFirewallCommand = func(name string, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "status" {
			return []byte("Status: active\n"), nil
		}
		return []byte("permission denied"), errors.New("exit status 1")
	}

	if _, err := AllowPanelPort(443); err == nil {
		t.Fatal("AllowPanelPort() error = nil, want failure")
	}
}
