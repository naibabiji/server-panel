package handlers

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/i18n"
	"github.com/naibabiji/server-panel/models"
)

func validateOptionalHTTPURL(c *gin.Context, value *string) bool {
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		*value = ""
		return true
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.url.http_required")))
		return false
	}
	*value = trimmed
	return true
}
