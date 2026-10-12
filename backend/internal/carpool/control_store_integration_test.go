//go:build integration

package carpool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type controlInfoStub struct {
	calls            atomic.Int64
	info             ControlInfo
	err              error
	started, release chan struct{}
}

func (s *controlInfoStub) Info(ctx context.Context, _, key string) (ControlInfo, error) {
	s.calls.Add(1)
	if s.started != nil {
		close(s.started)
		select {
		case <-s.release:
		case <-ctx.Done():
			return ControlInfo{}, ErrControlTransport
		}
	}
	if key != "synthetic-control-key" && key != "replacement-control-key" {
		return ControlInfo{}, ErrControlAuth
	}
	return s.info, s.err
}

func TestControlStoreIdentityConnectionsAndRaces(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	main := NewMainService(db, MainConfig{Enabled: true, TestOrdersEnabled: true})
	car := publishedCar(t, main, 2)
	node, err := main.RegisterNode(ctx, 1, NodeInput{CarID: car.ID, Hostname: "node.example"})
	require.NoError(t, err)
	root := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	store, err := NewControlStore(ctx, main, root, []string{"https://node.example", "https://node.example:8443", "https://different.example"})
	require.NoError(t, err)
	again, err := NewControlStore(ctx, main, root, []string{"https://node.example"})
	require.NoError(t, err)
	require.Equal(t, store.MainID(), again.MainID())
	require.NotEmpty(t, store.MainID())
	_, err = EnsureControlIdentity(ctx, db, "node")
	require.ErrorIs(t, err, ErrMainConflict, "an established database must not silently change role")
	input := ControlConnectionInput{Origin: "https://node.example", InstanceID: uuid.NewString(), AdminKey: "synthetic-control-key"}
	saved, err := store.SaveConnection(ctx, 1, node.ID, input)
	require.NoError(t, err)
	require.EqualValues(t, 1, saved.Generation)
	require.Equal(t, "unverified", saved.Status)
	require.True(t, saved.CredentialConfigured)
	assertSafe := func(got ControlConnection) {
		data, err := json.Marshal(got)
		require.NoError(t, err)
		require.NotContains(t, string(data), "synthetic-control-key")
		require.NotContains(t, string(data), "replacement-control-key")
		require.NotContains(t, string(data), "credential_ciphertext")
		require.NotContains(t, string(data), "v1.")
	}
	assertSafe(saved)
	read, err := store.ConnectionForCar(ctx, car.ID)
	require.NoError(t, err)
	require.Equal(t, saved.Generation, read.Generation)
	assertSafe(read)
	var sealed string
	require.NoError(t, db.QueryRow(`SELECT credential_ciphertext FROM carpool_node_connections WHERE node_id=$1`, node.ID).Scan(&sealed))
	require.NotContains(t, sealed, "synthetic-control-key")
	_, err = store.SaveConnection(ctx, 1, node.ID, input)
	require.ErrorIs(t, err, ErrMainConflict)
	input.ExpectedGeneration = 1
	input.AdminKey = ""
	wrongHost := input
	wrongHost.Origin = "https://different.example"
	_, err = store.SaveConnection(ctx, 1, node.ID, wrongHost)
	require.ErrorIs(t, err, ErrControlTarget)
	store.remote = &controlInfoStub{info: ControlInfo{Protocol: 1, MainID: store.MainID(), NodeID: node.ID, CarID: car.ID, InstanceID: input.InstanceID, Capabilities: []string{"connection-check"}}}
	checked, err := store.CheckConnection(ctx, 1, node.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", checked.Status)
	require.NotNil(t, checked.VerifiedAt)
	changedTarget := input
	changedTarget.InstanceID = uuid.NewString()
	_, err = store.SaveConnection(ctx, 1, node.ID, changedTarget)
	require.ErrorIs(t, err, ErrMainConflict)
	changedTarget = input
	changedTarget.Origin = "https://node.example:8443"
	_, err = store.SaveConnection(ctx, 1, node.ID, changedTarget)
	require.ErrorIs(t, err, ErrMainConflict)
	remote, ok := store.remote.(*controlInfoStub)
	require.True(t, ok)
	beforeDiscovery := remote.calls.Load()
	changedTarget.InstanceID = ""
	changedTarget.AdminKey = "synthetic-control-key"
	_, err = store.SaveConnection(ctx, 1, node.ID, changedTarget)
	require.ErrorIs(t, err, ErrMainConflict)
	require.Equal(t, beforeDiscovery, remote.calls.Load(), "do not send credentials to a replacement origin during discovery")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := store.SaveConnection(ctx, 1, node.ID, input); results <- err }()
	}
	wg.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrMainConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	read, err = store.ConnectionForCar(ctx, car.ID)
	require.NoError(t, err)
	require.EqualValues(t, 2, read.Generation)
	require.Equal(t, "unverified", read.Status)
	baseInfo := ControlInfo{Protocol: 1, MainID: store.MainID(), NodeID: node.ID, CarID: car.ID, InstanceID: input.InstanceID, Capabilities: []string{"connection-check"}}
	blocked := &controlInfoStub{info: baseInfo, started: make(chan struct{}), release: make(chan struct{})}
	store.remote = blocked
	checkResult := make(chan error, 1)
	go func() { _, err := store.CheckConnection(ctx, 1, node.ID); checkResult <- err }()
	<-blocked.started
	input.ExpectedGeneration = 2
	input.AdminKey = "replacement-control-key"
	saved, err = store.SaveConnection(ctx, 1, node.ID, input)
	require.NoError(t, err)
	close(blocked.release)
	require.ErrorIs(t, <-checkResult, ErrMainConflict, "late response must not update replaced credentials")
	read, err = store.ConnectionForCar(ctx, car.ID)
	require.NoError(t, err)
	require.EqualValues(t, 3, read.Generation)
	require.Equal(t, "unverified", read.Status)
	wrongInfo := baseInfo
	wrongInfo.InstanceID = uuid.NewString()
	store.remote = &controlInfoStub{info: wrongInfo}
	_, err = store.CheckConnection(ctx, 1, node.ID)
	require.ErrorIs(t, err, ErrControlTarget)
	read, err = store.ConnectionForCar(ctx, car.ID)
	require.NoError(t, err)
	require.Equal(t, "error", read.Status)
	require.Equal(t, "CARPOOL_CONTROL_TARGET", read.LastErrorCode)
	store.remote = &controlInfoStub{info: baseInfo, err: ErrControlAuth}
	_, err = store.CheckConnection(ctx, 1, node.ID)
	require.ErrorIs(t, err, ErrControlAuth)
	input.ExpectedGeneration = 3
	input.AdminKey = ""
	input.Disabled = true
	saved, err = store.SaveConnection(ctx, 1, node.ID, input)
	require.NoError(t, err)
	require.Equal(t, "disabled", saved.Status)
	_, err = store.CheckConnection(ctx, 1, node.ID)
	require.ErrorIs(t, err, ErrMainConflict)
	assertSafe(saved)
	var grants, started int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_node_memberships`).Scan(&grants))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_memberships WHERE service_start IS NOT NULL`).Scan(&started))
	require.Zero(t, grants)
	require.Zero(t, started)
	_, err = store.SaveConnection(ctx, 101, node.ID, input)
	require.ErrorIs(t, err, ErrMainNotFound)
	_, err = store.ConnectionForCar(ctx, uuid.NewString())
	require.ErrorIs(t, err, ErrMainNotFound)
	input.ExpectedGeneration = 4
	input.Disabled = false
	_, err = store.SaveConnection(ctx, 1, node.ID, input)
	require.NoError(t, err)
	wrongRoot, err := NewControlStore(ctx, main, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{10}, 32)), []string{"https://node.example"})
	require.NoError(t, err)
	_, err = wrongRoot.CheckConnection(ctx, 1, node.ID)
	require.ErrorIs(t, err, ErrControlCredential)
	read, err = store.ConnectionForCar(ctx, car.ID)
	require.NoError(t, err)
	require.Equal(t, "error", read.Status)
	require.Equal(t, "CARPOOL_CONTROL_CREDENTIAL", read.LastErrorCode)
	input.ExpectedGeneration = 5
	input.AdminKey = "replacement-control-key"
	_, err = store.SaveConnection(ctx, 1, node.ID, input)
	require.NoError(t, err)
	oldCheck := &controlInfoStub{info: baseInfo, err: ErrControlAuth, started: make(chan struct{}), release: make(chan struct{})}
	store.remote = oldCheck
	go func() { _, err := store.CheckConnection(ctx, 1, node.ID); checkResult <- err }()
	<-oldCheck.started
	store.remote = &controlInfoStub{info: baseInfo}
	checked, err = store.CheckConnection(ctx, 1, node.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", checked.Status)
	close(oldCheck.release)
	require.ErrorIs(t, <-checkResult, ErrMainConflict, "older check at the same generation must not replace a newer check")
	read, err = store.ConnectionForCar(ctx, car.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", read.Status)
	oldCheck = &controlInfoStub{info: baseInfo, started: make(chan struct{}), release: make(chan struct{})}
	store.remote = oldCheck
	go func() { _, err := store.CheckConnection(ctx, 1, node.ID); checkResult <- err }()
	<-oldCheck.started
	input.ExpectedGeneration = 6
	input.Disabled = true
	_, err = store.SaveConnection(ctx, 1, node.ID, input)
	require.NoError(t, err)
	close(oldCheck.release)
	require.ErrorIs(t, <-checkResult, ErrMainConflict)
	read, err = store.ConnectionForCar(ctx, car.ID)
	require.NoError(t, err)
	require.Equal(t, "disabled", read.Status)
}
