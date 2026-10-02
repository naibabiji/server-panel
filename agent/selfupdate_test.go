package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/naibabiji/server-panel/releasecheck"
)

func testUpdatePaths(t *testing.T) updatePaths {
	t.Helper()
	dir := t.TempDir()
	p := updatePaths{
		binary:    filepath.Join(dir, "bin", "server-panel-agent"),
		stateDir:  filepath.Join(dir, "state"),
		resultDir: filepath.Join(dir, "result"),
		unitPath:  filepath.Join(dir, "update.path"),
	}
	for _, d := range []string{filepath.Dir(p.binary), p.stateDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, p.unitPath, "")
	mustWrite(t, p.binary, "old-binary")
	return p
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func withVersion(t *testing.T, v string) {
	t.Helper()
	old := Version
	Version = v
	t.Cleanup(func() { Version = old })
}

// fakeRelease serves a release whose checksums list newBinary; restart
// simulates the restarted Agent writing report-ok when healthy is true.
func fakeUpdater(t *testing.T, p updatePaths, target, newBinary string, healthy bool) (*selfUpdater, *[]string) {
	t.Helper()
	arch := runtime.GOARCH
	sum := sha256.Sum256([]byte(newBinary))
	checksums := hex.EncodeToString(sum[:]) + "  server-panel-agent-linux-" + arch + "\n"
	files := map[string]string{
		"checksums-" + arch + ".txt":       checksums,
		"checksums-" + arch + ".txt.sig":   "good-sig",
		"server-panel-agent-linux-" + arch: newBinary,
	}
	var fetched []string
	u := &selfUpdater{
		paths: p,
		proxy: "https://proxy.example/",
		fetch: func(url string) ([]byte, error) {
			fetched = append(fetched, url)
			name := url[strings.LastIndex(url, "/")+1:]
			if body, ok := files[name]; ok {
				return []byte(body), nil
			}
			return nil, errors.New("404")
		},
		restart: func() error {
			data, _ := os.ReadFile(p.binary)
			if healthy && string(data) == newBinary {
				mustWrite(t, p.reportOKFile(), target)
			}
			return nil
		},
		checkVersion: func(string) (string, error) { return target, nil },
		verify: func(_, sig []byte) error {
			if string(sig) != "good-sig" {
				return errors.New("bad signature")
			}
			return nil
		},
	}
	return u, &fetched
}

func TestSelfUpdateInstallsVerifiedReleaseThroughProxy(t *testing.T) {
	withVersion(t, "v1.4.9")
	p := testUpdatePaths(t)
	mustWrite(t, p.requestFile(), "v1.5.0\n")
	u, fetched := fakeUpdater(t, p, "v1.5.0", "new-binary", true)

	if err := u.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if data, _ := os.ReadFile(p.binary); string(data) != "new-binary" {
		t.Fatalf("binary = %q, want new-binary", data)
	}
	if data, _ := os.ReadFile(p.binary + ".bak"); string(data) != "old-binary" {
		t.Fatalf("backup = %q, want old-binary kept for manual rollback", data)
	}
	if _, err := os.Stat(p.requestFile()); !os.IsNotExist(err) {
		t.Fatalf("request file not consumed: %v", err)
	}
	if r, ok := p.readResult(); !ok || !r.OK || r.Version != "v1.5.0" {
		t.Fatalf("result = %+v (ok=%v), want success for v1.5.0", r, ok)
	}
	want := "https://proxy.example/https://github.com/naibabiji/server-panel/releases/download/v1.5.0/"
	if !strings.HasPrefix((*fetched)[0], want) {
		t.Fatalf("first fetch %q, want prefix %q", (*fetched)[0], want)
	}
}

func TestSelfUpdateRollsBackWhenNewVersionNeverReports(t *testing.T) {
	withVersion(t, "v1.4.9")
	p := testUpdatePaths(t)
	u, _ := fakeUpdater(t, p, "v1.5.0", "new-binary", false)

	err := u.installWithTimeout("v1.5.0", 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("err = %v, want rollback", err)
	}
	if data, _ := os.ReadFile(p.binary); string(data) != "old-binary" {
		t.Fatalf("binary = %q, want old-binary restored", data)
	}
}

func TestSelfUpdateRejectsBadSignatureAndHash(t *testing.T) {
	withVersion(t, "v1.4.9")
	p := testUpdatePaths(t)

	u, _ := fakeUpdater(t, p, "v1.5.0", "new-binary", true)
	u.verify = releasecheck.VerifySignatureFile // real key: fake signature must fail
	mustWrite(t, p.requestFile(), "v1.5.0")
	if err := u.run(); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("err = %v, want signature failure", err)
	}

	u, _ = fakeUpdater(t, p, "v1.5.0", "new-binary", true)
	inner := u.fetch
	u.fetch = func(url string) ([]byte, error) {
		if strings.HasSuffix(url, "server-panel-agent-linux-"+runtime.GOARCH) {
			return []byte("tampered"), nil
		}
		return inner(url)
	}
	mustWrite(t, p.requestFile(), "v1.5.0")
	if err := u.run(); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("err = %v, want hash mismatch", err)
	}
	if data, _ := os.ReadFile(p.binary); string(data) != "old-binary" {
		t.Fatalf("binary changed to %q after rejected update", data)
	}
}

