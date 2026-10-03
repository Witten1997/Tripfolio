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
	"os"
	"strings"

	"filippo.io/age"
	"golang.org/x/crypto/scrypt"
)

const credentialPrefix = "scrypt-v2:"

func ParseKey(value string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(b) != 32 {
		return nil, errors.New("TRIPFOLIO_BACKUP_KEY 必须是 32 字节随机密钥的 base64 编码")
	}
	return b, nil
}

func keyID(key []byte) string { h := sha256.Sum256(key); return hex.EncodeToString(h[:8]) }

func (s *Service) Seal(password string) (string, error) {
	if s.passphrase() == "" {
		return "", errors.New("BACKUP_KEY_MISSING")
	}
	key, prefix, aad := s.key, "", "tripfolio/webdav/v1"
	var salt []byte
	if s.options.Password != "" {
		salt = make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return "", err
		}
		var err error
		key, err = s.credentialKey(salt)
		if err != nil {
			return "", err
		}
		prefix, aad = credentialPrefix, "tripfolio/webdav/v2"
	}
	block, err := aes.NewCipher(key)
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
	encrypted := aead.Seal(nonce, nonce, []byte(password), []byte(aad))
	return prefix + base64.StdEncoding.EncodeToString(append(salt, encrypted...)), nil
}

func (s *Service) unseal(secret string) (string, error) {
	key, aad := s.key, "tripfolio/webdav/v1"
	encoded, version2 := strings.CutPrefix(secret, credentialPrefix)
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.New("BACKUP_KEY_INVALID")
	}
	if version2 {
		if len(raw) < 16+12+16 || len(raw) > 16+12+16+2048 {
			return "", errors.New("BACKUP_KEY_INVALID")
		}
		key, err = s.credentialKey(raw[:16])
		if err != nil {
			return "", err
		}
		raw, aad = raw[16:], "tripfolio/webdav/v2"
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		if s.options.Password != "" {
			return "", errors.New("BACKUP_KEY_INVALID")
		}
		return "", errors.New("BACKUP_KEY_MISSING")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < aead.NonceSize()+aead.Overhead() {
		return "", errors.New("BACKUP_KEY_INVALID")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(aad))
	if err != nil {
		return "", errors.New("BACKUP_KEY_INVALID")
	}
	return string(plain), nil
}

func (s *Service) credentialKey(salt []byte) ([]byte, error) {
	if s.options.Password == "" {
		return nil, errors.New("BACKUP_KEY_MISSING")
	}
	key, err := scrypt.Key([]byte(s.options.Password), salt, 1<<17, 8, 1, 32)
	if err != nil {
		return nil, errors.New("BACKUP_KEY_INVALID")
	}
	return key, nil
}

func (s *Service) archiveUsesPassword(filename string) bool {
	f, err := os.Open(filename)
	if err != nil {
		return false
	}
	defer f.Close()
	identity, err := age.NewScryptIdentity(s.passphrase())
	if err != nil {
		return false
	}
	_, err = age.Decrypt(f, identity)
	return err == nil
}

func Decrypt(password string, input io.Reader, output io.Writer) error {
	if password == "" {
		return errors.New("请配置 TRIPFOLIO_BACKUP_PASSWORD 后解密")
	}
	identity, err := age.NewScryptIdentity(password)
	if err != nil {
		return err
	}
	reader, err := age.Decrypt(input, identity)
	if err != nil {
		return errors.New("备份解密失败，请核对备份当时的密码与文件；旧备份需使用原密钥")
	}
	if _, err = io.Copy(output, reader); err != nil {
		return errors.New("备份解密或完整性验证失败")
	}
	return nil
}
