package carpool

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func controlTestDial(address string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
}

func TestControlMembersClientFixedTargetAndReplay(t *testing.T) {
	target := ManagedTarget{MainID: uuid.NewString(), NodeID: uuid.NewString(), CarID: uuid.NewString(), InstanceID: uuid.NewString()}
	subject := uuid.NewString()
	var calls int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "PUT", r.Method)
		require.Equal(t, "synthetic-key", r.Header.Get("x-api-key"))
		require.Equal(t, target.MainID, r.Header.Get("X-Carpool-Main-ID"))
		require.Equal(t, target.NodeID, r.Header.Get("X-Carpool-Node-ID"))
		require.Equal(t, target.CarID, r.Header.Get("X-Carpool-Car-ID"))
		require.Equal(t, target.InstanceID, r.Header.Get("X-Carpool-Instance-ID"))
		require.Empty(t, r.URL.RawQuery)
		if strings.HasSuffix(r.URL.Path, "/grant") {
			require.Equal(t, controlPrefix+"/members/"+subject+"/grant", r.URL.Path)
			var grant ManagedGrant
			require.NoError(t, json.NewDecoder(r.Body).Decode(&grant))
			require.Equal(t, ManagedGrant{Version: 1, Status: "prepared"}, grant)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"version": grant.Version, "status": grant.Status}})
			return
		}
		require.Equal(t, controlPrefix+"/members/"+subject, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": ManagedMember{Subject: subject, UserID: 101, GroupID: 7, Status: "prepared"}})
	}))
	defer server.Close()
	origin := "https://" + server.Certificate().DNSNames[0]
	client, err := NewControlClient([]string{origin})
	require.NoError(t, err)
	transport, ok := server.Client().Transport.(*http.Transport)
	require.True(t, ok)
	testTransport := transport.Clone()
	testTransport.Proxy = nil
	testTransport.DialContext = controlTestDial(server.Listener.Addr().String())
	client.http.Transport = testTransport
	for range 2 {
		member, err := client.Provision(context.Background(), origin, "synthetic-key", target, subject)
		require.NoError(t, err)
		require.EqualValues(t, 101, member.UserID)
	}
	require.NoError(t, client.ApplyGrant(context.Background(), origin, "synthetic-key", target, subject, ManagedGrant{Version: 1, Status: "prepared"}))
	_, err = client.Provision(context.Background(), origin, "synthetic-key", target, "../users/1")
	require.ErrorIs(t, err, ErrMainInvalid)
	bad := target
	bad.InstanceID = ""
	_, err = client.Provision(context.Background(), origin, "synthetic-key", bad, subject)
	require.ErrorIs(t, err, ErrControlTarget)
	require.Equal(t, 3, calls)
}

func TestControlMembersReportsInvalidGrantWindow(t *testing.T) {
	target := ManagedTarget{MainID: uuid.NewString(), NodeID: uuid.NewString(), CarID: uuid.NewString(), InstanceID: uuid.NewString()}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":400,"reason":"CARPOOL_GRANT_WINDOW","message":"private node clock detail"}`))
	}))
	defer server.Close()
	origin := "https://" + server.Certificate().DNSNames[0]
	client, err := NewControlClient([]string{origin})
	require.NoError(t, err)
	transport, ok := server.Client().Transport.(*http.Transport)
	require.True(t, ok)
	local := transport.Clone()
	local.DialContext = controlTestDial(server.Listener.Addr().String())
	client.http.Transport = local
	err = client.ApplyGrant(context.Background(), origin, "synthetic-key", target, uuid.NewString(), ManagedGrant{Version: 1, Status: "active"})
	require.ErrorIs(t, err, ErrControlGrantWindow)
	require.NotContains(t, err.Error(), "private node clock detail")
}
