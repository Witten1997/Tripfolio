package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"

	"filippo.io/age"
)

func ParseKey(value string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(b) != 32 {
		return nil, errors.New("TRIPFOLIO_BACKUP_KEY 必须是 32 字节随机密钥的 base64 编码")
	}
	return b, nil
}

func keyID(key []byte) string { h := sha256.Sum256(key); return hex.EncodeToString(h[:8]) }

func (s *Service) Seal(password string) (string, error) {
	if len(s.key) != 32 {
		return "", errors.New("BACKUP_KEY_MISSING")
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	encrypted := aead.Seal(nonce, nonce, []byte(password), []byte("tripfolio/webdav/v1"))
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

func (s *Service) unseal(secret string) (string, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", errors.New("BACKUP_KEY_MISSING")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(secret)
	if err != nil || len(raw) < aead.NonceSize() {
		return "", errors.New("BACKUP_KEY_INVALID")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte("tripfolio/webdav/v1"))
	if err != nil {
		return "", errors.New("BACKUP_KEY_INVALID")
	}
	return string(plain), nil
}

func Decrypt(key string, input io.Reader, output io.Writer) error {
	if _, err := ParseKey(key); err != nil {
		return err
	}
	identity, err := age.NewScryptIdentity(key)
	if err != nil {
		return err
	}
	reader, err := age.Decrypt(input, identity)
	if err != nil {
		return errors.New("备份解密失败，请核对密钥与文件")
	}
	if _, err = io.Copy(output, reader); err != nil {
		return errors.New("备份解密或完整性验证失败")
	}
	return nil
}
