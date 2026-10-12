//go:build integration

package carpool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type memberRemoteStub struct {
	controlNodeClient
	mu       sync.Mutex
	members  map[string]ManagedMember
	keys     map[string]ManagedKey
	calls    int
	lost     bool
	expires  time.Time
	onCreate func()
}

func (f *memberRemoteStub) Provision(_ context.Context, _, _ string, _ ManagedTarget, subject string) (ManagedMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.members[subject]; ok {
		return m, nil
	}
	m := ManagedMember{Subject: subject, UserID: int64(101 + len(f.members)), GroupID: 7, Status: "prepared"}
	f.members[subject] = m
	return m, nil
}
func (f *memberRemoteStub) CreateKey(_ context.Context, origin, credential string, target ManagedTarget, subject, op, name string) (ManagedKey, error) {
	f.mu.Lock()
	f.calls++
	if origin != "https://node.example" || credential != "synthetic-control-key" {
		f.mu.Unlock()
		return ManagedKey{}, ErrControlTarget
	}
	key, ok := f.keys[subject+op]
	if !ok {
		key = ManagedKey{ID: int64(1000 + len(f.keys)), Name: name, Status: "active", Key: "synthetic-key-" + op, ExpiresAt: &f.expires}
		f.keys[subject+op] = key
	}
	callback, lost := f.onCreate, f.lost
	f.lost = false
	f.mu.Unlock()
	if callback != nil {
		callback()
	}
	if lost {
		return ManagedKey{}, ErrControlTransport
	}
	return key, nil
}

func (f *memberRemoteStub) UpdateKey(_ context.Context, _, _ string, _ ManagedTarget, subject string, id int64, input ManagedKeyUpdate) (ManagedKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for op, key := range f.keys {
		if key.ID == id && strings.HasPrefix(op, subject) {
			if input.Name != nil {
				key.Name = *input.Name
			}
			if input.Status != nil {
				key.Status = *input.Status
			}
			f.keys[op] = key
			return key, nil
		}
	}
	return ManagedKey{}, ErrMainNotFound
}
func (f *memberRemoteStub) DeleteKey(_ context.Context, _, _ string, _ ManagedTarget, subject string, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for op, key := range f.keys {
		if key.ID == id && strings.HasPrefix(op, subject) {
			key.Status = "deleted"
			key.Key = ""
			f.keys[op] = key
			return nil
		}
	}
	return ErrMainNotFound
}
func (f *memberRemoteStub) ListMemberKeys(_ context.Context, _, _ string, _ ManagedTarget, userID, groupID int64, q ControlPageQuery) (ControlPage[ManagedKey], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	page := ControlPage[ManagedKey]{Items: []ManagedKey{}, Page: q.Page, PageSize: q.PageSize}
	for subject, m := range f.members {
		if m.UserID == userID && m.GroupID == groupID {
			for op, key := range f.keys {
				if strings.HasPrefix(op, subject) && key.Status != "deleted" {
					page.Items = append(page.Items, key)
				}
			}
		}
	}
	page.Total = int64(len(page.Items))
	if page.Total > 0 {
		page.Pages = 1
	}
	return page, nil
}
func (f *memberRemoteStub) MemberUsage(_ context.Context, _, _ string, _ ManagedTarget, userID, groupID int64, q ControlUsageQuery) (ControlPage[ManagedUsage], error) {
	return ControlPage[ManagedUsage]{Items: []ManagedUsage{{ID: 1, Model: "synthetic", InputTokens: userID}}, Total: 1, Page: q.Page, PageSize: q.PageSize, Pages: 1}, nil
}
func (f *memberRemoteStub) MemberUsageStats(_ context.Context, _, _ string, _ ManagedTarget, userID, groupID int64, q ControlUsageQuery) (ManagedUsageStats, error) {
	return ManagedUsageStats{TotalRequests: userID}, nil
}

