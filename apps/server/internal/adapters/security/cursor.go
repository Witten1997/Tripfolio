package security

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/paging"
)

const cursorPurpose = "cursor"

// CursorCodec 用密钥环签发与校验分页游标：明文含 kid、账号与作用域，摘要为 HMAC-SHA256；
// 令牌形如 base64url(明文) "." base64url(摘要)。密钥轮换后旧游标仍可用旧 kid 校验。
type CursorCodec struct {
	keyring *Keyring
}

// NewCursorCodec 创建游标编解码器。
func NewCursorCodec(keyring *Keyring) *CursorCodec { return &CursorCodec{keyring: keyring} }

var _ paging.Codec = (*CursorCodec)(nil)

// Encode 实现 paging.Codec。
func (c *CursorCodec) Encode(accountID uuid.UUID, scope string, payload any) (string, error) {
	env, err := paging.NewEnvelope(accountID, scope, c.keyring.CurrentKID(), payload)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	_, mac, err := c.keyring.MAC(cursorPurpose, raw)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac), nil
}

// Decode 实现 paging.Codec。
func (c *CursorCodec) Decode(accountID uuid.UUID, scope string, token string, payload any) error {
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return paging.ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return paging.ErrInvalidCursor
	}
	mac, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return paging.ErrInvalidCursor
	}
	var env paging.Envelope
	if err := json.Unmarshal(raw, &env); err != nil || env.KeyID == "" {
		return paging.ErrInvalidCursor
	}
	if !c.keyring.VerifyMAC(env.KeyID, cursorPurpose, raw, mac) {
		return paging.ErrInvalidCursor
	}
	return env.Open(accountID, scope, payload)
}
