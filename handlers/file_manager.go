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
	"github.com/naibabiji/server-panel/i18n"
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
	var req struct{ Path, Name string }
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.files.invalid_params")))
		return
	}
	root, err := executor.AddCustomFileRoot(h.DB, req.Path, req.Name, h.dataDir())
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	executor.RecordOperationLog("file_root_add", root.Path, "success", i18n.TE(c.Request, "errors.files.log_add_root"))
	c.JSON(http.StatusOK, models.SuccessResponse(root))
}

func (h *FileManagerHandler) RemoveRoot(c *gin.Context) {
	path := c.Query("root")
	if err := executor.RemoveCustomFileRoot(h.DB, path); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	executor.RecordOperationLog("file_root_remove", path, "success", i18n.TE(c.Request, "errors.files.log_remove_root"))
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": i18n.TE(c.Request, "errors.files.root_removed_note")}))
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
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.files.download_regular_only")))
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(target)))
	c.File(target)
}

func (h *FileManagerHandler) Upload(c *gin.Context) {
	root, path := c.PostForm("root"), c.DefaultPostForm("path", "/")
	header, err := c.FormFile("file")
	if err != nil || filepath.Base(header.Filename) != header.Filename {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.files.invalid_upload")))
		return
	}
	dest, err := executor.ManagedFilePath(h.DB, h.dataDir(), root, filepath.Join(path, header.Filename), true)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	if _, err := os.Lstat(dest); err == nil {
		c.JSON(http.StatusConflict, models.ErrorResponse(i18n.TE(c.Request, "errors.files.name_exists")))
		return
	}
	if err := c.SaveUploadedFile(header, dest); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.files.upload_failed", i18n.P{"error": err.Error()})))
		return
	}
	_ = os.Chmod(dest, 0644)
	executor.RecordOperationLog("file_upload", dest, "success", i18n.TE(c.Request, "errors.files.uploaded_bytes", i18n.P{"bytes": fmt.Sprintf("%d", header.Size)}))
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": i18n.TE(c.Request, "errors.files.upload_done")}))
}

func (h *FileManagerHandler) Mkdir(c *gin.Context) {
	var req struct {
		Root string `json:"root"`
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.files.invalid_params")))
		return
	}
	if err := executor.CreateManagedDirectory(h.DB, h.dataDir(), req.Root, req.Path, req.Name); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	executor.RecordOperationLog("file_mkdir", filepath.Join(req.Root, req.Path, req.Name), "success", i18n.TE(c.Request, "errors.files.log_mkdir"))
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": i18n.TE(c.Request, "errors.files.mkdir_done")}))
}

func (h *FileManagerHandler) Rename(c *gin.Context) {
	var req struct {
		Root    string `json:"root"`
		Path    string `json:"path"`
		NewName string `json:"new_name"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.files.invalid_params")))
		return
	}
	if err := executor.RenameManagedFile(h.DB, h.dataDir(), req.Root, req.Path, req.NewName); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	executor.RecordOperationLog("file_rename", filepath.Join(req.Root, req.Path), "success", i18n.TE(c.Request, "errors.files.log_renamed_to", i18n.P{"name": req.NewName}))
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": i18n.TE(c.Request, "errors.files.rename_done")}))
}

func (h *FileManagerHandler) Delete(c *gin.Context) {
	root, path := c.Query("root"), c.Query("path")
	if err := executor.DeleteManagedFile(h.DB, h.dataDir(), root, path); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	executor.RecordOperationLog("file_delete", filepath.Join(root, path), "success", i18n.TE(c.Request, "errors.files.log_deleted"))
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": i18n.TE(c.Request, "errors.files.delete_done")}))
}

func (h *FileManagerHandler) Transfer(c *gin.Context) {
	var req struct {
		Root        string `json:"root"`
		Source      string `json:"source"`
		Destination string `json:"destination"`
		Move        bool   `json:"move"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.files.invalid_params")))
		return
	}
	if filepath.Clean(req.Source) == "/" || filepath.Clean(req.Source) == "." {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.files.cannot_copy_root")))
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
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.files.cannot_copy_into_self")))
		return
	}
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		c.JSON(http.StatusConflict, models.ErrorResponse(i18n.TE(c.Request, "errors.files.target_name_exists")))
		return
	}
	if req.Move {
		err = os.Rename(src, dst)
	} else {
		err = executor.CopyManagedFile(src, dst)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.files.operation_failed", i18n.P{"error": err.Error()})))
		return
	}
	actionDone := i18n.TE(c.Request, "errors.files.copy_done")
	if req.Move {
		actionDone = i18n.TE(c.Request, "errors.files.move_done")
	}
	executor.RecordOperationLog("file_transfer", src, "success", actionDone+" -> "+dst)
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": actionDone}))
}

func (h *FileManagerHandler) Compress(c *gin.Context) {
	var req struct {
		Root string `json:"root"`
		Path string `json:"path"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.files.invalid_params")))
		return
	}
	dest, err := executor.CompressManagedFile(h.DB, h.dataDir(), req.Root, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	executor.RecordOperationLog("file_compress", filepath.Join(req.Root, req.Path), "success", i18n.TE(c.Request, "errors.files.log_created", i18n.P{"path": dest}))
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": i18n.TE(c.Request, "errors.files.compress_done")}))
}

func (h *FileManagerHandler) Extract(c *gin.Context) {
	var req struct {
		Root string `json:"root"`
		Path string `json:"path"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.files.invalid_params")))
		return
	}
	dest, err := executor.ExtractManagedZip(h.DB, h.dataDir(), req.Root, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	executor.RecordOperationLog("file_extract", filepath.Join(req.Root, req.Path), "success", i18n.TE(c.Request, "errors.files.log_extracted_to", i18n.P{"path": dest}))
	c.JSON(http.StatusOK, models.SuccessResponse(gin.H{"message": i18n.TE(c.Request, "errors.files.extract_done")}))
}
