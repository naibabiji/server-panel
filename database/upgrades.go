package database

import (
	"database/sql"
	"fmt"
	"strconv"
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
	{
		Version:     "1.1.0",
		Description: "Store encrypted Agent API keys for later viewing",
		SQL: []string{
			`ALTER TABLE servers ADD COLUMN agent_api_key_enc TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		Version:     "1.2.0",
		Description: "Store website panel login information",
		SQL: []string{
			`ALTER TABLE websites ADD COLUMN panel_type TEXT NOT NULL DEFAULT 'none'`,
			`ALTER TABLE websites ADD COLUMN panel_url TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE websites ADD COLUMN panel_username TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE websites ADD COLUMN panel_password_enc TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		Version:     "1.3.0",
		Description: "Rename user records to customers",
		Func:        migrateUsersToCustomers,
	},
	{
		Version:     "1.4.0",
		Description: "Add operation log table for update/audit history",
		SQL: []string{
			`CREATE TABLE IF NOT EXISTS operation_logs (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				operation  TEXT    NOT NULL,
				target     TEXT    DEFAULT '',
				status     TEXT    NOT NULL DEFAULT 'success',
				message    TEXT    DEFAULT '',
				created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`CREATE INDEX IF NOT EXISTS idx_operation_logs_created ON operation_logs(created_at)`,
		},
	},
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
	currentVersion, err := currentSchemaVersion()
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

func currentSchemaVersion() (string, error) {
	rows, err := DB.Query("SELECT version FROM schema_version")
	if err != nil {
		return "", err
	}
	defer rows.Close()

	current := ""
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return "", err
		}
		if current == "" || versionLE(current, version) {
			current = version
		}
	}
	if current == "" {
		return "", sql.ErrNoRows
	}
	return current, rows.Err()
}

func versionLE(a, b string) bool {
	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		va := atoiSafe(partsA, i)
		vb := atoiSafe(partsB, i)
		if va < vb {
			return true
		}
		if va > vb {
			return false
		}
	}
	return true
}

func atoiSafe(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	v, _ := strconv.Atoi(parts[i])
	return v
}

func migrateUsersToCustomers() error {
	hasUsers, err := tableExists("users")
	if err != nil {
		return err
	}
	hasCustomers, err := tableExists("customers")
	if err != nil {
		return err
	}
	if hasUsers && !hasCustomers {
		if _, err := DB.Exec(`ALTER TABLE users RENAME TO customers`); err != nil {
			return err
		}
	}

	if ok, err := columnExists("servers", "user_id"); err != nil {
		return err
	} else if ok {
		if _, err := DB.Exec(`ALTER TABLE servers RENAME COLUMN user_id TO customer_id`); err != nil {
			return err
		}
	}
	if ok, err := columnExists("websites", "user_id"); err != nil {
		return err
	} else if ok {
		if _, err := DB.Exec(`ALTER TABLE websites RENAME COLUMN user_id TO customer_id`); err != nil {
			return err
		}
	}

	_, _ = DB.Exec(`DROP INDEX IF EXISTS idx_servers_user`)
	_, _ = DB.Exec(`CREATE INDEX IF NOT EXISTS idx_servers_customer ON servers(customer_id)`)
	_, _ = DB.Exec(`CREATE INDEX IF NOT EXISTS idx_websites_customer ON websites(customer_id)`)
	return nil
}

func tableExists(name string) (bool, error) {
	var count int
	err := DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count)
	return count > 0, err
}

func columnExists(tableName, columnName string) (bool, error) {
	rows, err := DB.Query(`PRAGMA table_info(` + tableName + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == columnName {
			return true, nil
		}
	}
	return false, rows.Err()
}
