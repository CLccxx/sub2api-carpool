package carpool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNativeQuotaClientRejectsMissingUsageAndPrivateFields(t *testing.T) {
	target := ManagedTarget{MainID: uuid.NewString(), NodeID: uuid.NewString(), CarID: uuid.NewString(), InstanceID: uuid.NewString()}
	subject := uuid.NewString()
	var body atomic.Value
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, target.CarID, r.Header.Get(ControlHeaderCar))
		require.Equal(t, controlPrefix+"/members/"+subject+"/subscription", r.URL.Path)
		require.Empty(t, r.URL.RawQuery)
		payload, ok := body.Load().([]byte)
		require.True(t, ok)
		_, _ = w.Write(payload)
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
	now := time.Now().UTC()
	valid := NativeMemberSubscription{GroupName: "native group", Platform: "openai", Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), WeeklyUsedUSD: 123, WeeklyLimitUSD: 475, WeeklyRemainingUSD: 352, APIBaseURL: "https://untrusted.example", ServerTime: now, Account: NativeQuotaStatus{WeeklyLimitUSD: 475, State: "unavailable"}}
	encode := func(change func(map[string]any)) {
		raw, e := json.Marshal(valid)
		require.NoError(t, e)
		var data map[string]any
		require.NoError(t, json.Unmarshal(raw, &data))
		data["admin_key"] = "do-not-forward"
		change(data)
		raw, e = json.Marshal(map[string]any{"code": 0, "data": data})
		require.NoError(t, e)
		body.Store(raw)
	}
	encode(func(map[string]any) {})
	result, err := client.MemberSubscription(context.Background(), origin, "synthetic-admin", target, subject)
	require.NoError(t, err)
	require.Equal(t, origin+"/v1", result.APIBaseURL)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "do-not-forward")
	for _, change := range []func(map[string]any){
		func(data map[string]any) { delete(data, "weekly_used_usd") },
		func(data map[string]any) { data["weekly_used_usd"] = nil },
		func(data map[string]any) { delete(data, "account") },
		func(data map[string]any) {
			data["account"] = map[string]any{"weekly_limit_usd": 475, "state": "invented"}
		},
		func(data map[string]any) { data["weekly_remaining_usd"] = 999 },
		func(data map[string]any) { data["server_time"] = nil },
	} {
		encode(change)
		_, err = client.MemberSubscription(context.Background(), origin, "synthetic-admin", target, subject)
		require.ErrorIs(t, err, ErrControlProtocol, "missing data must never become zero usage")
	}
}
