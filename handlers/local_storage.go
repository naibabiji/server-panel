package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/executor"
	"github.com/naibabiji/server-panel/i18n"
	"github.com/naibabiji/server-panel/models"
)

type LocalStorageHandler struct{}

func (h *LocalStorageHandler) ListDevices(c *gin.Context) {
	items, err := executor.ListStorageDevices()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.local_storage.read_disk_info_failed", i18n.P{"error": err.Error()})))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(items))
}

func (h *LocalStorageHandler) Mount(c *gin.Context) {
	var req struct {
		DevicePath string `json:"device_path"`
		MountPoint string `json:"mount_point"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.files.invalid_params")))
		return
	}
	if err := executor.MountStorage(req.DevicePath, req.MountPoint); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": i18n.TE(c.Request, "errors.local_storage.mounted")}))
}

func (h *LocalStorageHandler) Unmount(c *gin.Context) {
	var req struct {
		DevicePath      string `json:"device_path"`
		RemoveAutoMount bool   `json:"remove_auto_mount"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.files.invalid_params")))
		return
	}
	if err := executor.UnmountStorage(req.DevicePath, req.RemoveAutoMount); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": i18n.TE(c.Request, "errors.local_storage.unmounted")}))
}

func (h *LocalStorageHandler) Format(c *gin.Context) {
	var req struct {
		DevicePath   string `json:"device_path"`
		Confirmation string `json:"confirmation"`
		MountPoint   string `json:"mount_point"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.files.invalid_params")))
		return
	}
	if err := executor.FormatAndMountPartition(req.DevicePath, req.Confirmation, req.MountPoint); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": i18n.TE(c.Request, "errors.local_storage.partition_formatted")}))
}

func (h *LocalStorageHandler) Initialize(c *gin.Context) {
	var req struct {
		DevicePath   string `json:"device_path"`
		Confirmation string `json:"confirmation"`
		MountPoint   string `json:"mount_point"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.files.invalid_params")))
		return
	}
	if err := executor.InitializeAndMountDisk(req.DevicePath, req.Confirmation, req.MountPoint); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": i18n.TE(c.Request, "errors.local_storage.disk_initialized")}))
}

func (h *LocalStorageHandler) ListUsers(c *gin.Context) {
	items, err := executor.ListLocalUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.local_storage.read_users_failed", i18n.P{"error": err.Error()})))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(items))
}

func (h *LocalStorageHandler) CheckPermission(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Path     string `json:"path"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.files.invalid_params")))
		return
	}
	result, err := executor.CheckPathPermission(req.Username, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(result))
}
