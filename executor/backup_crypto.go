package executor

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/naibabiji/server-panel/database"
)

const (
	backupRecipientSetting = "backup_age_recipient"
	backupIdentitySetting  = "backup_age_identity"
	portableBackupVersion  = 1
	maxWrappedIdentitySize = 64 << 10
	maxDecryptedBackupSize = 1 << 30
)

type portableBackupManifest struct {
	Format    string `json:"format"`
	Version   int    `json:"version"`
	CreatedAt string `json:"created_at"`
	Cipher    string `json:"cipher"`
}

// PrepareBackupEncryption creates the panel backup keypair when it is absent,
// or re-wraps the existing private identity when the view password changes.
// The private identity is never stored without passphrase encryption.
func PrepareBackupEncryption(db *sql.DB, oldPassword, newPassword string, replace bool) (recipient, wrappedIdentity string, err error) {
	var existingRecipient, existingWrapped string
	_ = db.QueryRow("SELECT svalue FROM settings WHERE skey = ?", backupRecipientSetting).Scan(&existingRecipient)
	_ = db.QueryRow("SELECT svalue FROM settings WHERE skey = ?", backupIdentitySetting).Scan(&existingWrapped)

	if !replace && oldPassword == "" && existingRecipient != "" && existingWrapped != "" {
		return existingRecipient, existingWrapped, nil
	}

	var identity *age.HybridIdentity
	if existingWrapped != "" && oldPassword != "" && !replace {
		identity, err = unwrapBackupIdentity(existingWrapped, oldPassword)
		if err != nil {
			return "", "", fmt.Errorf("解锁现有备份密钥失败: %w", err)
		}
	} else {
		identity, err = age.GenerateHybridIdentity()
		if err != nil {
			return "", "", fmt.Errorf("生成备份密钥失败: %w", err)
		}
	}

	wrappedIdentity, err = wrapBackupIdentity(identity, newPassword)
	if err != nil {
		return "", "", err
	}
	return identity.Recipient().String(), wrappedIdentity, nil
}

func SaveBackupEncryptionSettings(tx *sql.Tx, recipient, wrappedIdentity string) error {
	for key, value := range map[string]string{
		backupRecipientSetting: recipient,
		backupIdentitySetting:  wrappedIdentity,
	} {
		if _, err := tx.Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES (?, ?)", key, value); err != nil {
			return err
		}
	}
	return nil
}

