package executor

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var fileManagerRootsMu sync.Mutex

const (
	fileManagerRootsSetting  = "file_manager_custom_roots"
	maxManagedArchiveEntries = 100000
	maxManagedExtractBytes   = int64(10 * 1024 * 1024 * 1024)
)

type FileRoot struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Source string `json:"source"`
}

type FileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime string `json:"mod_time"`
}

func ListFileRoots(db *sql.DB, dataDir string) ([]FileRoot, error) {
	byPath := map[string]FileRoot{}
	if dataDir != "" {
		backupPath := filepath.Join(dataDir, "backups")
		if info, err := os.Stat(backupPath); err == nil && info.IsDir() {
			byPath[backupPath] = FileRoot{Path: backupPath, Name: "面板备份", Source: "panel"}
		}
	}
	if devices, err := ListStorageDevices(); err == nil {
		for _, device := range devices {
			for _, mountPoint := range device.Mountpoints {
				if strings.HasPrefix(mountPoint, "/mnt/") {
					byPath[mountPoint] = FileRoot{Path: mountPoint, Name: filepath.Base(mountPoint), Source: "mounted"}
				}
			}
		}
	}
	custom, err := loadCustomFileRoots(db)
	if err != nil {
		return nil, err
	}
	for _, root := range custom {
		if _, exists := byPath[root.Path]; !exists {
			byPath[root.Path] = root
		}
	}
	result := make([]FileRoot, 0, len(byPath))
	for _, root := range byPath {
		result = append(result, root)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func AddCustomFileRoot(db *sql.DB, path, name, dataDir string) (FileRoot, error) {
	clean, err := validateCustomFileRoot(path, dataDir)
	if err != nil {
		return FileRoot{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(clean)
	}
	root := FileRoot{Path: clean, Name: strings.TrimSpace(name), Source: "custom"}
	if err := addCustomFileRootRecord(db, root); err != nil {
		return FileRoot{}, err
	}
	return root, nil
}

func addCustomFileRootRecord(db *sql.DB, root FileRoot) error {
	fileManagerRootsMu.Lock()
	defer fileManagerRootsMu.Unlock()
	roots, err := loadCustomFileRoots(db)
	if err != nil {
		return err
	}
	for _, existing := range roots {
		if existing.Path == root.Path {
			return errors.New("该目录已经添加")
		}
	}
	roots = append(roots, root)
	return saveCustomFileRoots(db, roots)
}

func RemoveCustomFileRoot(db *sql.DB, path string) error {
	fileManagerRootsMu.Lock()
	defer fileManagerRootsMu.Unlock()
	roots, err := loadCustomFileRoots(db)
	if err != nil {
		return err
	}
	clean := filepath.Clean(path)
	result := roots[:0]
	found := false
	for _, root := range roots {
		if root.Path == clean {
			found = true
			continue
		}
		result = append(result, root)
	}
	if !found {
		return errors.New("自定义目录不存在")
	}
	return saveCustomFileRoots(db, result)
}

func ListManagedFiles(db *sql.DB, dataDir, rootPath, relativePath string) ([]FileEntry, error) {
	target, root, err := resolveManagedFilePath(db, dataDir, rootPath, relativePath, false)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return nil, errors.New("目录不存在")
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, err
	}
	result := make([]FileEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == ".ssh" {
			continue
		}
		entryPath := filepath.Join(target, entry.Name())
		resolvedEntry, resolveErr := filepath.EvalSymlinks(entryPath)
		if resolveErr != nil || containsPathComponent(resolvedEntry, ".ssh") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, filepath.Join(target, entry.Name()))
		result = append(result, FileEntry{Name: entry.Name(), Path: "/" + filepath.ToSlash(rel), IsDir: entry.IsDir(), Size: info.Size(), Mode: info.Mode().String(), ModTime: info.ModTime().Format(time.RFC3339)})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result, nil
}

func ManagedFilePath(db *sql.DB, dataDir, rootPath, relativePath string, allowMissing bool) (string, error) {
	target, _, err := resolveManagedFilePath(db, dataDir, rootPath, relativePath, allowMissing)
	return target, err
}

func CreateManagedDirectory(db *sql.DB, dataDir, rootPath, relativePath, name string) error {
	if name != filepath.Base(name) || name == "." || name == ".." || strings.TrimSpace(name) == "" {
		return errors.New("目录名称无效")
	}
	parent, err := ManagedFilePath(db, dataDir, rootPath, relativePath, false)
	if err != nil {
		return err
	}
	_, _, err = resolveManagedFilePath(db, dataDir, rootPath, filepath.Join(relativePath, name), true)
	if err != nil {
		return err
	}
	return os.Mkdir(filepath.Join(parent, name), 0755)
}

func RenameManagedFile(db *sql.DB, dataDir, rootPath, relativePath, newName string) error {
	if newName != filepath.Base(newName) || newName == "." || newName == ".." || strings.TrimSpace(newName) == "" {
		return errors.New("新名称无效")
	}
	source, _, err := resolveManagedFilePath(db, dataDir, rootPath, relativePath, false)
	if err != nil {
		return err
	}
	if filepath.Clean(relativePath) == "/" || filepath.Clean(relativePath) == "." {
		return errors.New("不能重命名管理根目录")
	}
	destRel := filepath.Join(filepath.Dir(relativePath), newName)
	dest, _, err := resolveManagedFilePath(db, dataDir, rootPath, destRel, true)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dest); err == nil {
		return errors.New("目标名称已经存在")
	}
	return os.Rename(source, dest)
}

func DeleteManagedFile(db *sql.DB, dataDir, rootPath, relativePath string) error {
	target, _, err := resolveManagedFilePath(db, dataDir, rootPath, relativePath, false)
	if err != nil {
		return err
	}
	if filepath.Clean(relativePath) == "/" || filepath.Clean(relativePath) == "." {
		return errors.New("不能删除管理根目录")
	}
	return os.RemoveAll(target)
}

func CopyManagedFile(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("不支持复制符号链接")
	}
	if info.IsDir() {
		if err := os.Mkdir(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := CopyManagedFile(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		out.Close()
		if !ok {
			os.Remove(dst)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	ok = true
	return out.Close()
}

func CompressManagedFile(db *sql.DB, dataDir, rootPath, relativePath string) (string, error) {
	if isManagedRootRelativePath(relativePath) {
		return "", errors.New("不能压缩整个管理根目录，请选择其中的文件或子目录")
	}
	source, _, err := resolveManagedFilePath(db, dataDir, rootPath, relativePath, false)
	if err != nil {
		return "", err
	}
	destRel := filepath.Clean(relativePath) + ".zip"
	dest, _, err := resolveManagedFilePath(db, dataDir, rootPath, destRel, true)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(dest); err == nil {
		return "", errors.New("同名压缩包已经存在")
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(out)
	ok := false
	defer func() {
		zw.Close()
		out.Close()
		if !ok {
			os.Remove(dest)
		}
	}()
	baseParent := filepath.Dir(source)
	entries := 0
	err = filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > maxManagedArchiveEntries {
			return errors.New("文件数量过多，已停止压缩")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("压缩内容包含符号链接，已拒绝操作")
		}
		rel, err := filepath.Rel(baseParent, path)
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			header.Name += "/"
			_, err = zw.CreateHeader(header)
			return err
		}
		header.Method = zip.Deflate
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, in)
		closeErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	ok = true
	return dest, nil
}

func ExtractManagedZip(db *sql.DB, dataDir, rootPath, relativePath string) (string, error) {
	archivePath, _, err := resolveManagedFilePath(db, dataDir, rootPath, relativePath, false)
	if err != nil {
		return "", err
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", errors.New("不是有效的 ZIP 压缩包")
	}
	defer reader.Close()
	if len(reader.File) > maxManagedArchiveEntries {
		return "", errors.New("压缩包文件数量过多")
	}
	dirName := strings.TrimSuffix(filepath.Base(archivePath), filepath.Ext(archivePath))
	if dirName == "" {
		dirName = "extracted"
	}
	destRel := filepath.Join(filepath.Dir(relativePath), dirName)
	dest, _, err := resolveManagedFilePath(db, dataDir, rootPath, destRel, true)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(dest); err == nil {
		return "", errors.New("解压目标目录已经存在")
	}
	var total int64
	for _, file := range reader.File {
		if file.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("压缩包包含符号链接，已拒绝解压")
		}
		if file.UncompressedSize64 > uint64(maxManagedExtractBytes-total) {
			return "", errors.New("压缩包解压后超过 10GB 限制")
		}
		total += int64(file.UncompressedSize64)
		cleanName := filepath.Clean(filepath.FromSlash(file.Name))
		if cleanName == "." || filepath.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(os.PathSeparator)) {
			return "", errors.New("压缩包包含不安全路径")
		}
		target := filepath.Join(dest, cleanName)
		if target != dest && !strings.HasPrefix(target, dest+string(os.PathSeparator)) {
			return "", errors.New("压缩包路径超出目标目录")
		}
	}
	if err := os.Mkdir(dest, 0755); err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dest)
		}
	}()
	for _, file := range reader.File {
		target := filepath.Join(dest, filepath.Clean(filepath.FromSlash(file.Name)))
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, file.Mode().Perm()); err != nil {
				return "", err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return "", err
		}
		in, err := file.Open()
		if err != nil {
			return "", err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, file.Mode().Perm())
		if err != nil {
			in.Close()
			return "", err
		}
		_, copyErr := io.Copy(out, in)
		in.Close()
		out.Close()
		if copyErr != nil {
			return "", copyErr
		}
	}
	ok = true
	return dest, nil
}