func TestSelfUpdateIgnoresInvalidOrNonNewerRequests(t *testing.T) {
	withVersion(t, "v1.5.0")
	p := testUpdatePaths(t)
	u, fetched := fakeUpdater(t, p, "v1.5.0", "new-binary", true)

	for _, req := range []string{"v1.5.0", "v1.4.0", "../../etc", "v1.6.0-rc.1"} {
		mustWrite(t, p.requestFile(), req)
		_ = u.run()
		if _, err := os.Stat(p.requestFile()); !os.IsNotExist(err) {
			t.Fatalf("request %q not consumed", req)
		}
	}
	if len(*fetched) != 0 {
		t.Fatalf("fetched %v for invalid/non-newer requests", *fetched)
	}

	// A symlinked request must not be followed.
	target := filepath.Join(t.TempDir(), "elsewhere")
	mustWrite(t, target, "v1.6.0")
	if err := os.Symlink(target, p.requestFile()); err != nil {
		t.Fatal(err)
	}
	if err := u.run(); err == nil {
		t.Fatal("symlinked request accepted")
	}
}

func TestRequestUpdateHonorsCooldownAndReportsError(t *testing.T) {
	withVersion(t, "v1.4.9")
	p := testUpdatePaths(t)
	u := &selfUpdater{paths: p}
	now := time.Now()

	u.writeResult(updateResult{Version: "v1.5.0", Error: "download failed", Time: now.Add(-time.Hour)})
	p.requestUpdate("v1.5.0", now)
	if _, err := os.Stat(p.requestFile()); !os.IsNotExist(err) {
		t.Fatal("request written during cooldown")
	}
	if got := p.lastUpdateError(); got != "download failed" {
		t.Fatalf("lastUpdateError = %q", got)
	}

	p.requestUpdate("v1.5.0", now.Add(updateRetryCooldown))
	if data, err := os.ReadFile(p.requestFile()); err != nil || string(data) != "v1.5.0" {
		t.Fatalf("request after cooldown = %q, %v", data, err)
	}

	// Without the updater unit, nothing is requested or advertised.
	_ = os.Remove(p.requestFile())
	_ = os.Remove(p.unitPath)
	if p.updaterInstalled() {
		t.Fatal("updaterInstalled without unit")
	}
	p.requestUpdate("v1.6.0", now)
	if _, err := os.Stat(p.requestFile()); !os.IsNotExist(err) {
		t.Fatal("request written without updater")
	}
}

func TestSelfUpdateReportsRestartFailures(t *testing.T) {
	withVersion(t, "v1.4.9")

	// New version fails to start; previous binary restarts fine.
	p := testUpdatePaths(t)
	u, _ := fakeUpdater(t, p, "v1.5.0", "new-binary", true)
	restarts := 0
	u.restart = func() error {
		restarts++
		if restarts == 1 {
			return errors.New("unit failed")
		}
		return nil
	}
	err := u.installWithTimeout("v1.5.0", 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "restarting new version failed") || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("err = %v, want start failure + rollback", err)
	}
	if data, _ := os.ReadFile(p.binary); string(data) != "old-binary" {
		t.Fatalf("binary = %q, want old-binary restored", data)
	}

	// Rollback restart also fails: must not claim a successful rollback.
	p = testUpdatePaths(t)
	u, _ = fakeUpdater(t, p, "v1.5.0", "new-binary", false)
	u.restart = func() error { return errors.New("systemctl failed") }
	err = u.installWithTimeout("v1.5.0", 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "restarting it failed") || strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("err = %v, want rollback-restart failure reported", err)
	}
	if data, _ := os.ReadFile(p.binary); string(data) != "old-binary" {
		t.Fatalf("binary = %q, want old-binary restored on disk", data)
	}
}

func TestReadSmallRegularFileRejectsHardLinks(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	mustWrite(t, secret, "v1.6.0")
	link := filepath.Join(dir, "update-request")
	if err := os.Link(secret, link); err != nil {
		t.Skipf("hard links unsupported: %v", err)
	}
	if _, err := readSmallRegularFile(link, 64); err == nil {
		t.Fatal("hard-linked file accepted")
	}
}

func TestRequestUpdateIgnoresFutureResultTimestamp(t *testing.T) {
	withVersion(t, "v1.4.9")
	p := testUpdatePaths(t)
	u := &selfUpdater{paths: p}
	now := time.Now()
	// The clock moved backwards after a failure was recorded.
	u.writeResult(updateResult{Version: "v1.5.0", Error: "x", Time: now.Add(48 * time.Hour)})
	p.requestUpdate("v1.5.0", now)
	if _, err := os.Stat(p.requestFile()); err != nil {
		t.Fatalf("request not written after clock moved back: %v", err)
	}
}