func mainControlFixture(t *testing.T) (*ControlStore, *memberRemoteStub, Car, Node) {
	t.Helper()
	db := mainTestDB(t)
	ctx := context.Background()
	main := NewMainService(db, MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	car := publishedCar(t, main, 2)
	node, err := main.RegisterNode(ctx, 1, NodeInput{CarID: car.ID, Hostname: "node.example"})
	require.NoError(t, err)
	_, err = main.EnrollManual(ctx, 1, car.ID, "first", manualInput(2, "first"))
	require.NoError(t, err)
	_, err = main.EnrollManual(ctx, 1, car.ID, "second", manualInput(3, "second"))
	require.NoError(t, err)
	store, err := NewControlStore(ctx, main, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)), []string{"https://node.example", "https://node2.example"})
	require.NoError(t, err)
	conn, err := store.SaveConnection(ctx, 1, node.ID, ControlConnectionInput{Origin: "https://node.example", InstanceID: uuid.NewString(), AdminKey: "synthetic-control-key"})
	require.NoError(t, err)
	store.remote = &controlInfoStub{info: ControlInfo{Protocol: 1, MainID: store.MainID(), NodeID: node.ID, CarID: car.ID, InstanceID: conn.InstanceID, Capabilities: []string{"connection-check"}}}
	_, err = store.CheckConnection(ctx, 1, node.ID)
	require.NoError(t, err)
	remote := &memberRemoteStub{members: map[string]ManagedMember{}, keys: map[string]ManagedKey{}}
	store.nodeClient = remote
	return store, remote, car, node
}

