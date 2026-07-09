package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfigDefaultsBasicAuthOn verifies that a config file without the
// basic_auth_enabled key keeps BasicAuth ENABLED (secure default, no silent
// regression for pre-existing installs that predate the toggle).
func TestLoadConfigDefaultsBasicAuthOn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// 没有 security 块，也没有 basic_auth_enabled 字段
	if err := os.WriteFile(path, []byte(`{"panel":{"data_dir":"/tmp/sp"}}`), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.Security.BasicAuthEnabled {
		t.Fatal("missing basic_auth_enabled should default to ENABLED (true)")
	}
}

// TestLoadConfigHonorsExplicitDisable verifies that an explicit
// basic_auth_enabled:false is respected (the settings toggle can turn it off).
func TestLoadConfigHonorsExplicitDisable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"panel":{"data_dir":"/tmp/sp"},"security":{"basic_auth_enabled":false}}`), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Security.BasicAuthEnabled {
		t.Fatal("explicit basic_auth_enabled:false must be honored")
	}
}
