// Package security 提供密钥环、密码哈希、访问令牌与随机值。
package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

// Keyring 持有带版本（kid）的主密钥，并按用途派生子密钥：jwt、cursor、challenge。
// 配置格式："kid1=<base64 主密钥>,kid2=<base64>"，第一个为当前签发密钥，其余用于验证旧值。
type Keyring struct {
	current string
	keys    map[string][]byte
	order   []string
}

// ParseKeyring 解析配置串。格式 "kid=<base64 主密钥>，至少 32 字节"；多个用逗号分隔，第一个为当前签发密钥。
func ParseKeyring(spec string) (*Keyring, error) {
	kr := &Keyring{keys: map[string][]byte{}}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kid, raw, ok := strings.Cut(part, "=")
		if !ok {
			// 常见错误：只填了 base64 密钥，没写 kid= 前缀。
			if key, err := base64.RawStdEncoding.DecodeString(part); err == nil && len(key) >= 32 {
				return nil, fmt.Errorf("密钥项缺少 kid= 前缀：%q 本身是 base64 密钥，请写成 k1=%s", part, part)
			}
			return nil, fmt.Errorf("密钥项 %q 必须是 kid=base64 形式，例如 k1=<openssl rand -base64 32 的输出>", part)
		}
		kid = strings.TrimSpace(kid)
		if kid == "" {
			return nil, fmt.Errorf("密钥项 %q 缺少密钥编号，正确写法是 k1=<base64>", part)
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			// 另一种常见错误：漏写 kid=，于是 base64 末尾的填充 "=" 被当成了分隔符，
			// 整串 base64 变成了 kid、值成了空串。
			if key, err := base64.RawStdEncoding.DecodeString(kid); err == nil && len(key) >= 32 {
				return nil, fmt.Errorf("密钥项缺少 kid= 前缀：%q 是 base64 密钥，末尾的 \"=\" 被当成分隔符了，请写成 k1=%s=", kid, kid)
			}
			return nil, fmt.Errorf("密钥 %s 的值为空，格式应为 k1=<base64 至少 32 字节>", kid)
		}
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			key, err = base64.RawURLEncoding.DecodeString(raw)
		}
		if err != nil {
			return nil, fmt.Errorf("密钥 %s 不是合法的 base64", kid)
		}
		if len(key) < 32 {
			return nil, fmt.Errorf("密钥 %s 至少 32 字节，当前 %d 字节", kid, len(key))
		}
		if _, dup := kr.keys[kid]; dup {
			return nil, fmt.Errorf("密钥 %s 重复", kid)
		}
		kr.keys[kid] = key
		kr.order = append(kr.order, kid)
	}
	if len(kr.order) == 0 {
		return nil, errors.New("至少需要一个密钥")
	}
	kr.current = kr.order[0]
	return kr, nil
}

// CurrentKID 返回当前签发密钥编号。
func (k *Keyring) CurrentKID() string { return k.current }

// Derive 按用途派生 32 字节子密钥；未知 kid 返回错误。
func (k *Keyring) Derive(kid, purpose string) ([]byte, error) {
	master, ok := k.keys[kid]
	if !ok {
		return nil, fmt.Errorf("未知的密钥编号 %q", kid)
	}
	r := hkdf.New(sha256.New, master, []byte("tripfolio"), []byte(purpose))
	out := make([]byte, 32)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}

// MAC 用当前密钥的 purpose 子密钥计算 HMAC-SHA256，返回 kid 与摘要。
func (k *Keyring) MAC(purpose string, data []byte) (kid string, mac []byte, err error) {
	key, err := k.Derive(k.current, purpose)
	if err != nil {
		return "", nil, err
	}
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return k.current, h.Sum(nil), nil
}

// VerifyMAC 用指定 kid 校验摘要。
func (k *Keyring) VerifyMAC(kid, purpose string, data, mac []byte) bool {
	key, err := k.Derive(kid, purpose)
	if err != nil {
		return false
	}
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return hmac.Equal(h.Sum(nil), mac)
}
