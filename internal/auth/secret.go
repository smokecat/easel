package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// randomToken 返回 n 字节 CSPRNG 随机数的 base64url（无填充）编码。
func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b) // crypto/rand.Read 不会返回错误
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashToken 是所有随机令牌在库里的形态：SHA-256 的 hex。高熵随机值不需要盐和慢哈希；
// 查找走索引，比较的是哈希而不是明文，不需要常量时间比较。
func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// keySize 是服务端密钥的长度。
const keySize = 32

// LoadOrCreateKey 读取数据目录里的服务端密钥（只用来派生分享链接令牌），不存在时生成（权限 0600）。
// 换掉这个文件会让所有分享链接失效。
func LoadOrCreateKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != keySize {
			return nil, fmt.Errorf("%s: want %d bytes, got %d", path, keySize, len(key))
		}
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, keySize)
	_, _ = rand.Read(key)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(key); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return key, nil
}

// deriveShareToken 由服务端密钥和分享记录里的 nonce 算出分享令牌，所以库里不存明文也能再次显示链接。
func deriveShareToken(key []byte, nonce string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("easel share v1:" + nonce))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:24])
}
