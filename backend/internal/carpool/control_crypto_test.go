package carpool

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCredentialCipherRequiresIndependentRoot(t *testing.T) {
	for _, root := range []string{"", "not-base64", base64.StdEncoding.EncodeToString(make([]byte, 31)), base64.StdEncoding.EncodeToString(make([]byte, 33))} {
		_, err := NewCredentialCipher(root)
		require.ErrorIs(t, err, ErrControlCredential)
		require.NotContains(t, err.Error(), root+"secret")
	}
}

func TestCredentialCipherAuthenticatedRandomizedEnvelope(t *testing.T) {
	root := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	cipher, err := NewCredentialCipher(root)
	require.NoError(t, err)
	binding := CredentialBinding{MainID: "main-a", NodeID: "node-b", Generation: 1}
	secret := "synthetic-admin-key-only"
	first, err := cipher.Seal(binding, secret)
	require.NoError(t, err)
	second, err := cipher.Seal(binding, secret)
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	require.NotContains(t, first, secret)
	opened, err := cipher.Open(binding, first)
	require.NoError(t, err)
	require.Equal(t, secret, opened)
	for _, other := range []CredentialBinding{
		{MainID: "other-main", NodeID: "node-b", Generation: 1},
		{MainID: "main-a", NodeID: "node-c", Generation: 1},
		{MainID: "main-a", NodeID: "node-b", Generation: 2},
	} {
		_, err = cipher.Open(other, first)
		require.ErrorIs(t, err, ErrControlCredential)
	}
	otherCipher, err := NewCredentialCipher(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)))
	require.NoError(t, err)
	_, err = otherCipher.Open(binding, first)
	require.ErrorIs(t, err, ErrControlCredential)
	encoded, err := json.Marshal(map[string]any{"cipher": cipher})
	require.NoError(t, err)
	require.JSONEq(t, `{"cipher":{}}`, string(encoded))
	require.NotContains(t, string(encoded), root)
}

func TestCredentialCipherFailsClosed(t *testing.T) {
	cipher, err := NewCredentialCipher(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	require.NoError(t, err)
	binding := CredentialBinding{MainID: "a", NodeID: "b", Generation: 1}
	sealed, err := cipher.Seal(binding, "test-key")
	require.NoError(t, err)
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(sealed, "v1."))
	require.NoError(t, err)
	payload[len(payload)-1] ^= 1
	for _, value := range []string{"", "v2." + sealed, "v1.invalid!", "v1.AA", "v1." + base64.RawURLEncoding.EncodeToString(payload), strings.Repeat("x", 10000)} {
		plain, err := cipher.Open(binding, value)
		require.ErrorIs(t, err, ErrControlCredential)
		require.Empty(t, plain)
		require.NotContains(t, err.Error(), "test-key")
	}
	for _, invalid := range []CredentialBinding{{NodeID: "b", Generation: 1}, {MainID: "a", Generation: 1}, {MainID: "a", NodeID: "b"}, {MainID: "a", NodeID: "b", Generation: -1}} {
		_, err = cipher.Seal(invalid, "test-key")
		require.ErrorIs(t, err, ErrControlCredential)
	}
	for _, invalid := range []string{"", "key\r\nInjected: value", "has space", strings.Repeat("x", 4097)} {
		_, err = cipher.Seal(binding, invalid)
		require.ErrorIs(t, err, ErrControlCredential)
	}
	var missing *CredentialCipher
	_, err = missing.Open(binding, sealed)
	require.ErrorIs(t, err, ErrControlCredential)
}
