package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/executor"
	"github.com/naibabiji/server-panel/middleware"
	"github.com/naibabiji/server-panel/models"
)

type FileManagerHandler struct{ DB *sql.DB }

func (h *FileManagerHandler) dataDir() string {
	if config.AppConfig != nil && config.AppConfig.Panel.DataDir != "" {
		return config.AppConfig.Panel.DataDir
	}
	return "/www/server/server-panel"
}

func (h *FileManagerHandler) Roots(c *gin.Context) {
	roots, err := executor.ListFileRoots(h.DB, h.dataDir())
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(roots))
}

func (h *FileManagerHandler) AddRoot(c *gin.Context) {
	if !h.requireWriteToken(c) {
		return
	}
	var req struct{ Path, Name string }
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("请求参数无效"))
		return
	}
	root, err := executor.AddCustomFileRoot(h.DB, req.Path, req.Name, h.dataDir())
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	executor.RecordOperationLog("file_root_add", root.Path, "success", "添加文件管理目录")
	c.JSON(http.StatusOK, models.SuccessResponse(root))
}

func (h *FileManagerHandler) RemoveRoot(c *gin.Context) {
	if !h.requireWriteToken(c) {
		return
	}
	path := c.Query("root")
	if err := executor.RemoveCustomFileRoot(h.DB, path); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	executor.RecordOperationLog("file_root_remove", path, "success", "移除文件管理目录（未删除目录文件）")
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": "已移除管理入口，服务器文件没有被删除"}))
}

func (h *FileManagerHandler) List(c *gin.Context) {
	root, path := c.Query("root"), c.DefaultQuery("path", "/")
	entries, err := executor.ListManagedFiles(h.DB, h.dataDir(), root, path)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(entries))
}

func (h *FileManagerHandler) Download(c *gin.Context) {
	target, err := executor.ManagedFilePath(h.DB, h.dataDir(), c.Query("root"), c.Query("path"), false)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("只能下载普通文件"))
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(target)))
	c.File(target)
}

func (h *FileManagerHandler) Upload(c *gin.Context) {
	if !h.requireWriteToken(c) {
		return
	}
	root, path := c.PostForm("root"), c.DefaultPostForm("path", "/")
	header, err := c.FormFile("file")
	if err != nil || filepath.Base(header.Filename) != header.Filename {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("上传文件无效"))
		return
	}
	dest, err := executor.ManagedFilePath(h.DB, h.dataDir(), root, filepath.Join(path, header.Filename), true)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	if _, err := os.Lstat(dest); err == nil {
		c.JSON(http.StatusConflict, models.ErrorResponse("同名文件已经存在"))
		return
	}
	if err := c.SaveUploadedFile(header, dest); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("上传失败: "+err.Error()))
		return
	}
	_ = os.Chmod(dest, 0644)
	executor.RecordOperationLog("file_upload", dest, "success", fmt.Sprintf("上传 %d 字节", header.Size))
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": "文件上传完成"}))
}

func (h *FileManagerHandler) Mkdir(c *gin.Context) {
	if !h.requireWriteToken(c) {
		return
	}
	var req struct {
		Root string `json:"root"`
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("请求参数无效"))
		return
	}
	if err := executor.CreateManagedDirectory(h.DB, h.dataDir(), req.Root, req.Path, req.Name); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	executor.RecordOperationLog("file_mkdir", filepath.Join(req.Root, req.Path, req.Name), "success", "新建目录")
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": "目录已创建"}))
}

func (h *FileManagerHandler) Rename(c *gin.Context) {
	if !h.requireWriteToken(c) {
		return
	}
	var req struct {
		Root    string `json:"root"`
		Path    string `json:"path"`
		NewName string `json:"new_name"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("请求参数无效"))
		return
	}
	if err := executor.RenameManagedFile(h.DB, h.dataDir(), req.Root, req.Path, req.NewName); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	executor.RecordOperationLog("file_rename", filepath.Join(req.Root, req.Path), "success", "重命名为 "+req.NewName)
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": "重命名完成"}))
}

func (h *FileManagerHandler) Delete(c *gin.Context) {
	if !h.requireWriteToken(c) {
		return
	}
	root, path := c.Query("root"), c.Query("path")
	if err := executor.DeleteManagedFile(h.DB, h.dataDir(), root, path); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	executor.RecordOperationLog("file_delete", filepath.Join(root, path), "success", "删除文件或目录")
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": "删除完成"}))
}

func (h *FileManagerHandler) Transfer(c *gin.Context) {
	if !h.requireWriteToken(c) {
		return
	}
	var req struct {
		Root        string `json:"root"`
		Source      string `json:"source"`
		Destination string `json:"destination"`
		Move        bool   `json:"move"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("请求参数无效"))
		return
	}
	if filepath.Clean(req.Source) == "/" || filepath.Clean(req.Source) == "." {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("不能复制或移动管理根目录"))
		return
	}
	src, err := executor.ManagedFilePath(h.DB, h.dataDir(), req.Root, req.Source, false)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	dst, err := executor.ManagedFilePath(h.DB, h.dataDir(), req.Root, filepath.Join(req.Destination, filepath.Base(src)), true)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	if info, statErr := os.Stat(src); statErr == nil && info.IsDir() && (dst == src || strings.HasPrefix(dst, src+string(os.PathSeparator))) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("不能将目录复制或移动到它自己的子目录"))
		return
	}
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		c.JSON(http.StatusConflict, models.ErrorResponse("目标中存在同名项目"))
		return
	}
	if req.Move {
		err = os.Rename(src, dst)
	} else {
		err = executor.CopyManagedFile(src, dst)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("操作失败: "+err.Error()))
		return
	}
	action := "复制"
	if req.Move {
		action = "移动"
	}
	executor.RecordOperationLog("file_transfer", src, "success", action+"到 "+dst)
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": action + "完成"}))
}

func (h *FileManagerHandler) Compress(c *gin.Context) {
	if !h.requireWriteToken(c) {
		return
	}
	var req struct {
		Root string `json:"root"`
		Path string `json:"path"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("请求参数无效"))
		return
	}
	dest, err := executor.CompressManagedFile(h.DB, h.dataDir(), req.Root, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	executor.RecordOperationLog("file_compress", filepath.Join(req.Root, req.Path), "success", "创建 "+dest)
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": "压缩完成"}))
}

func (h *FileManagerHandler) Extract(c *gin.Context) {
	if !h.requireWriteToken(c) {
		return
	}
	var req struct {
		Root string `json:"root"`
		Path string `json:"path"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("请求参数无效"))
		return
	}
	dest, err := executor.ExtractManagedZip(h.DB, h.dataDir(), req.Root, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	executor.RecordOperationLog("file_extract", filepath.Join(req.Root, req.Path), "success", "解压到 "+dest)
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": "解压完成"}))
}

func (h *FileManagerHandler) requireWriteToken(c *gin.Context) bool {
	sessionToken, ok := getSessionToken(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse("会话已过期，请重新登录"))
		return false
	}
	if !ConsumeViewToken(c.GetHeader("X-View-Token"), sessionToken, middleware.ClientIP(c)) {
		c.JSON(http.StatusForbidden, models.ErrorResponse("文件写操作需要重新输入查看密码"))
		return false
	}
	return true
}