func validateCustomFileRoot(path, dataDir string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "/" {
		return "", errors.New("普通文件管理不能添加服务器根目录 /。建议按用途添加 /var/www、/home/<用户>、/srv/files 或 /mnt/<数据盘>；如需修改系统配置，请使用 SSH/SFTP 并提前备份")
	}
	allowed := []string{"/mnt", "/home", "/srv", "/var/www", "/opt"}
	ok := false
	for _, prefix := range allowed {
		if clean == prefix || strings.HasPrefix(clean, prefix+string(os.PathSeparator)) {
			ok = true
			break
		}
	}
	if !ok {
		return "", errors.New("为保护系统，仅支持添加 /mnt、/home、/srv、/var/www 或 /opt 下的目录；系统配置请使用 SSH/SFTP 管理")
	}
	if containsPathComponent(clean, ".ssh") {
		return "", errors.New("SSH 密钥目录不能加入网页文件管理，请使用 SSH/SFTP 管理")
	}
	if dataDir != "" && (clean == dataDir || strings.HasPrefix(clean, dataDir+string(os.PathSeparator))) {
		return "", errors.New("不能添加面板配置、数据库和密钥所在目录")
	}
	info, err := os.Lstat(clean)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("目录不存在、不是目录或本身是符号链接")
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil || resolved != clean {
		return "", errors.New("自定义根目录不能包含符号链接跳转")
	}
	return clean, nil
}

