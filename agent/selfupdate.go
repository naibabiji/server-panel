package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/naibabiji/server-panel/releasecheck"
)

// Self-update is split by privilege. The Agent service runs unprivileged
// under ProtectSystem=strict and can only write its own StateDirectory, so
// when the panel announces a newer version it just drops the target tag into
// requestFile. A root systemd path unit (installed next to the Agent) sees the
// file and runs `server-panel-agent -self-update`, which downloads the
// release, verifies it against the release signing key and swaps the binary.
// Root never writes into the Agent-writable directory; its results go to the
// root-owned resultDir, which the Agent only reads.

// updatePaths groups every filesystem location self-update touches so tests
// can redirect them into a temp dir.
type updatePaths struct {
	binary    string // installed Agent binary
	stateDir  string // Agent-owned StateDirectory
	resultDir string // root-owned, world-readable
	unitPath  string // presence means the root updater is installed
}

var defaultUpdatePaths = updatePaths{
	binary:    "/usr/local/bin/server-panel-agent",
	stateDir:  "/var/lib/server-panel-agent",
	resultDir: "/var/lib/server-panel-agent-update",
	unitPath:  "/etc/systemd/system/server-panel-agent-update.path",
}

func (p updatePaths) requestFile() string  { return filepath.Join(p.stateDir, "update-request") }
func (p updatePaths) reportOKFile() string { return filepath.Join(p.stateDir, "report-ok") }
func (p updatePaths) resultFile() string   { return filepath.Join(p.resultDir, "result.json") }

// updateRetryCooldown is how long a failed (or interrupted) update to a given
// version blocks another attempt at that same version.
const updateRetryCooldown = 6 * time.Hour

// healthWaitTimeout bounds how long the updater waits for the restarted
// Agent to complete a report before rolling back. A report with retries and
// DNS fallbacks can take close to a minute.
const healthWaitTimeout = 3 * time.Minute

type updateResult struct {
	Version string    `json:"version"`
	Running bool      `json:"running,omitempty"`
	OK      bool      `json:"ok"`
	Error   string    `json:"error,omitempty"`
	Time    time.Time `json:"time"`
}

// --- Agent (unprivileged) side ---

func (p updatePaths) updaterInstalled() bool {
	if _, err := os.Stat(p.unitPath); err != nil {
		return false
	}
	info, err := os.Stat(p.stateDir)
	return err == nil && info.IsDir()
}

func (p updatePaths) readResult() (updateResult, bool) {
	var r updateResult
	data, err := readSmallRegularFile(p.resultFile(), 4096)
	if err != nil || json.Unmarshal(data, &r) != nil {
		return updateResult{}, false
	}
	return r, true
}

// requestUpdate asks the root updater to install target, unless an attempt
// at target is already queued, running, or failed within updateRetryCooldown.
func (p updatePaths) requestUpdate(target string, now time.Time) {
	if !p.updaterInstalled() || !releasecheck.IsStableVersion(target) ||
		releasecheck.CompareVersions(target, Version) <= 0 {
		return
	}
	if _, err := os.Lstat(p.requestFile()); err == nil {
		return
	}
	if r, ok := p.readResult(); ok && r.Version == target && !r.OK {
		if age := now.Sub(r.Time); age >= 0 && age < updateRetryCooldown {
			return
		}
	}
	tmp := p.requestFile() + ".tmp"
	if err := os.WriteFile(tmp, []byte(target), 0o644); err != nil {
		log.Printf("Self-update request failed: %v", err)
		return
	}
	if err := os.Rename(tmp, p.requestFile()); err != nil {
		log.Printf("Self-update request failed: %v", err)
		return
	}
	log.Printf("Requested self-update to %s", target)
}

// lastUpdateError is the failure reported to the panel: the last finished
// attempt failed and the Agent is still older than the version it tried.
func (p updatePaths) lastUpdateError() string {
	r, ok := p.readResult()
	if !ok || r.OK || r.Running || releasecheck.CompareVersions(r.Version, Version) <= 0 {
		return ""
	}
	return r.Error
}

// markReportOK records that this process completed a report, which is the
// updater's health signal for a freshly installed binary.
func (p updatePaths) markReportOK() {
	if _, err := os.Stat(p.stateDir); err != nil {
		return
	}
	if err := os.WriteFile(p.reportOKFile(), []byte(Version), 0o644); err != nil {
		log.Printf("Write report-ok failed: %v", err)
	}
}

// --- root updater side ---

type selfUpdater struct {
	paths   updatePaths
	proxy   string
	fetch   func(url string) ([]byte, error)
	restart func() error
	// checkVersion runs a candidate binary and returns the version it reports.
	checkVersion func(path string) (string, error)
	// verify checks a published .sig file (see releasecheck.VerifySignatureFile).
	verify func(message, sigFile []byte) error
}

func newSelfUpdater(cfg *AgentConfig) *selfUpdater {
	return &selfUpdater{
		paths: defaultUpdatePaths,
		proxy: cfg.GitHubProxy,
		fetch: releasecheck.NewFetcher(cfg.GitHubProxy,
			&http.Transport{DialContext: resilientDialContext()}, 5*time.Minute),
		restart:      func() error { return exec.Command("systemctl", "restart", "server-panel-agent").Run() },
		checkVersion: binaryVersion,
		verify:       releasecheck.VerifySignatureFile,
	}
}

