package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/middleware"
	"github.com/naibabiji/server-panel/models"
	"golang.org/x/crypto/bcrypt"
)

var dummyBcryptHash []byte

func init() {
	dummyBcryptHash, _ = bcrypt.GenerateFromPassword([]byte("dummy_for_timing_attack_mitigation"), 12)
}

type AuthHandler struct {
	AttemptTracker *middleware.LoginAttemptTracker
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("请提供用户名和密码"))
		return
	}

	db := database.GetDB()
	var id int
	var username, passwordHash string
	err := db.QueryRow(
		"SELECT id, username, password_hash FROM admin_users WHERE username = ?",
		req.Username,
	).Scan(&id, &username, &passwordHash)

	if err != nil {
		bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(req.Password))
		if h.AttemptTracker != nil {
			h.AttemptTracker.RecordAttempt(middleware.ClientIP(c), "web_login")
		}
		c.JSON(http.StatusUnauthorized, models.ErrorResponse("用户名或密码错误"))
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)) != nil {
		if h.AttemptTracker != nil {
			h.AttemptTracker.RecordAttempt(middleware.ClientIP(c), "web_login")
		}
		c.JSON(http.StatusUnauthorized, models.ErrorResponse("用户名或密码错误"))
		return
	}

	// 登录成功：清空该 IP 的历史失败计数，避免误封真实用户。
	if h.AttemptTracker != nil {
		h.AttemptTracker.ClearAttempts(middleware.ClientIP(c))
	}

	session := middleware.GlobalSessionStore.Create(username)
	c.SetCookie("sp_session", session.Token, 1800, "/", "", middleware.IsSecureRequest(c), true)

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"username": username,
	}))
}

func (h *AuthHandler) Logout(c *gin.Context) {
	token, err := c.Cookie("sp_session")
	if err == nil && token != "" {
		middleware.GlobalSessionStore.Delete(token)
	}
	c.SetCookie("sp_session", "", -1, "/", "", middleware.IsSecureRequest(c), true)
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": "已登出",
	}))
}

func (h *AuthHandler) Check(c *gin.Context) {
	username, _ := c.Get("session_username")
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"authenticated": true,
		"username":      username,
	}))
}

func (h *AuthHandler) CSRFToken(c *gin.Context) {
	token := middleware.GetCSRFToken(c)
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"csrf_token": token,
	}))
}

func GetPanelTitle() string {
	db := database.GetDB()
	if db == nil {
		return "Server Panel"
	}
	var title string
	err := db.QueryRow("SELECT svalue FROM settings WHERE skey = 'panel_title'").Scan(&title)
	if err != nil || title == "" {
		return "Server Panel"
	}
	return title
}

func GetPanelVersion() string {
	cfg := config.AppConfig
	if cfg == nil {
		return ""
	}
	return cfg.Panel.Version
}
