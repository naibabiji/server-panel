package executor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"
)

// Update source is hard-coded on purpose: the auto-updater must never be
// pointed at an arbitrary repository via config or user input.
const (
	panelRepoOwner = "naibabiji"
	panelRepoName  = "server-panel"
)

type GithubRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Body    string `json:"body"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// FetchLatestPanelRelease queries the GitHub Releases API for the latest
// server-panel release.
func FetchLatestPanelRelease() (*GithubRelease, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", panelRepoOwner, panelRepoName)
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub Releases 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub Releases 返回状态码 %d", resp.StatusCode)
	}

	var release GithubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("解析 GitHub Releases 响应失败: %w", err)
	}
	if release.TagName == "" {
		return nil, fmt.Errorf("GitHub Releases 响应缺少版本号")
	}
	return &release, nil
}

// PanelAssetNames returns the expected release asset filenames for the
// current architecture: the panel binary, its checksums file, and the
// checksums file's detached signature.
func PanelAssetNames() (binary, checksums, signature string, err error) {
	var arch string
	switch runtime.GOARCH {
	case "amd64", "arm64":
		arch = runtime.GOARCH
	default:
		return "", "", "", fmt.Errorf("不支持的架构: %s", runtime.GOARCH)
	}
	return fmt.Sprintf("server-panel-linux-%s", arch),
		fmt.Sprintf("checksums-%s.txt", arch),
		fmt.Sprintf("checksums-%s.txt.sig", arch),
		nil
}

// FindAssetURL returns the download URL of the named asset, or "" if absent.
func FindAssetURL(release *GithubRelease, name string) string {
	for _, a := range release.Assets {
		if a.Name == name {
			return a.BrowserDownloadURL
		}
	}
	return ""
}
