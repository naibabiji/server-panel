package executor

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Update source is hard-coded on purpose: the auto-updater must never be
// pointed at an arbitrary repository via config or user input.
const (
	panelRepoOwner = "naibabiji"
	panelRepoName  = "server-panel"
)

// releasePubKeyHex verifies the detached signature published alongside each
// release's checksums file. The matching private key is kept locally and is
// never committed to this repository.
const releasePubKeyHex = "be74aa38e024baa117156f62c9714961f7e7d1aaa36138c527b6bdf1544e9da0"

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

// VerifyReleaseSignature verifies an Ed25519 signature over message using the
// embedded release public key.
func VerifyReleaseSignature(message, signature []byte) bool {
	pub, err := hex.DecodeString(releasePubKeyHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), message, signature)
}

// CompareVersions compares two vX.Y.Z[-prerelease] tags. Returns 1 if a > b,
// -1 if a < b, 0 if equal.
func CompareVersions(a, b string) int {
	majA, minA, patA, preA := parseVersion(a)
	majB, minB, patB, preB := parseVersion(b)

	if c := compareInt(majA, majB); c != 0 {
		return c
	}
	if c := compareInt(minA, minB); c != 0 {
		return c
	}
	if c := compareInt(patA, patB); c != 0 {
		return c
	}
	return comparePrerelease(preA, preB)
}

// IsStableVersion reports whether tag is a plain vX.Y.Z with no prerelease
// suffix. Only stable versions are eligible for auto-update.
func IsStableVersion(tag string) bool {
	v := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	if strings.Contains(v, "-") {
		return false
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

// IsPatchBump reports whether latest is a patch-level increment over current
// (same major.minor, higher patch only).
func IsPatchBump(current, latest string) bool {
	curMaj, curMin, curPat, _ := parseVersion(current)
	latMaj, latMin, latPat, _ := parseVersion(latest)
	return curMaj == latMaj && curMin == latMin && latPat > curPat
}

func parseVersion(v string) (major, minor, patch int, prerelease string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if idx := strings.Index(v, "-"); idx >= 0 {
		prerelease = v[idx+1:]
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	major = atoiSafeVersionPart(parts, 0)
	minor = atoiSafeVersionPart(parts, 1)
	patch = atoiSafeVersionPart(parts, 2)
	return
}

func atoiSafeVersionPart(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n, _ := strconv.Atoi(parts[i])
	return n
}

func compareInt(a, b int) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	default:
		return 0
	}
}

// comparePrerelease implements semver 2.0 precedence rules: a version
// without a prerelease suffix outranks the same version with one; otherwise
// prerelease identifiers are compared dot-segment by dot-segment (numeric
// identifiers compared numerically, alphanumeric compared lexically, numeric
// always ranks below alphanumeric).
func comparePrerelease(a, b string) int {
	if a == "" && b == "" {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")
	for i := 0; i < len(partsA) || i < len(partsB); i++ {
		if i >= len(partsA) {
			return -1
		}
		if i >= len(partsB) {
			return 1
		}
		if c := comparePrereleaseIdentifier(partsA[i], partsB[i]); c != 0 {
			return c
		}
	}
	return 0
}

func comparePrereleaseIdentifier(a, b string) int {
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	if errA == nil && errB == nil {
		return compareInt(na, nb)
	}
	if errA == nil {
		return -1
	}
	if errB == nil {
		return 1
	}
	return strings.Compare(a, b)
}
