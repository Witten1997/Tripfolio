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

// ParseKeyring 解析配置串。主密钥至少 32 字节。
func ParseKeyring(spec string) (*Keyring, error) {
	kr := &Keyring{keys: map[string][]byte{}}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kid, raw, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(kid) == "" {
			return nil, fmt.Errorf("密钥项 %q 必须是 kid=base64 形式", part)
		}
		kid = strings.TrimSpace(kid)
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
		if err != nil {
			key, err = base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
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
