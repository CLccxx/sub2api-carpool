package carpool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestControlKeysClientCommands(t *testing.T) {
	target := ManagedTarget{MainID: uuid.NewString(), NodeID: uuid.NewString(), CarID: uuid.NewString(), InstanceID: uuid.NewString()}
	subject, operation := uuid.NewString(), uuid.NewString()
	var calls int
	expires := time.Now().UTC().Add(time.Hour)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, target.MainID, r.Header.Get(ControlHeaderMain))
		require.Equal(t, target.NodeID, r.Header.Get(ControlHeaderNode))
		require.Equal(t, target.CarID, r.Header.Get(ControlHeaderCar))
		require.Equal(t, target.InstanceID, r.Header.Get(ControlHeaderInstance))
		require.Equal(t, "synthetic-admin", r.Header.Get("x-api-key"))
		require.Empty(t, r.URL.RawQuery)
		if r.Method == "POST" {
			require.Equal(t, operation, r.Header.Get("Idempotency-Key"))
			require.Equal(t, controlPrefix+"/members/"+subject+"/keys", r.URL.Path)
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, map[string]any{"name": "my key"}, body)
		} else {
			require.Equal(t, controlPrefix+"/members/"+subject+"/keys/7", r.URL.Path)
			require.Empty(t, r.Header.Get("Idempotency-Key"))
		}
		if r.Method == "DELETE" {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"id": 7, "deleted": true}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"id": 7, "name": "my key", "status": "active", "expires_at": expires, "key": "synthetic-created", "admin_key": "do-not-forward"}})
	}))
	defer server.Close()
	origin := "https://" + server.Certificate().DNSNames[0]
	client, err := NewControlClient([]string{origin})
	require.NoError(t, err)
	baseTransport, ok := server.Client().Transport.(*http.Transport)
	require.True(t, ok)
	transport := baseTransport.Clone()
	transport.Proxy = nil
	transport.DialContext = controlTestDial(server.Listener.Addr().String())
	client.http.Transport = transport
	ctx := context.Background()
	key, err := client.CreateKey(ctx, origin, "synthetic-admin", target, subject, operation, "my key")
	require.NoError(t, err)
	require.Equal(t, "synthetic-created", key.Key)
	name := "renamed"
	key, err = client.UpdateKey(ctx, origin, "synthetic-admin", target, subject, 7, ManagedKeyUpdate{Name: &name})
	require.NoError(t, err)
	require.Empty(t, key.Key, "mutation results must never forward a secret")
	require.NoError(t, client.DeleteKey(ctx, origin, "synthetic-admin", target, subject, 7))
	_, err = client.CreateKey(ctx, origin, "synthetic-admin", target, subject, "injected\r\nheader", "my key")
	require.ErrorIs(t, err, ErrMainInvalid)
	_, err = client.UpdateKey(ctx, origin, "synthetic-admin", target, "../users", 7, ManagedKeyUpdate{Name: &name})
	require.ErrorIs(t, err, ErrMainInvalid)
	require.ErrorIs(t, client.DeleteKey(ctx, origin, "synthetic-admin", target, subject, 0), ErrMainInvalid)
	require.Equal(t, 3, calls)
}

func TestControlCommandErrorsAreTypedAndSanitized(t *testing.T) {
	for _, tc := range []struct {
		status int
		reason string
		want   error
	}{
		{409, "CARPOOL_KEY_GONE", ErrManagedKeyGone},
		{409, "CARPOOL_CONFLICT", ErrMainConflict},
		{409, "NODE_MEMBERSHIP_VERSION_CONFLICT", ErrVersionConflict},
		{404, "CARPOOL_NOT_FOUND", ErrMainNotFound},
		{403, "NODE_MEMBERSHIP_REQUIRED", ErrRequired},
		{400, "CARPOOL_CONTROL_TARGET", ErrControlTarget},
		{403, "ADMIN_REQUIRED", ErrControlAuth},
		{401, "CARPOOL_KEY_GONE", ErrControlAuth},
		{409, "unknown", ErrControlProtocol},
	} {
		t.Run(tc.reason+strconv.Itoa(tc.status), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]any{"code": tc.status, "reason": tc.reason, "message": "synthetic-secret-database-trace", "data": "synthetic-secret"})
			}))
			defer server.Close()
			origin := "https://" + server.Certificate().DNSNames[0]
			client, err := NewControlClient([]string{origin})
			require.NoError(t, err)
			baseTransport, ok := server.Client().Transport.(*http.Transport)
			require.True(t, ok)
			transport := baseTransport.Clone()
			transport.Proxy = nil
			transport.DialContext = controlTestDial(server.Listener.Addr().String())
			client.http.Transport = transport
			target := ManagedTarget{MainID: uuid.NewString(), NodeID: uuid.NewString(), CarID: uuid.NewString(), InstanceID: uuid.NewString()}
			err = client.DeleteKey(context.Background(), origin, "synthetic-admin", target, uuid.NewString(), 7)
			require.ErrorIs(t, err, tc.want)
			require.NotContains(t, err.Error(), "synthetic-secret")
		})
	}
}
