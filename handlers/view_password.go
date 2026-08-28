package handlers

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/i18n"
	"github.com/naibabiji/server-panel/middleware"
	"github.com/naibabiji/server-panel/models"
)

type ViewPasswordHandler struct{}

func (h *ViewPasswordHandler) GetStatus(c *gin.Context) {
	db := database.GetDB()
	if db == nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.view_password_status_failed")))
		return
	}
	var hash string
	var err error
	for i := 0; i < 3; i++ {
		err = db.QueryRow("SELECT svalue FROM settings WHERE skey = 'view_password_hash'").Scan(&hash)
		if err == nil || errors.Is(err, sql.ErrNoRows) {
			break
		}
		time.Sleep(time.Duration(100*(i+1)) * time.Millisecond)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("read view password status failed: %v", err)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.view_password_status_failed")))
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
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.password_required")))
		return
	}
	if req.Password != req.PasswordConfirm {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.mismatch")))
		return
	}

	var existingHash string
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'view_password_hash'").Scan(&existingHash)
	if existingHash != "" && !req.Force {
		c.JSON(http.StatusConflict, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.already_setup")))
		return
	}

	// 重置：清空所有已保存的敏感凭据，再设置新的查看密码。
	if existingHash != "" && req.Force {
		if req.Confirm != "DELETE_SAVED_PASSWORDS" {
			c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.reset_confirm_phrase")))
			return
		}
		if _, err := clearSavedSecrets(db); err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.clear_saved_failed")))
			return
		}
		clearViewTokens()
	}

	hash, err := HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.password_processing_failed")))
		return
	}

	db.Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES ('view_password_hash', ?)", hash)

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": i18n.TE(c.Request, "errors.vp.setup_success"),
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
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.old_new_required")))
		return
	}
	if req.NewPassword != req.NewPasswordConfirm {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.new_mismatch")))
		return
	}

	var hash string
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'view_password_hash'").Scan(&hash)
	if hash == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.not_setup")))
		return
	}
	if !VerifyPassword(req.OldPassword, hash) {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.old_wrong")))
		return
	}

	newHash, err := HashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.password_processing_failed")))
		return
	}
	tx, err := db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.change_failed")))
		return
	}
	if _, err := tx.Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES ('view_password_hash', ?)", newHash); err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.save_failed")))
		return
	}
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.change_failed")))
		return
	}
	clearViewTokens()

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": i18n.TE(c.Request, "errors.vp.changed"),
	}))
}

func (h *ViewPasswordHandler) Unlock(c *gin.Context) {
	db := database.GetDB()

	ip := middleware.ClientIP(c)

	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Password == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.password_required")))
		return
	}

	var hash string
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'view_password_hash'").Scan(&hash)

	if hash == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.not_setup")))
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
				c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.too_many_clear_failed")))
				return
			}
			clearViewTokens()
			c.JSON(http.StatusForbidden, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.too_many_attempts", i18n.P{"max": strconv.Itoa(maxUnlockAttempts), "cleared": strconv.Itoa(cleared)})))
			return
		}
		remaining := maxUnlockAttempts - unlockAttempts[ip]
		unlockAttemptsMu.Unlock()
		c.JSON(http.StatusUnauthorized, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.wrong_remaining", i18n.P{"remaining": strconv.Itoa(remaining)})))
		return
	}

	unlockAttemptsMu.Lock()
	delete(unlockAttempts, ip)
	unlockAttemptsMu.Unlock()

	sessionToken, ok := getSessionToken(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse(i18n.TE(c.Request, "session.session_expired")))
		return
	}
	token, err := CreateViewToken(sessionToken, ip)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.vp.auth_generate_failed")))
		return
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message":    i18n.TE(c.Request, "errors.vp.verify_success"),
		"view_token": token,
	}))
}

func (h *ViewPasswordHandler) Lock(c *gin.Context) {
	clearViewTokens()
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": i18n.TE(c.Request, "errors.vp.locked"),
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
