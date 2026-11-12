package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id 参数（RFC 9106 第二推荐档的内存，迭代次数取 OWASP 建议的下限之上）。
// 单次计算约 64 MiB 内存，hashSlots 限制同时进行的计算数，防止并发请求把内存吃满。
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// MinPasswordLen 是管理员和分享密码的最小长度。
const MinPasswordLen = 8

// maxPasswordLen 防止超长输入拖慢哈希。
const maxPasswordLen = 1024

var hashSlots = make(chan struct{}, 2)

// ErrPasswordFormat 表示存储的密码哈希无法解析。
var ErrPasswordFormat = errors.New("auth: malformed password hash")

// CheckPasswordPolicy 校验新密码。
func CheckPasswordPolicy(pw string) error {
	if len(pw) < MinPasswordLen {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	}
	if len(pw) > maxPasswordLen {
		return fmt.Errorf("password must be at most %d bytes", maxPasswordLen)
	}
	return nil
}

// hashPassword 返回 PHC 格式的 Argon2id 哈希：$argon2id$v=19$m=…,t=…,p=…$salt$hash。
func hashPassword(pw string) string {
	salt := make([]byte, argonSaltLen)
	_, _ = rand.Read(salt)
	hashSlots <- struct{}{}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	<-hashSlots
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// verifyPassword 用哈希里记录的参数重算并常量时间比较。
func verifyPassword(encoded, pw string) (bool, error) {
	if len(pw) > maxPasswordLen {
		return false, nil
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrPasswordFormat
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrPasswordFormat
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || t == 0 || p == 0 || m > 1<<20 || t > 16 {
		return false, ErrPasswordFormat
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrPasswordFormat
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, ErrPasswordFormat
	}
	hashSlots <- struct{}{}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	<-hashSlots
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
