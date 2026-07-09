package handlers

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/middleware"
	"github.com/naibabiji/server-panel/models"
)

type ViewPasswordHandler struct{}

func (h *ViewPasswordHandler) GetStatus(c *gin.Context) {
	db := database.GetDB()
	if db == nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取查看密码状态失败"))
		return
	}
	var hash string
	if err := db.QueryRow("SELECT svalue FROM settings WHERE skey = 'view_password_hash'").Scan(&hash); err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("read view password status failed: %v", err)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取查看密码状态失败"))
		return
	}

	isSetup := hash != ""

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]bool{
		"is_setup":    isSetup,
		"is_unlocked": false,
	}))
}

func (h *ViewPasswordHandler) Setup(c *gin.Context) {
	db := database.GetDB()

	var req struct {
		Password        string `json:"password"`
		PasswordConfirm string `json:"password_confirm"`
		Force           bool   `json:"force"`   // 破坏性重置确认
		Confirm         string `json:"confirm"` // 破坏性重置确认短语
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Password == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("密码不能为空"))
		return
	}
	if req.Password != req.PasswordConfirm {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("两次输入的查看密码不一致"))
		return
	}

	var existingHash string
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'view_password_hash'").Scan(&existingHash)
	if existingHash != "" && !req.Force {
		c.JSON(http.StatusConflict, models.ErrorResponse("查看密码已设置，请使用修改密码或重置密码"))
		return
	}

	// 重置：清空所有已保存的敏感凭据，再设置新的查看密码。
	if existingHash != "" && req.Force {
		if req.Confirm != "DELETE_SAVED_PASSWORDS" {
			c.JSON(http.StatusBadRequest, models.ErrorResponse("重置会删除所有已保存密码，请输入确认短语 DELETE_SAVED_PASSWORDS"))
			return
		}
		if _, err := clearSavedSecrets(db); err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse("清空已保存密码失败"))
			return
		}
		clearViewTokens()
	}

	hash, err := HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("密码处理失败"))
		return
	}

	db.Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES ('view_password_hash', ?)", hash)

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": "查看密码设置成功",
	}))
}

func (h *ViewPasswordHandler) Change(c *gin.Context) {
	db := database.GetDB()

	var req struct {
		OldPassword        string `json:"old_password"`
		NewPassword        string `json:"new_password"`
		NewPasswordConfirm string `json:"new_password_confirm"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.OldPassword == "" || req.NewPassword == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("旧密码和新密码不能为空"))
		return
	}
	if req.NewPassword != req.NewPasswordConfirm {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("两次输入的新查看密码不一致"))
		return
	}

	var hash string
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'view_password_hash'").Scan(&hash)
	if hash == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("查看密码尚未设置"))
		return
	}
	if !VerifyPassword(req.OldPassword, hash) {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse("旧查看密码错误"))
		return
	}

	newHash, err := HashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("密码处理失败"))
		return
	}
	tx, err := db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("修改查看密码失败"))
		return
	}
	if _, err := tx.Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES ('view_password_hash', ?)", newHash); err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("保存查看密码失败"))
		return
	}
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("修改查看密码失败"))
		return
	}
	clearViewTokens()

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": "查看密码已修改",
	}))
}

func (h *ViewPasswordHandler) Unlock(c *gin.Context) {
	db := database.GetDB()

	ip := middleware.ClientIP(c)

	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Password == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("密码不能为空"))
		return
	}

	var hash string
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'view_password_hash'").Scan(&hash)

	if hash == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("查看密码尚未设置"))
		return
	}

	if !VerifyPassword(req.Password, hash) {
		unlockAttemptsMu.Lock()
		unlockAttempts[ip]++
		if unlockAttempts[ip] >= maxUnlockAttempts {
			delete(unlockAttempts, ip)
			unlockAttemptsMu.Unlock()
			cleared, err := clearSavedSecrets(db)
			if err != nil {
				c.JSON(http.StatusInternalServerError, models.ErrorResponse("查看密码错误次数过多，清空已保存密码失败"))
				return
			}
			clearViewTokens()
			c.JSON(http.StatusForbidden, models.ErrorResponse("查看密码错误次数已达 "+strconv.Itoa(maxUnlockAttempts)+" 次，已清空 "+strconv.Itoa(cleared)+" 项已保存的服务器/网站敏感凭据"))
			return
		}
		remaining := maxUnlockAttempts - unlockAttempts[ip]
		unlockAttemptsMu.Unlock()
		c.JSON(http.StatusUnauthorized, models.ErrorResponse("密码错误，还剩 "+strconv.Itoa(remaining)+" 次尝试"))
		return
	}

	unlockAttemptsMu.Lock()
	delete(unlockAttempts, ip)
	unlockAttemptsMu.Unlock()

	sessionToken, ok := getSessionToken(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse("会话已过期，请重新登录"))
		return
	}
	token, err := CreateViewToken(sessionToken, ip)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("查看授权生成失败"))
		return
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message":    "查看密码验证成功",
		"view_token": token,
	}))
}

func (h *ViewPasswordHandler) Lock(c *gin.Context) {
	clearViewTokens()
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": "查看密码已锁定",
	}))
}

func clearSavedSecrets(db interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}) (int, error) {
	result, err := db.Exec(`UPDATE servers
		SET ssh_password_enc = '',
		    panel_password_enc = ''
		WHERE ssh_password_enc != ''
		   OR panel_password_enc != ''`)
	if err != nil {
		return 0, err
	}
	rows, _ := result.RowsAffected()
	websiteResult, err := db.Exec(`UPDATE websites
		SET panel_password_enc = ''
		WHERE panel_password_enc != ''`)
	if err != nil {
		return int(rows), err
	}
	websiteRows, _ := websiteResult.RowsAffected()
	return int(rows + websiteRows), nil
}

func clearViewTokens() {
	viewTokensMu.Lock()
	defer viewTokensMu.Unlock()
	for k := range viewTokens {
		delete(viewTokens, k)
	}
}

func getSessionToken(c *gin.Context) (string, bool) {
	token, exists := c.Get("session_token")
	if !exists {
		return "", false
	}
	s, ok := token.(string)
	return s, ok && s != ""
}