func TestControlMemberPreparationAndKeyJournal(t *testing.T) {
	s, remote, car, node := mainControlFixture(t)
	ctx := context.Background()
	require.NoError(t, s.PrepareMember(ctx, car.ID, 2))
	require.NoError(t, s.PrepareMember(ctx, car.ID, 2))
	require.NoError(t, s.PrepareMember(ctx, car.ID, 3))
	require.Len(t, remote.members, 2)
	op := uuid.NewString()
	_, err := s.CreateMemberKey(ctx, 2, car.ID, op, "one")
	require.ErrorIs(t, err, ErrRequired)
	require.Zero(t, remote.calls)
	_, remote.expires = activateMainFixture(t, s.main.db, car.ID)
	remote.lost = true
	_, err = s.CreateMemberKey(ctx, 2, car.ID, op, "one")
	require.ErrorIs(t, err, ErrControlTransport)
	key, err := s.CreateMemberKey(ctx, 2, car.ID, op, "one")
	require.NoError(t, err)
	require.NotEmpty(t, key.Key)
	require.Len(t, remote.keys, 1)
	_, err = s.CreateMemberKey(ctx, 2, car.ID, op, "different")
	require.ErrorIs(t, err, ErrMainConflict)
	_, err = s.CreateMemberKey(ctx, 4, car.ID, uuid.NewString(), "not a member")
	require.ErrorIs(t, err, ErrMainNotFound)
	_, err = s.CreateMemberKey(ctx, 2, uuid.NewString(), op, "one")
	require.ErrorIs(t, err, ErrMainNotFound)
	var encoded string
	require.NoError(t, s.main.db.QueryRow(`SELECT to_jsonb(j)::text FROM carpool_main_key_commands j WHERE actor_id=2 AND operation_id=$1`, op).Scan(&encoded))
	require.NotContains(t, encoded, key.Key)
	require.NotContains(t, encoded, "synthetic-control-key")
	var command struct {
		RemoteKeyID int64  `json:"remote_key_id"`
		Status      string `json:"status"`
		NodeID      string `json:"node_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(encoded), &command))
	require.Equal(t, key.ID, command.RemoteKeyID)
	require.Equal(t, "succeeded", command.Status)
	require.Equal(t, node.ID, command.NodeID)
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.CreateMemberKey(ctx, 2, car.ID, op, "one"); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Len(t, remote.keys, 1)
	_, err = s.main.db.Exec(`UPDATE carpool_memberships SET status='revoked' WHERE car_id=$1 AND user_id=2`, car.ID)
	require.NoError(t, err)
	_, err = s.CreateMemberKey(ctx, 2, car.ID, op, "one")
	require.ErrorIs(t, err, ErrRequired)
}

func TestControlKeyLateResponseCannotLeakAfterRevokeOrRotation(t *testing.T) {
	for _, kind := range []string{"revoke", "rotate", "disable-user"} {
		t.Run(kind, func(t *testing.T) {
			s, remote, car, node := mainControlFixture(t)
			ctx := context.Background()
			require.NoError(t, s.PrepareMember(ctx, car.ID, 2))
			_, remote.expires = activateMainFixture(t, s.main.db, car.ID)
			remote.onCreate = func() {
				switch kind {
				case "revoke":
					_, err := s.main.db.Exec(`UPDATE carpool_memberships SET status='revoked' WHERE car_id=$1 AND user_id=2`, car.ID)
					require.NoError(t, err)
				case "disable-user":
					_, err := s.main.db.Exec(`UPDATE users SET status='disabled' WHERE id=2`)
					require.NoError(t, err)
				case "rotate":
					conn, err := s.ConnectionForCar(ctx, car.ID)
					require.NoError(t, err)
					_, err = s.SaveConnection(ctx, 1, node.ID, ControlConnectionInput{Origin: conn.Origin, InstanceID: conn.InstanceID, ExpectedGeneration: conn.Generation, AdminKey: "replacement-control-key"})
					require.NoError(t, err)
				}
			}
			result, err := s.CreateMemberKey(ctx, 2, car.ID, uuid.NewString(), "late")
			require.Error(t, err)
			require.Empty(t, result.Key)
			require.True(t, errors.Is(err, ErrRequired) || errors.Is(err, ErrMainNotFound) || errors.Is(err, ErrMainConflict) || errors.Is(err, ErrControlTransport), err)
			var successes int
			require.NoError(t, s.main.db.QueryRow(`SELECT count(*) FROM carpool_main_key_commands WHERE status='succeeded'`).Scan(&successes))
			require.Zero(t, successes)
		})
	}
}

func TestControlMemberMutationsAndScopedReads(t *testing.T) {
	s, remote, car, _ := mainControlFixture(t)
	ctx := context.Background()
	for _, user := range []int64{2, 3} {
		require.NoError(t, s.PrepareMember(ctx, car.ID, user))
	}
	_, remote.expires = activateMainFixture(t, s.main.db, car.ID)
	op := uuid.NewString()
	key, err := s.CreateMemberKey(ctx, 2, car.ID, op, "one")
	require.NoError(t, err)
	name := "renamed"
	renamed, err := s.UpdateMemberKey(ctx, 2, car.ID, key.ID, ManagedKeyUpdate{Name: &name})
	require.NoError(t, err)
	require.Equal(t, name, renamed.Name)
	require.Empty(t, renamed.Key)
	_, err = s.UpdateMemberKey(ctx, 3, car.ID, key.ID, ManagedKeyUpdate{Name: &name})
	require.ErrorIs(t, err, ErrMainNotFound)
	require.ErrorIs(t, s.DeleteMemberKey(ctx, 3, car.ID, key.ID), ErrMainNotFound)
	page := ControlPageQuery{Page: 1, PageSize: 20}
	listed, err := s.ListMemberKeys(ctx, 2, car.ID, page)
	require.NoError(t, err)
	require.Len(t, listed.Items, 1)
	require.Empty(t, listed.Items[0].Key)
	other, err := s.ListMemberKeys(ctx, 3, car.ID, page)
	require.NoError(t, err)
	require.Empty(t, other.Items)
	query := ControlUsageQuery{ControlPageQuery: page, StartDate: "2026-09-01", EndDate: "2026-09-19"}
	usage, err := s.MemberUsage(ctx, 2, car.ID, query)
	require.NoError(t, err)
	require.EqualValues(t, 101, usage.Items[0].InputTokens)
	stats, err := s.MemberUsageStats(ctx, 3, car.ID, query)
	require.NoError(t, err)
	require.EqualValues(t, 102, stats.TotalRequests)
	_, err = s.MemberUsage(ctx, 4, car.ID, query)
	require.ErrorIs(t, err, ErrMainNotFound)
	_, err = s.main.db.Exec(`UPDATE carpool_memberships SET status='revoked' WHERE car_id=$1 AND user_id=2`, car.ID)
	require.NoError(t, err)
	disabled, active := "disabled", "active"
	_, err = s.UpdateMemberKey(ctx, 2, car.ID, key.ID, ManagedKeyUpdate{Status: &disabled})
	require.NoError(t, err)
	_, err = s.UpdateMemberKey(ctx, 2, car.ID, key.ID, ManagedKeyUpdate{Status: &active})
	require.ErrorIs(t, err, ErrRequired)
	require.NoError(t, s.DeleteMemberKey(ctx, 2, car.ID, key.ID))
	require.NoError(t, s.DeleteMemberKey(ctx, 2, car.ID, key.ID))
	_, err = s.main.db.Exec(`UPDATE carpool_memberships SET status='active' WHERE car_id=$1 AND user_id=2`, car.ID)
	require.NoError(t, err)
	_, err = s.CreateMemberKey(ctx, 2, car.ID, op, "one")
	require.ErrorIs(t, err, ErrManagedKeyGone)
}

func TestControlDeletionSuppressesInFlightCreationSecret(t *testing.T) {
	s, remote, car, _ := mainControlFixture(t)
	ctx := context.Background()
	require.NoError(t, s.PrepareMember(ctx, car.ID, 2))
	_, remote.expires = activateMainFixture(t, s.main.db, car.ID)
	remote.onCreate = func() { require.NoError(t, s.DeleteMemberKey(ctx, 2, car.ID, 1000)) }
	key, err := s.CreateMemberKey(ctx, 2, car.ID, uuid.NewString(), "racing")
	require.ErrorIs(t, err, ErrManagedKeyGone)
	require.Empty(t, key.Key)
}

func TestControlCreateRejectsExpiredRemoteResult(t *testing.T) {
	s, remote, car, _ := mainControlFixture(t)
	ctx := context.Background()
	require.NoError(t, s.PrepareMember(ctx, car.ID, 2))
	activateMainFixture(t, s.main.db, car.ID)
	remote.expires = time.Now().UTC().Add(-time.Minute)
	key, err := s.CreateMemberKey(ctx, 2, car.ID, uuid.NewString(), "expired response")
	require.ErrorIs(t, err, ErrControlProtocol)
	require.Empty(t, key.Key)
}

func TestControlJournalPersistenceBeforeSendAndCrossCarReplay(t *testing.T) {
	s, remote, car, _ := mainControlFixture(t)
	ctx := context.Background()
	require.NoError(t, s.PrepareMember(ctx, car.ID, 2))
	_, remote.expires = activateMainFixture(t, s.main.db, car.ID)
	_, err := s.main.db.Exec(`CREATE FUNCTION reject_main_key_command() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic-private-command-error'; END $$;
 CREATE TRIGGER reject_main_key_command BEFORE INSERT ON carpool_main_key_commands FOR EACH ROW EXECUTE FUNCTION reject_main_key_command()`)
	require.NoError(t, err)
	_, err = s.CreateMemberKey(ctx, 2, car.ID, uuid.NewString(), "not durable")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "synthetic-private")
	require.Zero(t, remote.calls)
	_, err = s.main.db.Exec(`DROP TRIGGER reject_main_key_command ON carpool_main_key_commands`)
	require.NoError(t, err)
	op := uuid.NewString()
	_, err = s.CreateMemberKey(ctx, 2, car.ID, op, "bound to car one")
	require.NoError(t, err)
	second := publishedCar(t, s.main, 2)
	node, err := s.main.RegisterNode(ctx, 1, NodeInput{CarID: second.ID, Hostname: "node2.example"})
	require.NoError(t, err)
	_, err = s.main.EnrollManual(ctx, 1, second.ID, "car-two", manualInput(2, "car-two"))
	require.NoError(t, err)
	conn, err := s.SaveConnection(ctx, 1, node.ID, ControlConnectionInput{Origin: "https://node2.example", InstanceID: uuid.NewString(), AdminKey: "synthetic-control-key"})
	require.NoError(t, err)
	s.remote = &controlInfoStub{info: ControlInfo{Protocol: 1, MainID: s.MainID(), NodeID: node.ID, CarID: second.ID, InstanceID: conn.InstanceID, Capabilities: []string{"connection-check"}}}
	_, err = s.CheckConnection(ctx, 1, node.ID)
	require.NoError(t, err)
	require.NoError(t, s.PrepareMember(ctx, second.ID, 2))
	activateMainFixture(t, s.main.db, second.ID)
	before := remote.calls
	_, err = s.CreateMemberKey(ctx, 2, second.ID, op, "bound to car one")
	require.ErrorIs(t, err, ErrMainConflict)
	require.Equal(t, before, remote.calls, "cross-car replay must be rejected before touching either node")
}
