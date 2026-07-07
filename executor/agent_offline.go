package executor

import (
	"log"
	"time"

	"github.com/naibabiji/server-panel/database"
)

// agentOfflineThreshold is how long a server can go without a metrics report
// before it's considered offline. The default agent report interval is 60s,
// so this gives room for a couple of missed/retried reports before flipping.
const agentOfflineThreshold = 3 * time.Minute

// StartAgentOfflineChecker periodically clears is_online for servers whose
// agent stopped reporting. AgentAuth only ever sets is_online=1 on a
// successful report; nothing else flips it back to 0 when an agent goes
// silent, which left "is_online" stuck true and made the server_offline
// alert (which requires is_online=0) unable to fire.
func StartAgentOfflineChecker(interval time.Duration) {
	go func() {
		for {
			time.Sleep(interval)
			markStaleAgentsOffline()
		}
	}()
}

func markStaleAgentsOffline() {
	db := database.GetDB()
	if db == nil {
		return
	}
	cutoff := time.Now().UTC().Add(-agentOfflineThreshold).Format("2006-01-02 15:04:05")
	if _, err := db.Exec(
		`UPDATE servers SET is_online = 0
		 WHERE is_online = 1 AND last_seen_at IS NOT NULL AND last_seen_at < ?`,
		cutoff,
	); err != nil {
		log.Printf("failed to mark stale agents offline: %v", err)
	}
}