func resolveManagedFilePath(db *sql.DB, dataDir, rootPath, relativePath string, allowMissing bool) (string, string, error) {
	roots, err := ListFileRoots(db, dataDir)
	if err != nil {
		return "", "", err
	}
	cleanRoot := filepath.Clean(rootPath)
	authorized := false
	for _, root := range roots {
		if root.Path == cleanRoot {
			authorized = true
			break
		}
	}
	if !authorized {
		return "", "", errors.New("目录未被授权管理")
	}
	resolvedRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return "", "", errors.New("管理根目录不存在")
	}
	rel := strings.TrimPrefix(filepath.Clean("/"+relativePath), "/")
	if containsPathComponent(rel, ".ssh") {
		return "", "", errors.New("SSH 密钥目录不允许通过网页文件管理访问")
	}
	target := filepath.Join(resolvedRoot, rel)
	checkPath := target
	if allowMissing {
		checkPath = filepath.Dir(target)
	}
	resolved, err := filepath.EvalSymlinks(checkPath)
	if err != nil {
		return "", "", err
	}
	if resolved != resolvedRoot && !strings.HasPrefix(resolved, resolvedRoot+string(os.PathSeparator)) {
		return "", "", errors.New("路径通过符号链接超出了授权目录")
	}
	if containsPathComponent(resolved, ".ssh") {
		return "", "", errors.New("SSH 密钥目录不允许通过网页文件管理访问")
	}
	if dataDir != "" {
		resolvedDataDir, dataErr := filepath.EvalSymlinks(dataDir)
		if dataErr == nil && (resolved == resolvedDataDir || strings.HasPrefix(resolved, resolvedDataDir+string(os.PathSeparator))) && resolved != filepath.Join(resolvedDataDir, "backups") && !strings.HasPrefix(resolved, filepath.Join(resolvedDataDir, "backups")+string(os.PathSeparator)) {
			return "", "", errors.New("面板配置、数据库和密钥目录不允许通过文件管理访问")
		}
	}
	// Existing targets use the already-resolved path for the actual operation.
	// For a new target, rebuild only its final name beneath the resolved parent.
	if allowMissing {
		target = filepath.Join(resolved, filepath.Base(target))
	} else {
		target = resolved
	}
	return target, resolvedRoot, nil
}

func isManagedRootRelativePath(path string) bool {
	clean := filepath.Clean(path)
	return clean == "/" || clean == "."
}

func containsPathComponent(path, component string) bool {
	for _, part := range strings.Split(filepath.Clean(path), string(os.PathSeparator)) {
		if part == component {
			return true
		}
	}
	return false
}

func loadCustomFileRoots(db *sql.DB) ([]FileRoot, error) {
	var raw string
	err := db.QueryRow("SELECT svalue FROM settings WHERE skey = ?", fileManagerRootsSetting).Scan(&raw)
	if err == sql.ErrNoRows || strings.TrimSpace(raw) == "" {
		return []FileRoot{}, nil
	}
	if err != nil {
		return nil, err
	}
	var roots []FileRoot
	if err := json.Unmarshal([]byte(raw), &roots); err != nil {
		return nil, fmt.Errorf("解析自定义目录设置失败: %w", err)
	}
	return roots, nil
}

func saveCustomFileRoots(db *sql.DB, roots []FileRoot) error {
	data, err := json.Marshal(roots)
	if err != nil {
		return err
	}
	_, err = db.Exec("INSERT INTO settings (skey, svalue) VALUES (?, ?) ON CONFLICT(skey) DO UPDATE SET svalue=excluded.svalue, updated_at=CURRENT_TIMESTAMP", fileManagerRootsSetting, string(data))
	return err
}
