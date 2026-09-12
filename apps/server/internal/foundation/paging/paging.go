// Package paging 定义键集分页的公共约定（接口设计 1.3）：页大小、页结果与不透明游标的编解码接口。
// 游标绑定账号与作用域（路径、筛选、排序），签名实现在 adapters/security；InsecureCodec 仅供单元测试。
package paging

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/apperr"
)

const (
	// DefaultLimit 是未指定 limit 时的页大小。
	DefaultLimit = 50
	// MaxLimit 是允许的最大页大小。
	MaxLimit = 100
	// EnvelopeVersion 是游标明文结构的版本。
	EnvelopeVersion = 1
)

// Page 是一页结果：items 与下一页游标，没有更多时 next_cursor 为 null。
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// ErrInvalidCursor 表示游标格式错误、签名不符、账号或作用域不匹配。
var ErrInvalidCursor = errors.New("paging: invalid cursor")

// Codec 生成与校验不透明游标。payload 是各列表自己的位置结构（JSON 可编码）。
type Codec interface {
	Encode(accountID uuid.UUID, scope string, payload any) (string, error)
	// Decode 校验后把位置解码到 payload；失败返回 ErrInvalidCursor。
	Decode(accountID uuid.UUID, scope string, token string, payload any) error
}

// Limit 校验页大小：0 表示默认；超出 1–MaxLimit 返回 422 字段错误。
func Limit(requested int) (int, error) {
	if requested == 0 {
		return DefaultLimit, nil
	}
	if requested < 1 || requested > MaxLimit {
		return 0, apperr.Validation(apperr.Field("limit", "INVALID", fmt.Sprintf("limit 须在 1–%d 之间", MaxLimit)))
	}
	return requested, nil
}

// InvalidCursor 是 400 INVALID_CURSOR 的业务错误。
func InvalidCursor() *apperr.Error {
	return apperr.BadRequest("INVALID_CURSOR", "游标无效，请从第一页重新开始")
}

// Envelope 是游标的明文结构，供各实现共用；签名实现把 KeyID 放在其中并对整个明文计算摘要。
type Envelope struct {
	Version   int             `json:"v"`
	KeyID     string          `json:"kid,omitempty"`
	AccountID uuid.UUID       `json:"a"`
	Scope     string          `json:"s"`
	Payload   json.RawMessage `json:"p"`
}

// NewEnvelope 序列化 payload 并组装明文。
func NewEnvelope(accountID uuid.UUID, scope, keyID string, payload any) (Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{Version: EnvelopeVersion, KeyID: keyID, AccountID: accountID, Scope: scope, Payload: raw}, nil
}

// Open 校验明文的版本、账号与作用域并解码 payload。
func (e Envelope) Open(accountID uuid.UUID, scope string, payload any) error {
	if e.Version != EnvelopeVersion || e.AccountID != accountID || e.Scope != scope {
		return ErrInvalidCursor
	}
	if err := json.Unmarshal(e.Payload, payload); err != nil {
		return ErrInvalidCursor
	}
	return nil
}

// InsecureCodec 不签名，只做 base64 与账号、作用域核对；仅供单元测试。
type InsecureCodec struct{}

// Encode 实现 Codec。
func (InsecureCodec) Encode(accountID uuid.UUID, scope string, payload any) (string, error) {
	env, err := NewEnvelope(accountID, scope, "", payload)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Decode 实现 Codec。
func (InsecureCodec) Decode(accountID uuid.UUID, scope string, token string, payload any) error {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return ErrInvalidCursor
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return ErrInvalidCursor
	}
	return env.Open(accountID, scope, payload)
}
