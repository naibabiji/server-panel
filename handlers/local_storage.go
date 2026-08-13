package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/executor"
	"github.com/naibabiji/server-panel/models"
)

type LocalStorageHandler struct{}

func (h *LocalStorageHandler) ListDevices(c *gin.Context) {
	items, err := executor.ListStorageDevices()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取磁盘信息失败: "+err.Error()))
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
		c.JSON(http.StatusBadRequest, models.ErrorResponse("请求参数无效"))
		return
	}
	if err := executor.MountStorage(req.DevicePath, req.MountPoint); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": "数据盘已挂载并设置为开机自动挂载"}))
}

func (h *LocalStorageHandler) Unmount(c *gin.Context) {
	var req struct {
		DevicePath      string `json:"device_path"`
		RemoveAutoMount bool   `json:"remove_auto_mount"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("请求参数无效"))
		return
	}
	if err := executor.UnmountStorage(req.DevicePath, req.RemoveAutoMount); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": "数据盘已安全卸载"}))
}

func (h *LocalStorageHandler) Format(c *gin.Context) {
	var req struct {
		DevicePath   string `json:"device_path"`
		Confirmation string `json:"confirmation"`
		MountPoint   string `json:"mount_point"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("请求参数无效"))
		return
	}
	if err := executor.FormatAndMountPartition(req.DevicePath, req.Confirmation, req.MountPoint); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": "分区已格式化为 ext4，并完成挂载和开机自动挂载设置"}))
}

func (h *LocalStorageHandler) Initialize(c *gin.Context) {
	var req struct {
		DevicePath   string `json:"device_path"`
		Confirmation string `json:"confirmation"`
		MountPoint   string `json:"mount_point"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("请求参数无效"))
		return
	}
	if err := executor.InitializeAndMountDisk(req.DevicePath, req.Confirmation, req.MountPoint); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": "裸盘已初始化为 ext4，并完成挂载和开机自动挂载设置"}))
}

func (h *LocalStorageHandler) ListUsers(c *gin.Context) {
	items, err := executor.ListLocalUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取系统用户失败: "+err.Error()))
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
		c.JSON(http.StatusBadRequest, models.ErrorResponse("请求参数无效"))
		return
	}
	result, err := executor.CheckPathPermission(req.Username, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(result))
}
