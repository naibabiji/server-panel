package handlers

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/naibabiji/server-panel/config"
	"golang.org/x/crypto/bcrypt"
)

type viewTokenRecord struct {
	ExpiresAt    time.Time
	SessionToken string
	IP           string
}

var (
	viewTokens   = make(map[string]viewTokenRecord)
	viewTokensMu sync.Mutex

	unlockAttempts   = make(map[string]int)
	unlockAttemptsMu sync.Mutex
)

const (
	maxUnlockAttempts = 5
	viewTokenTTL      = 2 * time.Minute
)

func init() {
	// 每小时清理过期的一次性查看授权，防止内存无限增长。
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			viewTokensMu.Lock()
			for token, record := range viewTokens {
				if !now.Before(record.ExpiresAt) {
					delete(viewTokens, token)
				}
			}
			viewTokensMu.Unlock()
		}
	}()
}

func CreateViewToken(sessionToken string, ip string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	viewTokensMu.Lock()
	defer viewTokensMu.Unlock()
	viewTokens[token] = viewTokenRecord{
		ExpiresAt:    time.Now().Add(viewTokenTTL),
		SessionToken: sessionToken,
		IP:           ip,
	}
	return token, nil
}

func ConsumeViewToken(token string, sessionToken string, ip string) bool {
	if token == "" {
		return false
	}
	viewTokensMu.Lock()
	defer viewTokensMu.Unlock()
	record, ok := viewTokens[token]
	if !ok {
		return false
	}
	delete(viewTokens, token)
	return record.SessionToken == sessionToken && record.IP == ip && time.Now().Before(record.ExpiresAt)
}

func GetSecretEncryptionKey(db *sql.DB) ([]byte, error) {
	if cfg := config.AppConfig; cfg != nil && cfg.Panel.DataDir != "" {
		return getFileSecretEncryptionKey(db, cfg.Panel.DataDir)
	}
	return getLegacyDBSecretEncryptionKey(db)
}

func getFileSecretEncryptionKey(db *sql.DB, dataDir string) ([]byte, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "secret.key")
	data, err := os.ReadFile(path)
	if err == nil {
		key, err := base64.StdEncoding.DecodeString(string(data))
		if err != nil {
			return nil, fmt.Errorf("invalid secret key file: %w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("invalid secret key file length")
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	key, err := getLegacyDBSecretEncryptionKey(db)
	if err != nil {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded), 0600); err != nil {
		return nil, err
	}
	_, _ = db.Exec("DELETE FROM settings WHERE skey = 'secret_encryption_key'")
	return key, nil
}

func getLegacyDBSecretEncryptionKey(db *sql.DB) ([]byte, error) {
	var encoded string
	_ = db.QueryRow("SELECT svalue FROM settings WHERE skey = 'secret_encryption_key'").Scan(&encoded)
	if encoded != "" {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("invalid secret encryption key: %w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("invalid secret encryption key length")
		}
		return key, nil
	}
	return nil, fmt.Errorf("secret encryption key does not exist")
}

// EncryptPassword AES-256-GCM 加密
func EncryptPassword(plaintext string, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptPassword AES-256-GCM 解密
func DecryptPassword(encoded string, key []byte) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// HashPassword bcrypt 哈希
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword bcrypt 验证
func VerifyPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
