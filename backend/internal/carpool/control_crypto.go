package carpool

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrControlCredential = apperrors.ServiceUnavailable("CARPOOL_CONTROL_CREDENTIAL", "node credential unavailable")

type CredentialBinding struct {
	MainID     string `json:"main_id"`
	NodeID     string `json:"node_id"`
	Generation int64  `json:"generation"`
}

// CredentialCipher deliberately has no exported key material or plaintext fallback.
type CredentialCipher struct{ aead cipher.AEAD }

func NewCredentialCipher(root string) (*CredentialCipher, error) {
	key, err := base64.StdEncoding.Strict().DecodeString(root)
	if err != nil || len(key) != 32 {
		return nil, ErrControlCredential
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrControlCredential
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrControlCredential
	}
	return &CredentialCipher{aead: aead}, nil
}

func (b CredentialBinding) aad() ([]byte, error) {
	if strings.TrimSpace(b.MainID) == "" || len(b.MainID) > 128 || strings.TrimSpace(b.NodeID) == "" || len(b.NodeID) > 128 || b.Generation <= 0 {
		return nil, ErrControlCredential
	}
	encoded, err := json.Marshal(b)
	if err != nil {
		return nil, ErrControlCredential
	}
	return append([]byte("carpool-control-credential/v1:"), encoded...), nil
}

func validControlCredential(value string) bool {
	if len(value) == 0 || len(value) > 4096 {
		return false
	}
	for i := range len(value) {
		if value[i] < 33 || value[i] > 126 {
			return false
		}
	}
	return true
}

func (c *CredentialCipher) Seal(binding CredentialBinding, secret string) (string, error) {
	if c == nil || c.aead == nil || !validControlCredential(secret) {
		return "", ErrControlCredential
	}
	aad, err := binding.aad()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", ErrControlCredential
	}
	plain := []byte(secret)
	defer clear(plain)
	sealed := c.aead.Seal(nonce, nonce, plain, aad)
	return "v1." + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *CredentialCipher) Open(binding CredentialBinding, envelope string) (string, error) {
	if c == nil || c.aead == nil || !strings.HasPrefix(envelope, "v1.") || len(envelope) > 6000 {
		return "", ErrControlCredential
	}
	aad, err := binding.aad()
	if err != nil {
		return "", err
	}
	sealed, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(envelope, "v1."))
	if err != nil || len(sealed) < c.aead.NonceSize()+c.aead.Overhead() {
		return "", ErrControlCredential
	}
	nonce := sealed[:c.aead.NonceSize()]
	plain, err := c.aead.Open(nil, nonce, sealed[c.aead.NonceSize():], aad)
	if err != nil {
		return "", ErrControlCredential
	}
	defer clear(plain)
	value := string(plain)
	if !validControlCredential(value) {
		return "", ErrControlCredential
	}
	return value, nil
}
