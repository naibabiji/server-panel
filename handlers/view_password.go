package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/models"
)

type ViewPasswordHandler struct{}

func (h *ViewPasswordHandler) GetStatus(c *gin.Context) {
	db := database.GetDB()
	var hash, salt, pepper string
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'view_password_hash'").Scan(&hash)
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'view_password_salt'").Scan(&salt)
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'pepper'").Scan(&pepper)

	isSetup := hash != ""
	isUnlocked := false
	if token, exists := c.Get("session_token"); exists {
		isUnlocked = IsViewPasswordUnlocked(token.(string))
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]bool{
		"is_setup":    isSetup,
		"is_unlocked": isUnlocked,
	}))
}

func (h *ViewPasswordHandler) Setup(c *gin.Context) {
	db := database.GetDB()

	// 检查是否已设置
	var existingHash string
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'view_password_hash'").Scan(&existingHash)
	if existingHash != "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("查看密码已设置"))
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Password == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("密码不能为空"))
		return
	}
	if len(req.Password) < 8 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("密码至少需要 8 个字符"))
		return
	}

	// 生成 salt 和 pepper
	salt := make([]byte, 32)
	pepper := make([]byte, 32)
	rand.Read(salt)
	rand.Read(pepper)
	saltHex := hex.EncodeToString(salt)
	pepperHex := hex.EncodeToString(pepper)

	// bcrypt hash
	hash, err := HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("密码处理失败"))
		return
	}

	// 存入 settings
	db.Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES ('view_password_hash', ?)", hash)
	db.Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES ('view_password_salt', ?)", saltHex)
	db.Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES ('pepper', ?)", pepperHex)

	// 派生密钥并存入内存
	key, err := DeriveEncryptionKey(req.Password, saltHex, pepperHex)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("密钥派生失败"))
		return
	}

	if token, exists := c.Get("session_token"); exists {
		StoreDerivedKey(token.(string), key)
		c.Set("view_password_unlocked", true)
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": "查看密码设置成功",
	}))
}

func (h *ViewPasswordHandler) Unlock(c *gin.Context) {
	db := database.GetDB()

	// 检查是否锁定
	unlockAttemptsMu.Lock()
	ip := c.ClientIP()
	now := time.Now().Unix()
	if lockUntil, exists := unlockLockUntil[ip]; exists && now < lockUntil {
		unlockAttemptsMu.Unlock()
		c.JSON(http.StatusTooManyRequests, models.ErrorResponse("尝试次数过多，请 5 分钟后重试"))
		return
	}
	unlockAttemptsMu.Unlock()

	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Password == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("密码不能为空"))
		return
	}

	// 获取存储的 hash/salt/pepper
	var hash, saltHex, pepperHex string
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'view_password_hash'").Scan(&hash)
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'view_password_salt'").Scan(&saltHex)
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'pepper'").Scan(&pepperHex)

	if hash == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("查看密码尚未设置"))
		return
	}

	// 验证密码
	if !VerifyPassword(req.Password, hash) {
		unlockAttemptsMu.Lock()
		unlockAttempts[ip]++
		if unlockAttempts[ip] >= maxUnlockAttempts {
			unlockLockUntil[ip] = now + 300 // 锁定 5 分钟
			delete(unlockAttempts, ip)
			unlockAttemptsMu.Unlock()
			c.JSON(http.StatusTooManyRequests, models.ErrorResponse("尝试次数过多，请 5 分钟后重试"))
			return
		}
		remaining := maxUnlockAttempts - unlockAttempts[ip]
		unlockAttemptsMu.Unlock()
		c.JSON(http.StatusUnauthorized, models.ErrorResponse("密码错误，还剩 "+strconv.Itoa(remaining)+" 次尝试"))
		return
	}

	// 清除尝试记录
	unlockAttemptsMu.Lock()
	delete(unlockAttempts, ip)
	unlockAttemptsMu.Unlock()

	// 派生密钥
	key, err := DeriveEncryptionKey(req.Password, saltHex, pepperHex)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("密钥派生失败"))
		return
	}

	if token, exists := c.Get("session_token"); exists {
		StoreDerivedKey(token.(string), key)
		c.Set("view_password_unlocked", true)
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": "查看密码已解锁",
	}))
}

func (h *ViewPasswordHandler) Lock(c *gin.Context) {
	if token, exists := c.Get("session_token"); exists {
		RemoveDerivedKey(token.(string))
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": "查看密码已锁定",
	}))
}
