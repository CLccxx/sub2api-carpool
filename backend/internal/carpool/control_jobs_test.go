package carpool

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type provisionOnlyRemote struct{ controlNodeClient }

func (provisionOnlyRemote) Provision(_ context.Context, _, _ string, _ ManagedTarget, subject string) (ManagedMember, error) {
	return ManagedMember{Subject: subject, UserID: 101, GroupID: 7}, nil
}
func (provisionOnlyRemote) ApplyGrant(context.Context, string, string, ManagedTarget, string, ManagedGrant) error {
	panic("grant must not be sent after preflight becomes unavailable")
}

func TestControlSyncDoesNotAcknowledgeUnsentGrant(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	cipher, err := NewCredentialCipher(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	require.NoError(t, err)
	mainID, instance := uuid.NewString(), uuid.NewString()
	c := controlSyncClaim{car: uuid.NewString(), node: uuid.NewString(), subject: uuid.NewString(), operation: uuid.NewString(), token: uuid.NewString(), generation: 1, version: 1}
	sealed, err := cipher.Seal(CredentialBinding{MainID: mainID, NodeID: c.node, Generation: 1}, "synthetic-key")
	require.NoError(t, err)
	s := &ControlStore{main: NewMainService(db, MainConfig{Enabled: true}), mainID: mainID, cipher: cipher, nodeClient: provisionOnlyRemote{}}
	for i, status := range []string{"ready", "unverified"} {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM carpool_cars").WithArgs(c.car).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(c.car))
		mock.ExpectQuery("SELECT id FROM carpool_nodes").WithArgs(c.car).WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery("SELECT origin,expected_instance_id").WithArgs(c.node).WillReturnRows(sqlmock.NewRows([]string{"origin", "instance", "sealed", "generation", "status", "pinned", "operation"}).AddRow("https://node.example", instance, sealed, 1, status, true, ""))
		if i == 0 {
			g := ManagedGrant{Version: 1, Status: "prepared"}
			mock.ExpectQuery("SELECT version,status,valid_from").WithArgs(c.node, c.subject, c.operation, c.token, c.generation).WillReturnRows(sqlmock.NewRows([]string{"version", "status", "start", "end", "lease", "digest", "native_subscription"}).AddRow(g.Version, g.Status, nil, nil, nil, controlGrantHash(g), false))
			mock.ExpectQuery("SELECT COALESCE\\(remote_user_id").WithArgs(c.node, c.subject).WillReturnRows(sqlmock.NewRows([]string{"user", "group", "instance"}).AddRow(0, 0, ""))
		}
		mock.ExpectCommit()
	}
	// No completion transaction is legitimate. A re-check may turn ready immediately
	// afterward; acknowledging now would report a grant that was never sent.
	require.NoError(t, s.deliverControlSync(context.Background(), c))
	require.NoError(t, mock.ExpectationsWereMet())
}
