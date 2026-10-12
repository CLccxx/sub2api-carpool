//go:build integration

package carpool_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNativeSubscriptionBootstrapAndAccess(t *testing.T) {
	lab := newNativeControlLab(t)
	ctx := context.Background()
	cfg := carpool.Config{Enabled: true, AuthorizationMode: "managed", MainInstanceID: uuid.NewString(), NodeID: uuid.NewString(), CarID: uuid.NewString()}
	node, err := carpool.NewNodeControl(ctx, lab.db, cfg)
	require.NoError(t, err, "auto group mode must start without an existing group")
	var account int64
	require.NoError(t, lab.db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,status,schedulable) VALUES('local OAuth fixture','openai','oauth','{}','active',true) RETURNING id`).Scan(&account))
	setup, err := node.InitializeSubscription(ctx, node.Target(), carpool.NativeSetupInput{Capacity: 4, AccountID: account})
	require.NoError(t, err)
	require.Equal(t, 450.0, setup.WeeklyLimitUSD)
	var name, kind string
	var daily, monthly *float64
	require.NoError(t, lab.db.QueryRow(`SELECT name,subscription_type,daily_limit_usd,monthly_limit_usd FROM groups WHERE id=$1`, setup.GroupID).Scan(&name, &kind, &daily, &monthly))
	require.Equal(t, "4 人车分组", name)
	require.Equal(t, "subscription", kind)
	require.Nil(t, daily)
	require.Nil(t, monthly)
	replay, err := node.InitializeSubscription(ctx, node.Target(), carpool.NativeSetupInput{Capacity: 4, AccountID: account})
	require.NoError(t, err)
	require.Equal(t, setup.GroupID, replay.GroupID)
	node, err = carpool.NewNodeControl(ctx, lab.db, cfg)
	require.NoError(t, err, "auto-discovered group binding survives restart")
	_, err = node.InitializeSubscription(ctx, node.Target(), carpool.NativeSetupInput{Capacity: 3, AccountID: account})
	require.Error(t, err, "retry cannot change car capacity or account")
	subject := uuid.NewString()
	member, err := node.Provision(ctx, node.Target(), subject)
	require.NoError(t, err)
	gate := carpool.NewGate(lab.db, cfg)
	require.Error(t, gate.CheckUser(ctx, member.UserID), "preparation must not enable requests")
	start := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	end := start.Add(30 * 24 * time.Hour)
	grant := carpool.ManagedGrant{Version: 1, NativeSubscription: true, Status: "active", ValidFrom: &start, ValidUntil: &end}
	require.NoError(t, node.ApplyGrant(ctx, node.Target(), subject, grant))
	require.NoError(t, gate.CheckUser(ctx, member.UserID))
	var subscription int64
	var expires time.Time
	require.NoError(t, lab.db.QueryRow(`SELECT id,expires_at FROM user_subscriptions WHERE user_id=$1 AND group_id=$2`, member.UserID, setup.GroupID).Scan(&subscription, &expires))
	require.True(t, end.Equal(expires))
	_, err = lab.db.Exec(`UPDATE user_subscriptions SET weekly_usage_usd=123 WHERE id=$1`, subscription)
	require.NoError(t, err)
	invalidations := 0
	node.SetSubscriptionInvalidator(func(_ context.Context, userID, groupID int64) error {
		require.Equal(t, member.UserID, userID)
		require.Equal(t, setup.GroupID, groupID)
		invalidations++
		if invalidations == 1 {
			return errors.New("synthetic cache outage")
		}
		return nil
	})
	require.ErrorIs(t, node.ApplyGrant(ctx, node.Target(), subject, grant), carpool.ErrUnavailable)
	require.NoError(t, node.ApplyGrant(ctx, node.Target(), subject, grant))
	require.Equal(t, 2, invalidations, "committed retry must repeat native cache invalidation")
	grant.Version++
	require.NoError(t, node.ApplyGrant(ctx, node.Target(), subject, grant))
	var used float64
	require.NoError(t, lab.db.QueryRow(`SELECT weekly_usage_usd,expires_at FROM user_subscriptions WHERE id=$1`, subscription).Scan(&used, &expires))
	require.Equal(t, 123.0, used)
	require.True(t, end.Equal(expires), "retry cannot renew subscription")
	require.NoError(t, gate.CheckUser(ctx, member.UserID), "no lease renewal is necessary")
	_, err = lab.db.Exec(`UPDATE user_subscriptions SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, subscription)
	require.NoError(t, err)
	require.Error(t, gate.CheckUser(ctx, member.UserID), "native expiry is authoritative")
	_, err = lab.db.Exec(`UPDATE user_subscriptions SET expires_at=$2 WHERE id=$1`, subscription, end)
	require.NoError(t, err)
	require.NoError(t, gate.CheckUser(ctx, member.UserID))
	require.NoError(t, node.ApplyGrant(ctx, node.Target(), subject, carpool.ManagedGrant{Version: 3, NativeSubscription: true, Status: "revoked"}))
	require.Error(t, gate.CheckUser(ctx, member.UserID))
	var status string
	require.NoError(t, lab.db.QueryRow(`SELECT status,weekly_usage_usd FROM user_subscriptions WHERE id=$1`, subscription).Scan(&status, &used))
	require.Equal(t, "suspended", status)
	require.Equal(t, 123.0, used)
	require.Error(t, gate.CheckUser(ctx, lab.admin.ID), "admin API keys do not bypass model access")
	wrong := node.Target()
	wrong.CarID = uuid.NewString()
	_, err = node.InitializeSubscription(ctx, wrong, carpool.NativeSetupInput{Capacity: 4, AccountID: account})
	require.Error(t, err)
}

