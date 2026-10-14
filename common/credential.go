package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
)

const credentialPrefix = "enc:v1:"

func ValidateEncryptionKey() error {
	_, err := credentialCipher()
	return err
}

func credentialCipher() (cipher.AEAD, error) {
	key, err := base64.StdEncoding.DecodeString(os.Getenv("ENCRYPTION_KEY"))
	if err != nil || len(key) != 32 {
		return nil, errors.New("ENCRYPTION_KEY must be a base64 encoded 32-byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func EncryptCredential(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, credentialPrefix) {
		if _, err := DecryptCredential(value); err != nil {
			return "", err
		}
		return value, nil
	}
	aead, err := credentialCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(value), []byte(credentialPrefix))
	return credentialPrefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Legacy plaintext values remain readable during an incremental upgrade.
func DecryptCredential(value string) (string, error) {
	if !strings.HasPrefix(value, credentialPrefix) {
		return value, nil
	}
	aead, err := credentialCipher()
	if err != nil {
		return "", err
	}
	sealed, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(value, credentialPrefix))
	if err != nil || len(sealed) < aead.NonceSize() {
		return "", errors.New("invalid encrypted credential")
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(credentialPrefix))
	if err != nil {
		return "", errors.New("credential authentication failed")
	}
	return string(plain), nil
}