// run handles one queued request. It always consumes the request first so a
// failure can never make the path unit retrigger in a loop.
func (u *selfUpdater) run() error {
	data, err := readSmallRegularFile(u.paths.requestFile(), 64)
	_ = os.Remove(u.paths.requestFile())
	if err != nil {
		return fmt.Errorf("read update request: %w", err)
	}
	target := strings.TrimSpace(string(data))
	if !releasecheck.IsStableVersion(target) {
		return fmt.Errorf("invalid update target %q", target)
	}
	if releasecheck.CompareVersions(target, Version) <= 0 {
		return nil // already current: a duplicate request queued during the last update
	}

	u.writeResult(updateResult{Version: target, Running: true, Time: time.Now()})
	err = u.install(target)
	res := updateResult{Version: target, OK: err == nil, Time: time.Now()}
	if err != nil {
		res.Error = err.Error()
	}
	u.writeResult(res)
	return err
}

func (u *selfUpdater) install(target string) error {
	return u.installWithTimeout(target, healthWaitTimeout)
}

func (u *selfUpdater) installWithTimeout(target string, healthTimeout time.Duration) error {
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("unsupported architecture %s", arch)
	}
	wantHash, err := releasecheck.VerifiedAgentChecksum(u.fetch, u.verify, u.proxy, target, arch)
	if err != nil {
		return err
	}
	bin, err := u.fetch(releasecheck.FileURL(u.proxy, target, releasecheck.AgentBinaryName(arch)))
	if err != nil {
		return fmt.Errorf("download binary: %w", err)
	}
	sum := sha256.Sum256(bin)
	if hex.EncodeToString(sum[:]) != wantHash {
		return fmt.Errorf("binary SHA-256 mismatch")
	}

	// Stage next to the live binary so every swap below is a single atomic
	// same-filesystem rename: the live path always holds a complete binary,
	// even if the updater is killed or the host loses power mid-update.
	staged := u.paths.binary + ".new"
	backup := u.paths.binary + ".bak"
	if err := writeFileSynced(staged, bin, 0o755); err != nil {
		return fmt.Errorf("stage binary: %w", err)
	}
	defer os.Remove(staged)
	if got, err := u.checkVersion(staged); err != nil || got != target {
		return fmt.Errorf("new binary reports version %q (want %s): %v", got, target, err)
	}
	current, err := os.ReadFile(u.paths.binary)
	if err != nil {
		return fmt.Errorf("read current binary: %w", err)
	}
	if err := writeFileSynced(backup, current, 0o755); err != nil {
		return fmt.Errorf("back up current binary: %w", err)
	}
	if err := os.Rename(staged, u.paths.binary); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	_ = syncDir(filepath.Dir(u.paths.binary))

	_ = os.Remove(u.paths.reportOKFile())
	startErr := u.restart()
	if startErr == nil && u.waitHealthy(target, healthTimeout) {
		log.Printf("Self-update to %s succeeded", target)
		return nil
	}
	reason := fmt.Sprintf("new version did not report successfully within %s", healthTimeout)
	if startErr != nil {
		reason = fmt.Sprintf("restarting new version failed: %v", startErr)
	}

	// Roll back to the previous binary.
	if err := os.Rename(backup, u.paths.binary); err != nil {
		return fmt.Errorf("%s; restoring previous binary failed: %v", reason, err)
	}
	_ = syncDir(filepath.Dir(u.paths.binary))
	if err := u.restart(); err != nil {
		return fmt.Errorf("%s; previous binary restored but restarting it failed: %v", reason, err)
	}
	return fmt.Errorf("%s; rolled back to %s", reason, Version)
}

// writeFileSynced writes data to path via a temp file + fsync + rename, so
// path is either absent/old or complete - never a partial binary.
func writeFileSynced(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (u *selfUpdater) waitHealthy(target string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if data, err := readSmallRegularFile(u.paths.reportOKFile(), 64); err == nil &&
			strings.TrimSpace(string(data)) == target {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Second)
	}
}

func (u *selfUpdater) writeResult(r updateResult) {
	if err := os.MkdirAll(u.paths.resultDir, 0o755); err != nil {
		log.Printf("Write update result failed: %v", err)
		return
	}
	data, _ := json.Marshal(r)
	tmp := u.paths.resultFile() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err == nil {
		err = os.Rename(tmp, u.paths.resultFile())
		if err != nil {
			log.Printf("Write update result failed: %v", err)
		}
	}
}

// readSmallRegularFile reads a file without following a final symlink and
// rejects anything that is not a small, singly-linked regular file. The root
// updater reads files from the Agent-writable state directory through this,
// so a planted symlink or hard link to another file is never read as root.
func readSmallRegularFile(path string, maxBytes int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, fmt.Errorf("%s is not a small regular file", path)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink != 1 {
		return nil, fmt.Errorf("%s has %d hard links", path, st.Nlink)
	}
	return io.ReadAll(io.LimitReader(f, maxBytes))
}

func binaryVersion(path string) (string, error) {
	out, err := exec.Command(path, "-version").Output()
	return strings.TrimSpace(string(out)), err
}