func TestNativeCarPreparationActivationAndRetry(t *testing.T) {
	ctx := context.Background()
	lab := newNativeControlLab(t)
	main := carpool.NewMainService(lab.db, carpool.MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	car, err := main.CreateCar(ctx, lab.admin.ID, carpool.CarInput{Name: "native car", Capacity: 2, PriceMinor: 70000, Currency: "CNY", ServiceDays: 30, RecruitUntil: time.Now().Add(time.Hour), DeliveryHours: 24})
	require.NoError(t, err)
	_, err = main.PublishCar(ctx, lab.admin.ID, car.ID)
	require.NoError(t, err)
	registered, err := main.RegisterNode(ctx, lab.admin.ID, carpool.NodeInput{CarID: car.ID, Hostname: "native.control-lab.invalid"})
	require.NoError(t, err)
	members := []int64{lab.user(t, "user").ID, lab.user(t, "user").ID}
	for _, id := range members {
		_, err = main.EnrollManual(ctx, lab.admin.ID, car.ID, uuid.NewString(), carpool.ManualEnrollmentInput{UserID: id, AmountMinor: 70000, Channel: "wechat", ReceivedAt: time.Now().Add(-time.Minute), Reference: uuid.NewString(), ConfirmReceived: true})
		require.NoError(t, err)
	}
	origin := "https://" + registered.Hostname
	store, err := carpool.NewControlStore(ctx, main, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)), []string{origin})
	require.NoError(t, err)
	roots := x509.NewCertPool()
	b := newNativeControlNode(t, store.MainID(), registered, roots)
	carpool.ConfigureTestTransport(store, &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		require.Equal(t, registered.Hostname+":443", address)
		return (&net.Dialer{}).DialContext(ctx, network, b.server.Listener.Addr().String())
	}})
	// An empty instance ID is discovered and pinned on A, never trusted from the browser.
	_, err = store.SaveConnection(ctx, lab.admin.ID, registered.ID, carpool.ControlConnectionInput{Origin: origin, AdminKey: b.adminKey})
	require.NoError(t, err)
	_, err = store.CheckConnection(ctx, lab.admin.ID, registered.ID)
	require.NoError(t, err)
	var account int64
	require.NoError(t, b.db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,status,schedulable) VALUES('selected OAuth','openai','oauth','{}','active',true) RETURNING id`).Scan(&account))
	_, err = store.ActivateNativeCar(ctx, lab.admin.ID, car.ID, true)
	require.Error(t, err, "cannot activate before native preparation")
	prepared, err := store.PrepareNativeCar(ctx, lab.admin.ID, car.ID, account)
	require.NoError(t, err)
	require.True(t, prepared.CanPrepare)
	require.False(t, prepared.CanActivate)
	require.Equal(t, b.groupID, prepared.GroupID)
	drain := func() {
		require.NoError(t, store.ReconcileControl(ctx))
		for i := 0; i < 6; i++ {
			worked, e := store.SyncNext(ctx)
			require.NoError(t, e)
			if !worked {
				break
			}
		}
	}
	drain()
	var count int
	require.NoError(t, b.db.QueryRow(`SELECT count(*) FROM user_subscriptions`).Scan(&count))
	require.Zero(t, count)
	_, err = store.ActivateNativeCar(ctx, lab.admin.ID, car.ID, false)
	require.Error(t, err, "explicit confirmation is required")
	first, err := store.ActivateNativeCar(ctx, lab.admin.ID, car.ID, true)
	require.NoError(t, err)
	require.NotNil(t, first.ServiceStart)
	require.NotNil(t, first.ServiceEnd)
	require.False(t, first.CanPrepare)
	drain()
	again, err := store.ActivateNativeCar(ctx, lab.admin.ID, car.ID, true)
	require.NoError(t, err)
	require.Equal(t, first.ServiceStart, again.ServiceStart)
	require.Equal(t, first.ServiceEnd, again.ServiceEnd)
	require.NoError(t, b.db.QueryRow(`SELECT count(*) FROM user_subscriptions WHERE status='active'`).Scan(&count))
	require.Equal(t, 2, count)
	key, err := store.CreateMemberKey(ctx, members[0], car.ID, uuid.NewString(), "native test key")
	require.NoError(t, err)
	require.NotEmpty(t, key.Key)
	var userID int64
	require.NoError(t, b.db.QueryRow(`SELECT user_id FROM api_keys WHERE id=$1`, key.ID).Scan(&userID))
	require.NoError(t, b.gate.CheckUser(ctx, userID))
	_, err = b.db.Exec(`UPDATE carpool_managed_members SET lease_until=clock_timestamp()-interval '1 second' WHERE user_id=$1`, userID)
	require.NoError(t, err)
	require.NoError(t, b.gate.CheckUser(ctx, userID), "A going offline must not add a short-lease denial")
	_, err = store.CreateMemberKey(ctx, members[0], car.ID, uuid.NewString(), "native after old lease")
	require.NoError(t, err, "delegated key management must not retain the old lease gate")
	var version int64
	require.NoError(t, lab.db.QueryRow(`SELECT max(version) FROM carpool_control_grants`).Scan(&version))
	drain()
	var after int64
	require.NoError(t, lab.db.QueryRow(`SELECT max(version) FROM carpool_control_grants`).Scan(&after))
	require.Equal(t, version, after, "native grants do not renew every fifteen minutes")
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	estimate := 2000.0
	_, err = b.node.ApplyQuotaObservation(ctx, account, carpool.QuotaObservation{CapturedAt: stamp, ObservedAt: stamp, ResetsAt: stamp.Add(time.Hour), Utilization: 25, EstimatedTotal: &estimate}, true)
	require.NoError(t, err)
	_, err = b.db.Exec(`UPDATE user_subscriptions SET weekly_usage_usd=CASE WHEN user_id=$1 THEN 123 ELSE 456 END`, userID)
	require.NoError(t, err)
	sub, err := store.MemberSubscription(ctx, members[0], car.ID)
	require.NoError(t, err)
	require.Equal(t, 123.0, sub.WeeklyUsedUSD)
	require.Equal(t, 950.0, sub.WeeklyLimitUSD)
	require.Equal(t, 827.0, sub.WeeklyRemainingUSD)
	require.Equal(t, origin+"/v1", sub.APIBaseURL)
	require.Equal(t, "ready", sub.Account.State)
	require.Equal(t, 25.0, *sub.Account.Utilization)
	require.True(t, sub.ExpiresAt.Equal(*first.ServiceEnd))
	otherSub, err := store.MemberSubscription(ctx, members[1], car.ID)
	require.NoError(t, err)
	require.Equal(t, 456.0, otherSub.WeeklyUsedUSD)
	_, err = store.MemberSubscription(ctx, lab.admin.ID, car.ID)
	require.Error(t, err, "a main administrator is not automatically a member of the car")
	quota, err := store.NativeQuota(ctx, lab.admin.ID, car.ID, false)
	require.NoError(t, err)
	require.Equal(t, 950.0, quota.WeeklyLimitUSD)
	_, err = b.db.Exec(`UPDATE groups SET platform='anthropic' WHERE id=$1`, b.groupID)
	require.NoError(t, err)
	sub, err = store.MemberSubscription(ctx, members[0], car.ID)
	require.NoError(t, err)
	require.Equal(t, origin, sub.APIBaseURL, "Anthropic base URL must not append /v1")
}
