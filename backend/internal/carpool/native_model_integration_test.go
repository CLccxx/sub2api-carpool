//go:build integration

package carpool_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNativeSubscriptionProductionLifecycle(t *testing.T) {
	binary := os.Getenv("CARPOOL_MODEL_BINARY")
	if binary == "" {
		t.Skip("set CARPOOL_MODEL_BINARY to the current embedded production server")
	}
	require.True(t, filepath.IsAbs(binary))
	ctx := context.Background()
	directory, err := os.MkdirTemp("", "carpool-native-model-")
	require.NoError(t, err)
	fmt.Fprintln(os.Stdout, "Private native model acceptance logs:", directory)
	var upstreamCount atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			_, _ = io.WriteString(w, `{"gpt-4o-mini":{"input_cost_per_token":0.00000015,"output_cost_per_token":0.0000006,"mode":"chat"}}`)
			return
		}
		upstreamCount.Add(1)
		_, _ = io.WriteString(w, `{"id":"chatcmpl-native","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"local native subscription fixture"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`)
	}))
	t.Cleanup(upstream.Close)
	lab := newNativeControlLab(t)
	main := carpool.NewMainService(lab.db, carpool.MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	members := []*service.User{lab.user(t, "user"), lab.user(t, "user"), lab.user(t, "user"), lab.user(t, "user")}
	makeCar := func(capacity int, host string) (carpool.Car, carpool.Node) {
		total := int64(140000)
		car, e := main.CreateCar(ctx, lab.admin.ID, carpool.CarInput{Name: host, Capacity: capacity, TotalPriceMinor: &total, Currency: "CNY", ServiceDays: 30, RecruitUntil: time.Now().Add(time.Hour), DeliveryHours: 24})
		require.NoError(t, e)
		_, e = main.PublishCar(ctx, lab.admin.ID, car.ID)
		require.NoError(t, e)
		for _, member := range members[:capacity] {
			_, e = main.EnrollManual(ctx, lab.admin.ID, car.ID, uuid.NewString(), carpool.ManualEnrollmentInput{UserID: member.ID, AmountMinor: car.PriceMinor, Channel: "wechat", ReceivedAt: time.Now().Add(-time.Minute), ConfirmReceived: true})
			require.NoError(t, e)
		}
		node, e := main.RegisterNode(ctx, lab.admin.ID, carpool.NodeInput{CarID: car.ID, Hostname: host})
		require.NoError(t, e)
		return car, node
	}
	carB, registeredB := makeCar(4, "b.native-model.invalid")
	carC, registeredC := makeCar(2, "c.native-model.invalid")
	store, err := carpool.NewControlStore(ctx, main, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{6}, 32)), []string{"https://" + registeredB.Hostname, "https://" + registeredC.Hostname})
	require.NoError(t, err)
	roots := x509.NewCertPool()
	b := newNativeControlNode(t, store.MainID(), registeredB, roots)
	c := newNativeControlNode(t, store.MainID(), registeredC, roots)
	addresses := map[string]string{registeredB.Hostname + ":443": b.server.Listener.Addr().String(), registeredC.Hostname + ":443": c.server.Listener.Addr().String()}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		target, ok := addresses[address]
		if !ok {
			return nil, carpool.ErrControlTarget
		}
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	carpool.ConfigureTestTransport(store, transport)
	drain := func() {
		t.Helper()
		require.NoError(t, store.ReconcileControl(ctx))
		require.Eventually(t, func() bool {
			_, e := store.SyncNext(ctx)
			require.NoError(t, e)
			var pending int
			require.NoError(t, lab.db.QueryRow(`SELECT count(*) FROM carpool_control_grants WHERE delivery IN ('pending','leased')`).Scan(&pending))
			return pending == 0
		}, 10*time.Second, 20*time.Millisecond)
	}
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	reset := stamp.Add(2 * 24 * time.Hour)
	writeSnapshot := func(remote nativeControlNode, accountID int64, observed, resets time.Time) {
		t.Helper()
		extra, e := json.Marshal(map[string]any{
			"codex_usage_updated_at": observed, "codex_5h_used_percent": 0, "codex_5h_reset_at": observed.Add(time.Hour),
			"codex_7d_used_percent": 0, "codex_7d_reset_at": resets,
			"codex_7d_observation": map[string]any{"observed_at": observed, "resets_at": resets, "utilization": 0},
		})
		require.NoError(t, e)
		_, e = remote.db.Exec(`UPDATE accounts SET extra=$2::jsonb WHERE id=$1`, accountID, string(extra))
		require.NoError(t, e)
	}
	var accountB int64
	for _, pair := range []struct {
		car        carpool.Car
		registered carpool.Node
		remote     nativeControlNode
	}{{carB, registeredB, b}, {carC, registeredC, c}} {
		_, err = store.SaveConnection(ctx, lab.admin.ID, pair.registered.ID, carpool.ControlConnectionInput{Origin: "https://" + pair.registered.Hostname, AdminKey: pair.remote.adminKey})
		require.NoError(t, err)
		_, err = store.CheckConnection(ctx, lab.admin.ID, pair.registered.ID)
		require.NoError(t, err)
		var account int64
		require.NoError(t, pair.remote.db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,status,schedulable) VALUES('Synthetic OAuth window','openai','oauth','{}','active',true) RETURNING id`).Scan(&account))
		writeSnapshot(pair.remote, account, stamp, reset)
		if pair.car.ID == carB.ID {
			accountB = account
		}
		_, err = store.PrepareNativeCar(ctx, lab.admin.ID, pair.car.ID, account)
		require.NoError(t, err)
		drain()
		_, err = store.CreateMemberKey(ctx, members[0].ID, pair.car.ID, uuid.NewString(), "prepared only")
		require.ErrorIs(t, err, carpool.ErrRequired)
		_, err = store.ActivateNativeCar(ctx, lab.admin.ID, pair.car.ID, true)
		require.NoError(t, err)
		drain()
		var count int
		require.NoError(t, pair.remote.db.QueryRow(`SELECT count(*) FROM user_subscriptions WHERE status='active'`).Scan(&count))
		require.Equal(t, pair.car.Capacity, count)
		// The local transport account added by startProductionModelNode handles all
		// requests; no real OAuth token or external upstream is used by this fixture.
		_, err = pair.remote.db.Exec(`UPDATE accounts SET schedulable=false WHERE id=$1`, account)
		require.NoError(t, err)
	}
	keyB, err := store.CreateMemberKey(ctx, members[0].ID, carB.ID, uuid.NewString(), "native B")
	require.NoError(t, err)
	otherKey, err := store.CreateMemberKey(ctx, members[1].ID, carB.ID, uuid.NewString(), "native B other")
	require.NoError(t, err)
	keyC, err := store.CreateMemberKey(ctx, members[0].ID, carC.ID, uuid.NewString(), "native C")
	require.NoError(t, err)
	modelB := startProductionModelNode(t, b, store.MainID(), registeredB, binary, upstream.URL, filepath.Join(directory, "b"))
	modelC := startProductionModelNode(t, c, store.MainID(), registeredC, binary, upstream.URL, filepath.Join(directory, "c"))
	client := &http.Client{Timeout: 15 * time.Second}
	call := func(site, key string, want int) string {
		t.Helper()
		request, e := http.NewRequest(http.MethodPost, site+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"native local fixture"}],"stream":false}`))
		require.NoError(t, e)
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("Content-Type", "application/json")
		result, e := client.Do(request)
		require.NoError(t, e)
		body, e := io.ReadAll(io.LimitReader(result.Body, 65536))
		_ = result.Body.Close()
		require.NoError(t, e)
		require.Equal(t, want, result.StatusCode, string(body))
		return string(body)
	}
	call(modelB.url, keyB.Key, 200)
	call(modelB.url, otherKey.Key, 200)
	call(modelC.url, keyC.Key, 200)
	call(modelB.url, keyC.Key, 401)
	call(modelC.url, keyB.Key, 401)
	waitBilled := func(remote nativeControlNode, keyID, afterID int64, want int) (float64, int64) {
		t.Helper()
		var used, cost float64
		var lastID int64
		require.Eventually(t, func() bool {
			var count int
			e := remote.db.QueryRow(`SELECT count(*),coalesce(sum(actual_cost),0),coalesce(max(id),0) FROM usage_logs WHERE api_key_id=$1 AND id>$2`, keyID, afterID).Scan(&count, &cost, &lastID)
			if e != nil || count != want || cost <= 0 {
				return false
			}
			e = remote.db.QueryRow(`SELECT s.weekly_usage_usd FROM user_subscriptions s JOIN api_keys k ON k.user_id=s.user_id AND k.group_id=s.group_id WHERE k.id=$1`, keyID).Scan(&used)
			return e == nil && math.Abs(used-cost) < 1e-10
		}, 10*time.Second, 50*time.Millisecond, "both expected native records and subscription charges must be committed")
		return used, lastID
	}
	waitBilled(b, keyB.ID, 0, 1)
	waitBilled(b, otherKey.ID, 0, 1)
	query := carpool.ControlUsageQuery{ControlPageQuery: carpool.ControlPageQuery{Page: 1, PageSize: 20}, StartDate: time.Now().UTC().Format(time.DateOnly), EndDate: time.Now().UTC().Format(time.DateOnly)}
	snapshot, err := store.MemberSubscription(ctx, members[0].ID, carB.ID)
	require.NoError(t, err)
	require.Equal(t, 450.0, snapshot.WeeklyLimitUSD)
	initialUsed, initialExpiry := snapshot.WeeklyUsedUSD, snapshot.ExpiresAt
	records, err := store.MemberUsage(ctx, members[0].ID, carB.ID, query)
	require.NoError(t, err)
	require.Len(t, records.Items, 1)
	require.EqualValues(t, 11, records.Items[0].InputTokens)
	var balance float64
	require.NoError(t, b.db.QueryRow(`SELECT sum(u.balance) FROM users u JOIN carpool_managed_members m ON m.user_id=u.id`).Scan(&balance))
	require.Zero(t, balance, "native subscription requests must not require a test balance top-up")

	// Use the production process's actual Redis caches for cross-process updates.
	nativeCfg := carpool.Config{Enabled: true, AuthorizationMode: "managed", MainInstanceID: store.MainID(), NodeID: registeredB.ID, CarID: carB.ID, GroupID: b.groupID}
	controller, err := carpool.NewNodeControl(ctx, b.db, nativeCfg)
	require.NoError(t, err)
	keyRepo := repository.NewAPIKeyRepository(b.ent, b.db)
	subRepo := repository.NewUserSubscriptionRepository(b.ent)
	groups := repository.NewGroupRepository(b.ent, b.db)
	cacheCfg := &config.Config{}
	api := service.NewAPIKeyService(keyRepo, b.users, groups, subRepo, nil, repository.NewAPIKeyCache(modelB.cache), cacheCfg)
	billing := service.NewBillingCacheService(repository.NewBillingCache(modelB.cache), b.users, subRepo, keyRepo, nil, nil, cacheCfg, nil)
	t.Cleanup(billing.Stop)
	subscriptions := service.NewSubscriptionService(groups, subRepo, billing, b.ent, nil)
	t.Cleanup(subscriptions.Stop)
	managedKeys := service.ProvideCarpoolManagedKeys(controller, b.ent, api, subscriptions)
	applyEstimate := func(total float64) {
		t.Helper()
		now := time.Now().UTC()
		_, e := controller.ApplyQuotaObservation(ctx, accountB, carpool.QuotaObservation{CapturedAt: now, ObservedAt: now, ResetsAt: reset, Utilization: 25, EstimatedTotal: &total}, true)
		require.NoError(t, e)
	}
	applyEstimate(2000)
	snapshot, err = store.MemberSubscription(ctx, members[0].ID, carB.ID)
	require.NoError(t, err)
	require.Equal(t, 475.0, snapshot.WeeklyLimitUSD)
	require.Equal(t, initialUsed, snapshot.WeeklyUsedUSD)
	require.True(t, initialExpiry.Equal(snapshot.ExpiresAt))
	applyEstimate(100.000001)
	before := upstreamCount.Load()
	require.Contains(t, call(modelB.url, keyB.Key, 429), "USAGE_LIMIT_EXCEEDED")
	require.Equal(t, before, upstreamCount.Load(), "a rejected request must not reach the model upstream")
	call(modelC.url, keyC.Key, 200)
	applyEstimate(2000)
	call(modelB.url, keyB.Key, 200)
	_, beforeResetID := waitBilled(b, keyB.ID, 0, 2)
	waitBilled(b, otherKey.ID, 0, 1)

	// Controlled time travel changes only period metadata, never usage/history.
	_, err = b.db.Exec(`UPDATE carpool_native_setup SET cycle_reset_at=clock_timestamp()-interval '1 minute',observed_at=clock_timestamp()-interval '2 minutes',last_read_at=NULL; UPDATE user_subscriptions SET weekly_window_start=clock_timestamp()-interval '8 days'`)
	require.NoError(t, err)
	stamp = time.Now().UTC().Truncate(time.Microsecond)
	reset = stamp.Add(7 * 24 * time.Hour)
	writeSnapshot(b, accountB, stamp, reset)
	observation := carpool.QuotaObservation{CapturedAt: stamp, ObservedAt: stamp, ResetsAt: reset, Utilization: 0}
	_, err = controller.ApplyQuotaObservation(ctx, accountB, observation, false)
	require.NoError(t, err)
	var nonzero int
	require.NoError(t, b.db.QueryRow(`SELECT count(*) FROM user_subscriptions WHERE weekly_usage_usd<>0`).Scan(&nonzero))
	require.Zero(t, nonzero)
	call(modelB.url, keyB.Key, 200)
	charged, _ := waitBilled(b, keyB.ID, beforeResetID, 1)
	_, err = controller.ApplyQuotaObservation(ctx, accountB, observation, true)
	require.NoError(t, err)
	snapshot, err = store.MemberSubscription(ctx, members[0].ID, carB.ID)
	require.NoError(t, err)
	require.Equal(t, charged, snapshot.WeeklyUsedUSD)
	require.True(t, initialExpiry.Equal(snapshot.ExpiresAt))
	records, err = store.MemberUsage(ctx, members[0].ID, carB.ID, query)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(records.Items), 3, "cycle resets must retain native request history")
	var otherSubject string
	require.NoError(t, b.db.QueryRow(`SELECT subject_id::text FROM carpool_managed_members WHERE user_id=(SELECT user_id FROM api_keys WHERE id=$1)`, otherKey.ID).Scan(&otherSubject))
	disabled := "disabled"
	_, err = managedKeys.Update(ctx, controller.Target(), otherSubject, otherKey.ID, carpool.ManagedKeyUpdate{Status: &disabled})
	require.NoError(t, err)
	before = upstreamCount.Load()
	call(modelB.url, otherKey.Key, 401)
	require.Equal(t, before, upstreamCount.Load())
	active := "active"
	_, err = managedKeys.Update(ctx, controller.Target(), otherSubject, otherKey.ID, carpool.ManagedKeyUpdate{Status: &active})
	require.NoError(t, err)
	call(modelB.url, otherKey.Key, 200)
	require.NoError(t, managedKeys.Delete(ctx, controller.Target(), otherSubject, otherKey.ID))
	before = upstreamCount.Load()
	call(modelB.url, otherKey.Key, 401)
	require.Equal(t, before, upstreamCount.Load())

	_, err = lab.db.Exec(`UPDATE carpool_memberships SET status='revoked' WHERE car_id=$1`, carC.ID)
	require.NoError(t, err)
	drain()
	before = upstreamCount.Load()
	call(modelC.url, keyC.Key, 403)
	require.Equal(t, before, upstreamCount.Load())

	// A is actually unavailable, not merely idle; B must not need lease renewal.
	require.NoError(t, lab.db.Close())
	require.Error(t, lab.db.PingContext(ctx))
	_, err = b.db.Exec(`UPDATE carpool_managed_members SET lease_until=clock_timestamp()-interval '1 hour'`)
	require.NoError(t, err)
	call(modelB.url, keyB.Key, 200)
	_, err = b.db.Exec(`UPDATE user_subscriptions SET expires_at=clock_timestamp()-interval '1 second'`)
	require.NoError(t, err)
	before = upstreamCount.Load()
	call(modelB.url, keyB.Key, 403)
	require.Equal(t, before, upstreamCount.Load(), "native expiry remains enforced while A is unavailable")
	fmt.Fprintln(os.Stdout, "PASS native initialization/activation, zero-balance subscription billing, native records, 450 to 475, decreased quota enforcement, cycle reset/replay, key disable/delete, cross-car isolation, revocation, A database outage and native expiry")
}
