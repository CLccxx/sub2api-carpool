package carpool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestControlReadQueriesScopedAndWhitelisted(t *testing.T) {
	target := ManagedTarget{MainID: uuid.NewString(), NodeID: uuid.NewString(), CarID: uuid.NewString(), InstanceID: uuid.NewString()}
	wrongOwner, wrongInstance := false, false
	boundReads := true
	var dataCalls int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == controlPrefix+"/info" {
			info := ControlInfo{Protocol: 1, MainID: target.MainID, NodeID: target.NodeID, CarID: target.CarID, InstanceID: target.InstanceID, Capabilities: []string{"connection-check"}}
			if boundReads {
				info.Capabilities = append(info.Capabilities, "bound-admin-reads")
			}
			if wrongInstance {
				info.InstanceID = uuid.NewString()
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": info})
			return
		}
		dataCalls++
		require.Equal(t, "GET", r.Method)
		require.Empty(t, r.URL.Query().Get("account_id"))
		user := int64(101)
		if wrongOwner {
			user = 202
		}
		var data any
		switch r.URL.Path {
		case "/api/v1/admin/users/101/api-keys":
			require.Equal(t, "20", r.URL.Query().Get("page_size"))
			data = map[string]any{"items": []any{map[string]any{"id": 7, "user_id": user, "group_id": 9, "name": "one", "status": "active", "key": "synthetic-private-key", "ip_address": "private-ip", "expires_at": time.Now().UTC()}}, "page": 1, "page_size": 20, "total": 1, "pages": 1}
		case "/api/v1/admin/usage", "/api/v1/admin/usage/stats":
			require.Equal(t, "101", r.URL.Query().Get("user_id"))
			require.Equal(t, "9", r.URL.Query().Get("group_id"))
			require.Equal(t, "2026-09-01", r.URL.Query().Get("start_date"))
			require.Equal(t, "2026-09-19", r.URL.Query().Get("end_date"))
			require.Equal(t, "UTC", r.URL.Query().Get("timezone"))
			if strings.HasSuffix(r.URL.Path, "/stats") {
				data = map[string]any{"total_requests": 1, "total_tokens": 15, "total_input_tokens": 10, "total_output_tokens": 5, "total_cost": 0.2, "total_actual_cost": 0.1, "total_account_cost": 999, "upstream_endpoints": []string{"private-endpoint"}}
			} else {
				data = map[string]any{"items": []any{map[string]any{"id": 12, "user_id": user, "group_id": 9, "api_key_id": 7, "model": "safe-model", "input_tokens": 10, "output_tokens": 5, "total_cost": 0.2, "actual_cost": 0.1, "created_at": "2026-09-19T00:00:00Z", "account_id": 99, "user": map[string]any{"email": "private-email"}, "upstream_model": "private-upstream", "request_body": "private-prompt"}}, "page": 1, "page_size": 20, "total": 1, "pages": 1}
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
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
	page := ControlPageQuery{Page: 1, PageSize: 20}
	query := ControlUsageQuery{ControlPageQuery: page, StartDate: "2026-09-01", EndDate: "2026-09-19"}
	keyPage, err := client.ListMemberKeys(ctx, origin, "synthetic-admin", target, 101, 9, page)
	require.NoError(t, err)
	require.Len(t, keyPage.Items, 1)
	require.Empty(t, keyPage.Items[0].Key)
	usage, err := client.MemberUsage(ctx, origin, "synthetic-admin", target, 101, 9, query)
	require.NoError(t, err)
	require.Len(t, usage.Items, 1)
	require.Equal(t, "safe-model", usage.Items[0].Model)
	stats, err := client.MemberUsageStats(ctx, origin, "synthetic-admin", target, 101, 9, query)
	require.NoError(t, err)
	require.EqualValues(t, 1, stats.TotalRequests)
	encoded, err := json.Marshal([]any{keyPage, usage, stats})
	require.NoError(t, err)
	for _, secret := range []string{"synthetic-private", "private-", "account_id", "user_id", "group_id", "upstream", "request_body"} {
		require.NotContains(t, string(encoded), secret)
	}
	wrongOwner = true
	_, err = client.ListMemberKeys(ctx, origin, "synthetic-admin", target, 101, 9, page)
	require.ErrorIs(t, err, ErrControlProtocol)
	_, err = client.MemberUsage(ctx, origin, "synthetic-admin", target, 101, 9, query)
	require.ErrorIs(t, err, ErrControlProtocol)
	wrongOwner = false
	wrongInstance = true
	before := dataCalls
	_, err = client.MemberUsage(ctx, origin, "synthetic-admin", target, 101, 9, query)
	require.ErrorIs(t, err, ErrControlTarget)
	require.Equal(t, before, dataCalls, "do not read native user records after an instance mismatch")
	wrongInstance = false
	boundReads = false
	_, err = client.MemberUsage(ctx, origin, "synthetic-admin", target, 101, 9, query)
	require.ErrorIs(t, err, ErrControlProtocol)
	require.Equal(t, before, dataCalls, "an old node which ignores binding headers cannot serve delegated native reads")
	_, err = client.ListMemberKeys(ctx, origin, "synthetic-admin", target, 0, 9, page)
	require.ErrorIs(t, err, ErrMainInvalid)
	_, err = client.ListMemberKeys(ctx, origin, "synthetic-admin", target, 101, 9, ControlPageQuery{Page: 1, PageSize: 101})
	require.ErrorIs(t, err, ErrMainInvalid)
	query.StartDate = "2026-01-01"
	_, err = client.MemberUsageStats(ctx, origin, "synthetic-admin", target, 101, 9, query)
	require.ErrorIs(t, err, ErrMainInvalid)
	query.StartDate = "../users"
	_, err = client.MemberUsage(ctx, origin, "synthetic-admin", target, 101, 9, query)
	require.ErrorIs(t, err, ErrMainInvalid)
}
