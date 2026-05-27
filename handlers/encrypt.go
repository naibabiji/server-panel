package handlers

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// 内存中存储已解锁的派生密钥，key = session_token
var (
	derivedKeys   = make(map[string][]byte)
	derivedKeysMu sync.RWMutex
	// view password unlock 尝试次数追踪
	unlockAttempts   = make(map[string]int)
	unlockAttemptsMu sync.Mutex
	unlockLockUntil  = make(map[string]int64)
)

const maxUnlockAttempts = 5

func StoreDerivedKey(sessionToken string, key []byte) {
	derivedKeysMu.Lock()
	defer derivedKeysMu.Unlock()
	derivedKeys[sessionToken] = key
}

func GetDerivedKey(sessionToken string) []byte {
	derivedKeysMu.RLock()
	defer derivedKeysMu.RUnlock()
	return derivedKeys[sessionToken]
}

func RemoveDerivedKey(sessionToken string) {
	derivedKeysMu.Lock()
	defer derivedKeysMu.Unlock()
	delete(derivedKeys, sessionToken)
}

func GetSessionToken(c *gin.Context) (string, bool) {
	token, exists := c.Get("session_token")
	if !exists {
		return "", false
	}
	s, ok := token.(string)
	return s, ok && s != ""
}

func IsViewPasswordUnlocked(sessionToken string) bool {
	derivedKeysMu.RLock()
	defer derivedKeysMu.RUnlock()
	_, ok := derivedKeys[sessionToken]
	return ok
}

func init() {
	// 每小时清理过期的锁定状态和尝试记录，防止内存无限增长
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now().Unix()
			unlockAttemptsMu.Lock()
			for ip, until := range unlockLockUntil {
				if now >= until {
					delete(unlockLockUntil, ip)
					delete(unlockAttempts, ip)
				}
			}
			unlockAttemptsMu.Unlock()
		}
	}()
}

// DeriveEncryptionKey 从查看密码 + salt + pepper 派生 AES-256 密钥
func DeriveEncryptionKey(password string, saltHex string, pepperHex string) ([]byte, error) {
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return nil, fmt.Errorf("invalid salt: %w", err)
	}
	pepper, err := hex.DecodeString(pepperHex)
	if err != nil {
		return nil, fmt.Errorf("invalid pepper: %w", err)
	}

	combinedSalt := append(salt, pepper...)
	key := argon2.IDKey([]byte(password), combinedSalt, 3, 64*1024, 4, 32)
	return key, nil
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
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword bcrypt 验证
func VerifyPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