func wrapBackupIdentity(identity *age.HybridIdentity, password string) (string, error) {
	recipient, err := age.NewScryptRecipient(password)
	if err != nil {
		return "", fmt.Errorf("创建密码加密器失败: %w", err)
	}
	var encrypted bytes.Buffer
	w, err := age.Encrypt(&encrypted, recipient)
	if err != nil {
		return "", fmt.Errorf("加密备份密钥失败: %w", err)
	}
	if _, err := io.WriteString(w, identity.String()); err != nil {
		return "", fmt.Errorf("加密备份密钥失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("完成备份密钥加密失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(encrypted.Bytes()), nil
}

func unwrapBackupIdentity(encoded, password string) (*age.HybridIdentity, error) {
	encrypted, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(encrypted) > maxWrappedIdentitySize {
		return nil, fmt.Errorf("备份密钥格式无效")
	}
	identity, err := age.NewScryptIdentity(password)
	if err != nil {
		return nil, err
	}
	// We generate wrappers with age's current factor 18. Reject more expensive
	// hostile inputs instead of letting an uploaded backup force excessive work.
	identity.SetMaxWorkFactor(18)
	r, err := age.Decrypt(bytes.NewReader(encrypted), identity)
	if err != nil {
		return nil, fmt.Errorf("查看密码不正确或备份密钥已损坏")
	}
	plain, err := io.ReadAll(io.LimitReader(r, maxWrappedIdentitySize+1))
	if err != nil || len(plain) > maxWrappedIdentitySize {
		return nil, fmt.Errorf("读取备份密钥失败")
	}
	parsed, err := age.ParseHybridIdentity(strings.TrimSpace(string(plain)))
	if err != nil {
		return nil, fmt.Errorf("备份密钥内容无效")
	}
	return parsed, nil
}

func createPortableBackup(legacyArchive, dir string) (string, error) {
	db := backupDB()
	if db == nil {
		return "", fmt.Errorf("数据库未连接")
	}
	var recipientText, wrappedIdentity string
	if err := db.QueryRow("SELECT svalue FROM settings WHERE skey = ?", backupRecipientSetting).Scan(&recipientText); err != nil || recipientText == "" {
		return "", fmt.Errorf("加密备份尚未初始化，请先验证一次查看密码")
	}
	if err := db.QueryRow("SELECT svalue FROM settings WHERE skey = ?", backupIdentitySetting).Scan(&wrappedIdentity); err != nil || wrappedIdentity == "" {
		return "", fmt.Errorf("加密备份密钥缺失，请先验证一次查看密码")
	}
	recipient, err := age.ParseHybridRecipient(recipientText)
	if err != nil {
		return "", fmt.Errorf("备份公钥无效: %w", err)
	}
	identityBlob, err := base64.StdEncoding.DecodeString(wrappedIdentity)
	if err != nil || len(identityBlob) > maxWrappedIdentitySize {
		return "", fmt.Errorf("加密备份密钥格式无效")
	}

	name := fmt.Sprintf("server-panel-backup.%s.spbackup", time.Now().UTC().Format("20060102-150405"))
	finalPath := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".spbackup-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return "", err
	}

	zw := zip.NewWriter(tmp)
	manifest, _ := json.Marshal(portableBackupManifest{Format: "server-panel-backup", Version: portableBackupVersion, CreatedAt: time.Now().UTC().Format(time.RFC3339), Cipher: "age-v1"})
	if err := writeStoredZipEntry(zw, "manifest.json", manifest); err != nil {
		zw.Close()
		tmp.Close()
		return "", err
	}
	if err := writeStoredZipEntry(zw, "identity.age", identityBlob); err != nil {
		zw.Close()
		tmp.Close()
		return "", err
	}
	payloadHeader := &zip.FileHeader{Name: "payload.age", Method: zip.Store}
	payloadHeader.SetMode(0600)
	payloadEntry, err := zw.CreateHeader(payloadHeader)
	if err != nil {
		zw.Close()
		tmp.Close()
		return "", err
	}
	ageWriter, err := age.Encrypt(payloadEntry, recipient)
	if err != nil {
		zw.Close()
		tmp.Close()
		return "", err
	}
	src, err := os.Open(legacyArchive)
	if err != nil {
		zw.Close()
		tmp.Close()
		return "", err
	}
	_, copyErr := io.Copy(ageWriter, src)
	src.Close()
	closeErr := ageWriter.Close()
	if copyErr != nil || closeErr != nil {
		zw.Close()
		tmp.Close()
		if copyErr != nil {
			return "", copyErr
		}
		return "", closeErr
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", err
	}
	return finalPath, nil
}

func writeStoredZipEntry(zw *zip.Writer, name string, data []byte) error {
	h := &zip.FileHeader{Name: name, Method: zip.Store}
	h.SetMode(0600)
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// decryptPortableBackup creates a short-lived legacy archive for the existing
// startup restore path. The caller owns the returned file and must remove it.
func decryptPortableBackup(path, password, dir string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("加密备份格式无效: %w", err)
	}
	defer zr.Close()
	if len(zr.File) != 3 {
		return "", fmt.Errorf("加密备份内容不完整")
	}
	entries := make(map[string]*zip.File, 3)
	for _, f := range zr.File {
		if f.Method != zip.Store || entries[f.Name] != nil {
			return "", fmt.Errorf("加密备份条目无效")
		}
		entries[f.Name] = f
	}
	manifestFile, identityFile, payloadFile := entries["manifest.json"], entries["identity.age"], entries["payload.age"]
	if manifestFile == nil || identityFile == nil || payloadFile == nil || manifestFile.UncompressedSize64 > 64<<10 || identityFile.UncompressedSize64 > maxWrappedIdentitySize {
		return "", fmt.Errorf("加密备份内容不完整")
	}
	manifestBytes, err := readZipEntry(manifestFile, 64<<10)
	if err != nil {
		return "", err
	}
	var manifest portableBackupManifest
	if json.Unmarshal(manifestBytes, &manifest) != nil || manifest.Format != "server-panel-backup" || manifest.Version != portableBackupVersion || manifest.Cipher != "age-v1" {
		return "", fmt.Errorf("不支持的加密备份版本")
	}
	identityBytes, err := readZipEntry(identityFile, maxWrappedIdentitySize)
	if err != nil {
		return "", err
	}
	identity, err := unwrapBackupIdentity(base64.StdEncoding.EncodeToString(identityBytes), password)
	if err != nil {
		return "", err
	}
	payload, err := payloadFile.Open()
	if err != nil {
		return "", err
	}
	defer payload.Close()
	plain, err := age.Decrypt(payload, identity)
	if err != nil {
		return "", fmt.Errorf("备份内容无法解密: %w", err)
	}
	out, err := os.CreateTemp(dir, "server-panel-restore-stage.*.tar.gz")
	if err != nil {
		return "", err
	}
	outPath := out.Name()
	if err := out.Chmod(0600); err != nil {
		out.Close()
		os.Remove(outPath)
		return "", err
	}
	n, copyErr := io.Copy(out, io.LimitReader(plain, maxDecryptedBackupSize+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || n > maxDecryptedBackupSize {
		os.Remove(outPath)
		if n > maxDecryptedBackupSize {
			return "", fmt.Errorf("解密后的备份超过大小上限")
		}
		if copyErr != nil {
			return "", copyErr
		}
		return "", closeErr
	}
	return outPath, nil
}

func readZipEntry(f *zip.File, limit int64) ([]byte, error) {
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, fmt.Errorf("加密备份条目过大")
	}
	return b, nil
}

var backupDB = database.GetDB
