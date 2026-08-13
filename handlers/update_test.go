package handlers

import (
	"testing"
	"time"

	"github.com/naibabiji/server-panel/executor"
)

func TestCachedLatestPanelReleaseForceRefresh(t *testing.T) {
	originalFetch := fetchLatestPanelRelease
	t.Cleanup(func() {
		fetchLatestPanelRelease = originalFetch
		panelReleaseCache.Lock()
		panelReleaseCache.release = nil
		panelReleaseCache.expireAt = time.Time{}
		panelReleaseCache.lastErr = nil
		panelReleaseCache.errExpireAt = time.Time{}
		panelReleaseCache.Unlock()
	})

	panelReleaseCache.Lock()
	panelReleaseCache.release = &executor.GithubRelease{TagName: "v1.4.40"}
	panelReleaseCache.expireAt = time.Now().Add(panelReleaseCacheTTL)
	panelReleaseCache.Unlock()

	requests := 0
	fetchLatestPanelRelease = func() (*executor.GithubRelease, error) {
		requests++
		return &executor.GithubRelease{TagName: "v1.4.41"}, nil
	}

	cached, err := cachedLatestPanelRelease(false)
	if err != nil || cached.TagName != "v1.4.40" || requests != 0 {
		t.Fatalf("cached result = (%v, %v), requests = %d", cached, err, requests)
	}

	refreshed, err := cachedLatestPanelRelease(true)
	if err != nil || refreshed.TagName != "v1.4.41" || requests != 1 {
		t.Fatalf("refreshed result = (%v, %v), requests = %d", refreshed, err, requests)
	}
}

func TestIsValidAutoUpdateWindow(t *testing.T) {
	tests := []struct {
		name   string
		window string
		want   bool
	}{
		{name: "normal", window: "03:00-05:00", want: true},
		{name: "cross midnight", window: "23:30-01:00", want: true},
		{name: "with spaces", window: " 03:00 - 05:00 ", want: true},
		{name: "empty", window: "", want: false},
		{name: "missing end", window: "03:00-", want: false},
		{name: "bad hour", window: "24:00-05:00", want: false},
		{name: "bad minute", window: "03:60-05:00", want: false},
		{name: "single digit hour", window: "3:00-05:00", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidAutoUpdateWindow(tt.window); got != tt.want {
				t.Fatalf("isValidAutoUpdateWindow(%q) = %v, want %v", tt.window, got, tt.want)
			}
		})
	}
}
