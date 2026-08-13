package executor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

const managedFstabComment = "# server-panel managed mount"

var (
	mountPointPattern = regexp.MustCompile(`^/mnt/[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	devicePathPattern = regexp.MustCompile(`^/dev/[A-Za-z0-9._/+:-]+$`)
	storageCommand    = func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).CombinedOutput()
	}
	storageMutationMu sync.Mutex
)

type StorageDevice struct {
	Name           string   `json:"name"`
	Path           string   `json:"path"`
	Parent         string   `json:"parent"`
	Size           int64    `json:"size"`
	Type           string   `json:"type"`
	FSType         string   `json:"fstype"`
	Label          string   `json:"label"`
	UUID           string   `json:"uuid"`
	Mountpoints    []string `json:"mountpoints"`
	Model          string   `json:"model"`
	Serial         string   `json:"serial"`
	ReadOnly       bool     `json:"read_only"`
	System         bool     `json:"system"`
	HasChildren    bool     `json:"has_children"`
	CanMount       bool     `json:"can_mount"`
	CanFormat      bool     `json:"can_format"`
	CanInitialize  bool     `json:"can_initialize"`
	ManagedFstab   bool     `json:"managed_fstab"`
	UsedBytes      uint64   `json:"used_bytes,omitempty"`
	AvailableBytes uint64   `json:"available_bytes,omitempty"`
}

type lsblkDevice struct {
	Name        string        `json:"name"`
	Path        string        `json:"path"`
	PKName      *string       `json:"pkname"`
	Size        json.Number   `json:"size"`
	Type        string        `json:"type"`
	FSType      *string       `json:"fstype"`
	Label       *string       `json:"label"`
	UUID        *string       `json:"uuid"`
	Mountpoints []interface{} `json:"mountpoints"`
	Model       *string       `json:"model"`
	Serial      *string       `json:"serial"`
	RO          bool          `json:"ro"`
	Children    []lsblkDevice `json:"children"`
}

type lsblkOutput struct {
	BlockDevices []lsblkDevice `json:"blockdevices"`
}

func ListStorageDevices() ([]StorageDevice, error) {
	out, err := storageCommand("lsblk", "-J", "-b", "-o", "NAME,PATH,PKNAME,SIZE,TYPE,FSTYPE,LABEL,UUID,MOUNTPOINTS,MODEL,SERIAL,RO")
	if err != nil {
		return nil, commandError("lsblk", out, err)
	}
	var raw lsblkOutput
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse lsblk output: %w", err)
	}
	rootSource, _ := storageCommand("findmnt", "-n", "-o", "SOURCE", "/")
	protected := protectedDeviceNames(raw.BlockDevices, strings.TrimSpace(string(rootSource)))
	managed, _ := managedFstabUUIDs("/etc/fstab")
	items := make([]StorageDevice, 0)
	var flatten func(lsblkDevice)
	flatten = func(d lsblkDevice) {
		size, _ := strconv.ParseInt(d.Size.String(), 10, 64)
		mountpoints := stringMountpoints(d.Mountpoints)
		item := StorageDevice{
			Name: d.Name, Path: d.Path, Parent: stringValue(d.PKName), Size: size, Type: d.Type,
			FSType: stringValue(d.FSType), Label: stringValue(d.Label), UUID: stringValue(d.UUID), Mountpoints: mountpoints,
			Model: strings.TrimSpace(stringValue(d.Model)), Serial: strings.TrimSpace(stringValue(d.Serial)),
			ReadOnly: d.RO, System: protected[d.Name], HasChildren: len(d.Children) > 0,
		}
		item.ManagedFstab = managed[item.UUID]
		item.CanMount = !item.System && !item.ReadOnly && item.FSType != "" && len(item.Mountpoints) == 0
		item.CanFormat = !item.System && !item.ReadOnly && item.Type == "part" && item.FSType == "" && len(item.Mountpoints) == 0
		item.CanInitialize = !item.System && !item.ReadOnly && item.Type == "disk" && item.FSType == "" && len(item.Mountpoints) == 0 && !item.HasChildren
		if len(mountpoints) > 0 {
			var stat syscall.Statfs_t
			if syscall.Statfs(mountpoints[0], &stat) == nil {
				item.AvailableBytes = stat.Bavail * uint64(stat.Bsize)
				item.UsedBytes = (stat.Blocks - stat.Bfree) * uint64(stat.Bsize)
			}
		}
		items = append(items, item)
		for _, child := range d.Children {
			flatten(child)
		}
	}
	for _, device := range raw.BlockDevices {
		flatten(device)
	}
	return items, nil
}

func protectedDeviceNames(devices []lsblkDevice, rootSource string) map[string]bool {
	protected := map[string]bool{}
	// findmnt appends the btrfs subvolume to SOURCE (for example
	// /dev/sda2[/@]). Strip it before resolving the lsblk device name.
	rootDevice := strings.SplitN(strings.TrimSpace(rootSource), "[", 2)[0]
	rootName := filepath.Base(rootDevice)
	parents := map[string]string{}
	var walk func(lsblkDevice, string)
	walk = func(d lsblkDevice, parent string) {
		if stringValue(d.PKName) != "" {
			parent = stringValue(d.PKName)
		}
		parents[d.Name] = parent
		for _, child := range d.Children {
			walk(child, d.Name)
		}
	}
	for _, d := range devices {
		walk(d, "")
	}
	if _, ok := parents[rootName]; rootName == "." || rootName == "" || !ok {
		// If the root source cannot be mapped to the current lsblk tree, fail
		// closed: no device may be offered for destructive storage actions.
		for name := range parents {
			protected[name] = true
		}
		return protected
	}
	for name := rootName; name != ""; name = parents[name] {
		protected[name] = true
	}
	// Once the physical/root parent is protected, protect every sibling
	// partition beneath it as well (for example an unmounted EFI partition).
	changed := true
	for changed {
		changed = false
		for name, parent := range parents {
			if parent != "" && protected[parent] && !protected[name] {
				protected[name] = true
				changed = true
			}
		}
	}
	return protected
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stringMountpoints(values []interface{}) []string {
	result := []string{}
	for _, value := range values {
		if s, ok := value.(string); ok && s != "" {
			result = append(result, s)
		}
	}
	return result
}

func ValidateMountPoint(path string) error {
	if !mountPointPattern.MatchString(path) {
		return errors.New("挂载目录必须是 /mnt 下的单层安全路径，例如 /mnt/backup-storage")
	}
	return nil
}

func MountStorage(devicePath, mountPoint string) error {
	storageMutationMu.Lock()
	defer storageMutationMu.Unlock()
	return mountStorage(devicePath, mountPoint)
}

func mountStorage(devicePath, mountPoint string) error {
	if err := prepareMountDestination(mountPoint); err != nil {
		return err
	}
	device, err := requireDevice(devicePath)
	if err != nil {
		return err
	}
	if !device.CanMount || device.UUID == "" {
		return errors.New("设备不可挂载：必须是未挂载、已有文件系统且非系统盘的设备")
	}
	line := fmt.Sprintf("UUID=%s %s %s defaults,nofail,noatime 0 2", device.UUID, mountPoint, device.FSType)
	if err := addManagedFstabLine("/etc/fstab", line); err != nil {
		return err
	}
	if out, err := storageCommand("mount", mountPoint); err != nil {
		_ = removeManagedFstabLine("/etc/fstab", device.UUID, mountPoint)
		return commandError("mount", out, err)
	}
	if out, err := storageCommand("findmnt", "-n", "--target", mountPoint); err != nil {
		return commandError("findmnt", out, err)
	}
	return nil
}

func UnmountStorage(devicePath string, removeAutoMount bool) error {
	storageMutationMu.Lock()
	defer storageMutationMu.Unlock()
	device, err := requireDevice(devicePath)
	if err != nil {
		return err
	}
	if device.System || len(device.Mountpoints) == 0 {
		return errors.New("设备不是可卸载的数据盘")
	}
	for _, mountPoint := range device.Mountpoints {
		if !strings.HasPrefix(mountPoint, "/mnt/") {
			return errors.New("仅允许从 /mnt 子目录卸载数据盘")
		}
	}
	if out, err := storageCommand("umount", device.Path); err != nil {
		return commandError("umount", out, err)
	}
	if removeAutoMount && device.UUID != "" {
		for _, mountPoint := range device.Mountpoints {
			if err := removeManagedFstabLine("/etc/fstab", device.UUID, mountPoint); err != nil {
				return err
			}
		}
	}
	return nil
}

func formatPartition(devicePath, confirmation string) error {
	if confirmation != devicePath {
		return errors.New("确认文本必须与设备路径完全一致")
	}
	device, err := requireDevice(devicePath)
	if err != nil {
		return err
	}
	if !device.CanFormat {
		return errors.New("仅允许格式化非系统盘、未挂载且没有文件系统的分区")
	}
	if hasStorageSignature(devicePath) {
		return errors.New("检测到已有存储签名，为保护数据已拒绝格式化")
	}
	out, err := storageCommand("mkfs.ext4", "-F", devicePath)
	if err != nil {
		return commandError("mkfs.ext4", out, err)
	}
	return nil
}

func initializeDisk(devicePath, confirmation string) (string, error) {
	if confirmation != devicePath {
		return "", errors.New("确认文本必须与设备路径完全一致")
	}
	device, err := requireDevice(devicePath)
	if err != nil {
		return "", err
	}
	if !device.CanInitialize {
		return "", errors.New("仅允许初始化非系统盘、未挂载、无分区且无文件系统的裸盘")
	}
	if hasStorageSignature(devicePath) {
		return "", errors.New("检测到已有分区表或存储签名，为保护数据已拒绝初始化")
	}
	if out, err := storageCommand("parted", "-s", devicePath, "mklabel", "gpt", "mkpart", "primary", "ext4", "1MiB", "100%"); err != nil {
		return "", commandError("parted", out, err)
	}
	_, _ = storageCommand("partprobe", devicePath)
	for i := 0; i < 20; i++ {
		devices, _ := ListStorageDevices()
		for _, candidate := range devices {
			if candidate.Parent == device.Name && candidate.Type == "part" {
				if err := formatPartition(candidate.Path, candidate.Path); err != nil {
					return "", err
				}
				return candidate.Path, nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return "", errors.New("分区创建完成，但系统未及时识别新分区，请刷新后继续")
}

func FormatAndMountPartition(devicePath, confirmation, mountPoint string) error {
	storageMutationMu.Lock()
	defer storageMutationMu.Unlock()
	if err := prepareMountDestination(mountPoint); err != nil {
		return err
	}
	if err := formatPartition(devicePath, confirmation); err != nil {
		return err
	}
	if err := mountStorage(devicePath, mountPoint); err != nil {
		return fmt.Errorf("分区已格式化为 ext4，但自动挂载失败: %w", err)
	}
	return nil
}

func InitializeAndMountDisk(devicePath, confirmation, mountPoint string) error {
	storageMutationMu.Lock()
	defer storageMutationMu.Unlock()
	if err := prepareMountDestination(mountPoint); err != nil {
		return err
	}
	partitionPath, err := initializeDisk(devicePath, confirmation)
	if err != nil {
		return err
	}
	if err := mountStorage(partitionPath, mountPoint); err != nil {
		return fmt.Errorf("裸盘已初始化并格式化为 ext4，但自动挂载失败: %w", err)
	}
	return nil
}

func hasStorageSignature(devicePath string) bool {
	out, err := storageCommand("wipefs", "-n", devicePath)
	return err != nil || strings.TrimSpace(string(out)) != ""
}

func prepareMountDestination(mountPoint string) error {
	if err := ValidateMountPoint(mountPoint); err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(mountPoint); err == nil && !strings.HasPrefix(resolved, "/mnt/") {
		return errors.New("挂载目录不能通过符号链接指向 /mnt 之外")
	}
	if out, err := storageCommand("findmnt", "-n", "--mountpoint", mountPoint); err == nil && strings.TrimSpace(string(out)) != "" {
		return errors.New("挂载目录已是其他文件系统的挂载点，请更换目录或先卸载现有文件系统")
	}
	if entries, err := os.ReadDir(mountPoint); err == nil && len(entries) > 0 {
		return errors.New("挂载目录不为空，挂载会隐藏其中现有文件，请选择空目录")
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("检查挂载目录失败: %w", err)
	}
	if err := ensureFstabMountPointAvailable("/etc/fstab", mountPoint); err != nil {
		return err
	}
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return fmt.Errorf("创建挂载目录失败: %w", err)
	}
	return nil
}

func ensureFstabMountPointAvailable(path, mountPoint string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取 fstab 失败: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == mountPoint {
			return errors.New("该挂载目录已存在于 /etc/fstab")
		}
	}
	return nil
}

func requireDevice(path string) (StorageDevice, error) {
	if !devicePathPattern.MatchString(path) {
		return StorageDevice{}, errors.New("设备路径格式无效")
	}
	devices, err := ListStorageDevices()
	if err != nil {
		return StorageDevice{}, err
	}
	for _, device := range devices {
		if device.Path == path {
			return device, nil
		}
	}
	return StorageDevice{}, errors.New("设备不存在")
}

func managedFstabUUIDs(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	result := map[string]bool{}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != managedFstabComment || i+1 >= len(lines) {
			continue
		}
		fields := strings.Fields(lines[i+1])
		if len(fields) > 0 && strings.HasPrefix(fields[0], "UUID=") {
			result[strings.TrimPrefix(fields[0], "UUID=")] = true
		}
	}
	return result, nil
}

func addManagedFstabLine(path, line string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取 fstab 失败: %w", err)
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return errors.New("fstab 条目无效")
	}
	for _, existing := range strings.Split(string(data), "\n") {
		ef := strings.Fields(existing)
		if len(ef) >= 2 && (ef[0] == fields[0] || ef[1] == fields[1]) {
			return errors.New("该 UUID 或挂载目录已存在于 /etc/fstab")
		}
	}
	content := strings.TrimRight(string(data), "\n") + "\n" + managedFstabComment + "\n" + line + "\n"
	return writeFileAtomic(path, []byte(content), existingFileMode(path, 0644))
}

func removeManagedFstabLine(path, uuid, mountPoint string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取 fstab 失败: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	result := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == managedFstabComment && i+1 < len(lines) {
			fields := strings.Fields(lines[i+1])
			if len(fields) >= 2 && fields[0] == "UUID="+uuid && fields[1] == mountPoint {
				i++
				continue
			}
		}
		result = append(result, lines[i])
	}
	return writeFileAtomic(path, []byte(strings.Join(result, "\n")), existingFileMode(path, 0644))
}

func existingFileMode(path string, fallback os.FileMode) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return fallback
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".server-panel-fstab-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func commandError(name string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s 执行失败: %w", name, err)
	}
	return fmt.Errorf("%s 执行失败: %s", name, message)
}

type LocalUser struct {
	Username       string          `json:"username"`
	UID            int             `json:"uid"`
	GID            int             `json:"gid"`
	Home           string          `json:"home"`
	Shell          string          `json:"shell"`
	Groups         []string        `json:"groups"`
	Sudo           bool            `json:"sudo"`
	SSHLogin       bool            `json:"ssh_login"`
	AuthorizedKeys []AuthorizedKey `json:"authorized_keys"`
}

type AuthorizedKey struct {
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	Comment     string `json:"comment"`
}

func ListLocalUsers() ([]LocalUser, error) {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return nil, err
	}
	users := []LocalUser{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 7 {
			continue
		}
		uid, errUID := strconv.Atoi(fields[2])
		gid, errGID := strconv.Atoi(fields[3])
		if errUID != nil || errGID != nil || (uid != 0 && uid < 1000) || uid == 65534 {
			continue
		}
		groups := userGroups(fields[0])
		user := LocalUser{Username: fields[0], UID: uid, GID: gid, Home: fields[5], Shell: fields[6], Groups: groups}
		user.SSHLogin = !strings.HasSuffix(user.Shell, "/nologin") && !strings.HasSuffix(user.Shell, "/false")
		user.Sudo = uid == 0 || containsString(groups, "sudo") || containsString(groups, "wheel")
		user.AuthorizedKeys = readAuthorizedKeys(filepath.Join(user.Home, ".ssh", "authorized_keys"))
		users = append(users, user)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].UID < users[j].UID })
	return users, nil
}

func userGroups(username string) []string {
	out, err := storageCommand("id", "-nG", username)
	if err != nil {
		return []string{}
	}
	return strings.Fields(string(out))
}

func readAuthorizedKeys(path string) []AuthorizedKey {
	data, err := os.ReadFile(path)
	if err != nil {
		return []AuthorizedKey{}
	}
	keys := []AuthorizedKey{}
	for len(data) > 0 {
		key, comment, _, rest, err := ssh.ParseAuthorizedKey(data)
		if err != nil {
			break
		}
		keys = append(keys, AuthorizedKey{Type: key.Type(), Fingerprint: ssh.FingerprintSHA256(key), Comment: comment})
		data = rest
	}
	return keys
}

type PathPermission struct {
	Path       string `json:"path"`
	Username   string `json:"username"`
	Exists     bool   `json:"exists"`
	Mountpoint string `json:"mountpoint"`
	Source     string `json:"source"`
	FSType     string `json:"fstype"`
	OwnerUID   uint32 `json:"owner_uid"`
	OwnerGID   uint32 `json:"owner_gid"`
	Mode       string `json:"mode"`
	Readable   bool   `json:"readable"`
	Writable   bool   `json:"writable"`
	Searchable bool   `json:"searchable"`
}

func CheckPathPermission(username, path string) (PathPermission, error) {
	result := PathPermission{Path: path, Username: username}
	if !strings.HasPrefix(path, "/mnt/") || filepath.Clean(path) != path {
		return result, errors.New("检查路径必须位于 /mnt 下")
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil && !strings.HasPrefix(resolved, "/mnt/") {
		return result, errors.New("检查路径不能通过符号链接指向 /mnt 之外")
	}
	users, err := ListLocalUsers()
	if err != nil {
		return result, err
	}
	var selected *LocalUser
	for i := range users {
		if users[i].Username == username {
			selected = &users[i]
			break
		}
	}
	if selected == nil {
		return result, errors.New("用户不存在或不是可登录用户")
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}
	result.Exists = true
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return result, errors.New("无法读取目录权限")
	}
	result.OwnerUID, result.OwnerGID, result.Mode = stat.Uid, stat.Gid, info.Mode().String()
	result.Readable = checkUserAccess(selected.Username, "-r", path)
	result.Writable = checkUserAccess(selected.Username, "-w", path)
	result.Searchable = checkUserAccess(selected.Username, "-x", path)
	if out, err := storageCommand("findmnt", "-n", "-o", "TARGET,SOURCE,FSTYPE", "--target", path); err == nil {
		fields := strings.Fields(string(out))
		if len(fields) >= 3 {
			result.Mountpoint, result.Source, result.FSType = fields[0], fields[1], fields[2]
		}
	}
	return result, nil
}

func checkUserAccess(username, mode, path string) bool {
	_, err := storageCommand("runuser", "-u", username, "--", "test", mode, path)
	return err == nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
