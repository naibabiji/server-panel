package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/executor"
	"github.com/naibabiji/server-panel/models"
)

type systemPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Repo    string `json:"repo"`
}

type sysPkgCacheState struct {
	mu          sync.Mutex
	expireAt    time.Time
	pkgs        []systemPackage
	valid       bool
	lastErr     error
	errExpireAt time.Time
}

var sysPkgCache sysPkgCacheState
var sysUpdateMu sync.Mutex

const sysPkgCacheTTL = 5 * time.Minute
// apt 检查失败（非 Debian 主机/无权限）时缓存错误一小段时间，
// 避免每次缓存过期都重新执行 shell 命令。
const sysPkgErrTTL = 60 * time.Second
const sysUpdateOutputLimit = 1024 * 1024

type SystemUpdateHandler struct{}

// Check lists apt packages with an available upgrade (Debian/Ubuntu only).
// Results are cached for a few minutes since `apt list` is not free to run
// on every dashboard/settings page load.
func (h *SystemUpdateHandler) Check(c *gin.Context) {
	sysPkgCache.mu.Lock()
	if sysPkgCache.valid && time.Now().Before(sysPkgCache.expireAt) {
		pkgs := sysPkgCache.pkgs
		sysPkgCache.mu.Unlock()
		c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
			"packages": pkgs,
			"count":    len(pkgs),
		}))
		return
	}
	// 命中失败缓存：直接复用上次的错误，避免重复执行 shell 命令
	if sysPkgCache.lastErr != nil && time.Now().Before(sysPkgCache.errExpireAt) {
		err := sysPkgCache.lastErr
		sysPkgCache.mu.Unlock()
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("检查系统更新失败: "+err.Error()))
		return
	}
	sysPkgCache.mu.Unlock()

	pkgs, err := getUpgradablePackages()
	if err != nil {
		sysPkgCache.mu.Lock()
		sysPkgCache.lastErr = err
		sysPkgCache.errExpireAt = time.Now().Add(sysPkgErrTTL)
		sysPkgCache.mu.Unlock()
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("检查系统更新失败: "+err.Error()))
		return
	}

	sysPkgCache.mu.Lock()
	sysPkgCache.pkgs = pkgs
	sysPkgCache.expireAt = time.Now().Add(sysPkgCacheTTL)
	sysPkgCache.valid = true
	sysPkgCache.lastErr = nil
	sysPkgCache.mu.Unlock()

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"packages": pkgs,
		"count":    len(pkgs),
	}))
}

// Update runs `apt update && apt upgrade -y` directly on the host and
// returns the combined output. This is a real, unattended-package-manager
// action — the frontend must confirm with the admin before calling it.
func (h *SystemUpdateHandler) Update(c *gin.Context) {
	if !sysUpdateMu.TryLock() {
		c.JSON(http.StatusConflict, models.ErrorResponse("已有系统更新任务正在执行，请稍后再试"))
		return
	}
	defer sysUpdateMu.Unlock()

	executor.RecordOperationLog("system_update", "apt", "running", "apt update started")
	out1, err := runAptCommand("update")
	if err != nil {
		executor.RecordOperationLog("system_update", "apt", "failed", "apt update failed: "+err.Error())
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("apt update 失败: "+err.Error()+"\n"+out1))
		return
	}
	out2, err := runAptCommand("upgrade", "-y",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold")
	if err != nil {
		executor.RecordOperationLog("system_update", "apt", "failed", "apt upgrade failed: "+err.Error())
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("apt upgrade 失败: "+err.Error()+"\n"+out2))
		return
	}

	sysPkgCache.mu.Lock()
	sysPkgCache.valid = false
	sysPkgCache.mu.Unlock()

	executor.RecordOperationLog("system_update", "apt", "success", "apt update && apt upgrade -y completed")
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": "系统更新完成",
		"output":  out1 + "\n" + out2,
	}))
}

// getUpgradablePackages runs `apt list --upgradable`. Unlike a silent
// empty-list fallback, exec errors are propagated to the caller so a
// broken/non-Debian host doesn't look identical to "fully up to date".
func getUpgradablePackages() ([]systemPackage, error) {
	out, err := exec.Command("bash", "-c", "apt list --upgradable 2>/dev/null").Output()
	if err != nil {
		return nil, err
	}
	return parseAptUpgradable(string(out)), nil
}

// parseAptUpgradable parses `apt list --upgradable` output into structured
// package entries. Split out from getUpgradablePackages so it's testable
// without needing a real apt install.
func parseAptUpgradable(output string) []systemPackage {
	var pkgs []systemPackage
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Listing...") {
			continue
		}
		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 2 {
			continue
		}
		nameRepo := strings.SplitN(parts[0], "/", 2)
		name := nameRepo[0]
		repo := ""
		if len(nameRepo) > 1 {
			repo = nameRepo[1]
		}
		pkgs = append(pkgs, systemPackage{Name: name, Version: parts[1], Repo: repo})
	}
	return pkgs
}

func runAptCommand(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	cmd := exec.CommandContext(ctx, "apt", args...)
	cmd.Env = append(cmd.Environ(),
		"DEBIAN_FRONTEND=noninteractive",
		"NEEDRESTART_MODE=a",
	)
	var output limitedOutput
	output.limit = sysUpdateOutputLimit
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Run()
	out := output.String()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("apt command timed out")
	}
	return out, err
}

type limitedOutput struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	if w.limit < 0 {
		_, _ = w.buf.Write(p)
		return len(p), nil
	}
	if w.limit == 0 {
		return len(p), nil
	}
	remaining := w.limit - w.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = w.buf.Write(p[:remaining])
			w.truncated = true
			return len(p), nil
		}
		_, _ = w.buf.Write(p)
		return len(p), nil
	}
	w.truncated = true
	return len(p), nil
}

func (w *limitedOutput) String() string {
	out := w.buf.String()
	if w.truncated {
		out += "\n...[output truncated]"
	}
	return out
}
