package database

import (
	"fmt"
	"strings"
)

type Upgrade struct {
	Version     string
	Description string
	SQL         []string
	Func        func() error
}

var upgrades = []Upgrade{
	// 新增升级在此追加，版本号递增
}

func LatestVersion() string {
	if len(upgrades) == 0 {
		return "1.0.0"
	}
	return upgrades[len(upgrades)-1].Version
}

func RunUpgrades() error {
	// 确保 schema_version 表存在
	_, err := DB.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version    TEXT NOT NULL,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("failed to create schema_version: %w", err)
	}

	// 读当前版本
	var currentVersion string
	err = DB.QueryRow("SELECT version FROM schema_version ORDER BY version DESC LIMIT 1").Scan(&currentVersion)
	if err != nil {
		// 新安装，插入基线版本
		_, err = DB.Exec("INSERT OR IGNORE INTO schema_version (version) VALUES ('1.0.0')")
		if err != nil {
			return fmt.Errorf("failed to insert baseline schema version: %w", err)
		}
		currentVersion = "1.0.0"
	}

	for _, u := range upgrades {
		if versionLE(u.Version, currentVersion) {
			continue
		}
		for _, stmt := range u.SQL {
			if _, err := DB.Exec(stmt); err != nil {
				if strings.Contains(err.Error(), "duplicate column name") {
					continue
				}
				return fmt.Errorf("upgrade %s failed: %w\nSQL: %s", u.Version, err, stmt[:100])
			}
		}
		if u.Func != nil {
			if err := u.Func(); err != nil {
				return fmt.Errorf("upgrade %s func failed: %w", u.Version, err)
			}
		}
		_, err = DB.Exec("INSERT OR REPLACE INTO schema_version (version) VALUES (?)", u.Version)
		if err != nil {
			return fmt.Errorf("failed to update schema_version: %w", err)
		}
	}
	return nil
}

func versionLE(a, b string) bool {
	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var va, vb string
		if i < len(partsA) {
			va = partsA[i]
		}
		if i < len(partsB) {
			vb = partsB[i]
		}
		if va < vb {
			return true
		}
		if va > vb {
			return false
		}
	}
	return true
}
