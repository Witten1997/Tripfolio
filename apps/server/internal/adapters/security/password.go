package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2 参数（技术选型评审 O1）：m=64 MiB、t=3、p=1；并发上限由 PasswordHasher 控制。
const (
	argonMemoryKiB = 64 * 1024
	argonTime      = 3
	argonThreads   = 1
	argonKeyLen    = 32
	argonSaltLen   = 16
)

// PasswordHasher 以有限并发计算 Argon2id，避免登录风暴耗尽内存。
type PasswordHasher struct {
	sem chan struct{}
}

// NewPasswordHasher 创建最多 concurrency 个并发哈希的哈希器。
func NewPasswordHasher(concurrency int) *PasswordHasher {
	if concurrency < 1 {
		concurrency = 1
	}
	return &PasswordHasher{sem: make(chan struct{}, concurrency)}
}

// Hash 返回 PHC 字符串：$argon2id$v=19$m=65536,t=3,p=1$<salt>$<hash>。
func (h *PasswordHasher) Hash(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h.sem <- struct{}{}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	<-h.sem
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemoryKiB, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify 比较密码与 PHC 字符串；格式错误返回 error，不匹配返回 false。
func (h *PasswordHasher) Verify(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("不支持的密码哈希格式")
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false, errors.New("密码哈希参数无效")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	h.sem <- struct{}{}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(want)))
	<-h.sem
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// DummyHash 是用于“账号不存在”分支的固定哈希，使两种分支耗时一致。
var DummyHash = func() string {
	h := NewPasswordHasher(1)
	s, _ := h.Hash("tripfolio-dummy-password")
	return s
}()

// RandomBytes 返回 n 字节密码学随机数。
func RandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// RandomToken 返回 32 字节随机值的 base64url 编码（43 字符）。
func RandomToken() (string, error) {
	b, err := RandomBytes(32)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Digest 返回 SHA-256 摘要，用于存储令牌摘要。
func Digest(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// SixDigitCode 返回 000000–999999 的验证码，保留前导零。
func SixDigitCode() (string, error) {
	b, err := RandomBytes(4)
	if err != nil {
		return "", err
	}
	n := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) % 1000000
	return fmt.Sprintf("%06d", n), nil
}
