package carpool

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

type ControlConfig struct {
	Enabled        bool     `mapstructure:"enabled"`
	EncryptionKey  string   `mapstructure:"encryption_key" json:"-"`
	AllowedOrigins []string `mapstructure:"allowed_origins"`
}

func (c ControlConfig) Validate(mainEnabled, nodeEnabled bool) error {
	if !c.Enabled {
		return nil
	}
	if !mainEnabled || nodeEnabled {
		return fmt.Errorf("carpool_control requires main mode and excludes node mode")
	}
	if _, err := NewCredentialCipher(c.EncryptionKey); err != nil {
		return fmt.Errorf("carpool_control requires an independent base64-encoded 32-byte encryption_key")
	}
	if _, err := NewControlClient(c.AllowedOrigins); err != nil {
		return fmt.Errorf("carpool_control requires distinct permitted HTTPS origins without paths")
	}
	return nil
}

func (c ControlConfig) ValidateSecrets(jwtSecret, totpHex string) error {
	if !c.Enabled {
		return nil
	}
	key, err := base64.StdEncoding.Strict().DecodeString(c.EncryptionKey)
	if err != nil || len(key) != 32 {
		return ErrControlCredential
	}
	defer clear(key)
	totpKey, _ := hex.DecodeString(totpHex)
	defer clear(totpKey)
	if c.EncryptionKey == jwtSecret || bytes.Equal(key, []byte(jwtSecret)) || bytes.Equal(key, totpKey) {
		return fmt.Errorf("carpool_control encryption_key must be independent of JWT and TOTP keys")
	}
	return nil
}
