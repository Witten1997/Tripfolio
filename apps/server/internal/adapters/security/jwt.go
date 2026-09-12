package security

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	jwtIssuer   = "tripfolio"
	jwtAudience = "tripfolio-api"
	jwtPurpose  = "jwt"
)

// AccessClaims 是访问令牌的声明。
type AccessClaims struct {
	AccountID uuid.UUID
	SessionID uuid.UUID
	ExpiresAt time.Time
}

// TokenIssuer 用密钥环签发与解析 HS256 访问令牌，头部带 kid 以支持轮换。
type TokenIssuer struct {
	keyring *Keyring
	ttl     time.Duration
}

// NewTokenIssuer 创建签发器；ttl 为访问令牌有效期（接口设计 3.1：15 分钟）。
func NewTokenIssuer(keyring *Keyring, ttl time.Duration) *TokenIssuer {
	return &TokenIssuer{keyring: keyring, ttl: ttl}
}

// TTL 返回访问令牌有效期。
func (t *TokenIssuer) TTL() time.Duration { return t.ttl }

// Issue 签发令牌，包含 sub、sid、iss、aud、iat、exp。
func (t *TokenIssuer) Issue(accountID, sessionID uuid.UUID, now time.Time) (string, error) {
	kid := t.keyring.CurrentKID()
	key, err := t.keyring.Derive(kid, jwtPurpose)
	if err != nil {
		return "", err
	}
	claims := jwt.MapClaims{
		"sub": accountID.String(),
		"sid": sessionID.String(),
		"iss": jwtIssuer,
		"aud": jwtAudience,
		"iat": now.Unix(),
		"exp": now.Add(t.ttl).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["kid"] = kid
	return token.SignedString(key)
}

// Parse 校验签名、签发者、受众与过期时间。
func (t *TokenIssuer) Parse(raw string, now time.Time) (AccessClaims, error) {
	var claims jwt.MapClaims
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(jwtIssuer),
		jwt.WithAudience(jwtAudience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return now }),
		jwt.WithLeeway(30*time.Second),
	)
	_, err := parser.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("令牌缺少 kid")
		}
		return t.keyring.Derive(kid, jwtPurpose)
	})
	if err != nil {
		return AccessClaims{}, err
	}
	sub, _ := claims["sub"].(string)
	sid, _ := claims["sid"].(string)
	accountID, err := uuid.Parse(sub)
	if err != nil {
		return AccessClaims{}, fmt.Errorf("令牌 sub 无效")
	}
	sessionID, err := uuid.Parse(sid)
	if err != nil {
		return AccessClaims{}, fmt.Errorf("令牌 sid 无效")
	}
	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil {
		return AccessClaims{}, fmt.Errorf("令牌缺少 exp")
	}
	return AccessClaims{AccountID: accountID, SessionID: sessionID, ExpiresAt: exp.Time}, nil
}
