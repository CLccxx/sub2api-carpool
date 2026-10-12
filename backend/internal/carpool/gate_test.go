package carpool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestMembershipAllows(t *testing.T) {
	now := time.Now().UTC()
	cfg := Config{Enabled: true, NodeID: "node-b", CarID: "car-1", Issuer: "http://id.localhost/realm"}
	valid := Grant{NodeID: cfg.NodeID, CarID: cfg.CarID, Issuer: cfg.Issuer, Subject: "alice", Version: 1, Status: "active", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour), LeaseUntil: now.Add(time.Minute)}
	for _, tc := range []struct {
		name    string
		change  func(*Grant)
		allowed bool
	}{
		{"active", func(g *Grant) {}, true},
		{"wrong node", func(g *Grant) { g.NodeID = "node-c" }, false},
		{"wrong car", func(g *Grant) { g.CarID = "car-2" }, false},
		{"wrong issuer", func(g *Grant) { g.Issuer = "http://other" }, false},
		{"missing subject", func(g *Grant) { g.Subject = "" }, false},
		{"revoked", func(g *Grant) { g.Status = "revoked" }, false},
		{"paused", func(g *Grant) { g.Status = "paused" }, false},
		{"unknown status", func(g *Grant) { g.Status = "unknown" }, false},
		{"future", func(g *Grant) { g.ValidFrom = now.Add(time.Second) }, false},
		{"expired", func(g *Grant) { g.ValidUntil = now }, false},
		{"lease expired", func(g *Grant) { g.LeaseUntil = now }, false},
		{"missing version", func(g *Grant) { g.Version = 0 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) { g := valid; tc.change(&g); require.Equal(t, tc.allowed, g.Allows(cfg, now)) })
	}
}

func TestGateReadsEveryRequestAndFailsClosed(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	gate := NewGate(db, Config{Enabled: true, NodeID: "b", CarID: "1", Issuer: "issuer"})
	mock.ExpectQuery("SELECT EXISTS").WithArgs("b", "1", "issuer", int64(7)).WillReturnRows(sqlmock.NewRows([]string{"allowed"}).AddRow(true))
	require.NoError(t, gate.CheckUser(context.Background(), 7))
	mock.ExpectQuery("SELECT EXISTS").WithArgs("b", "1", "issuer", int64(7)).WillReturnRows(sqlmock.NewRows([]string{"allowed"}).AddRow(false))
	require.ErrorIs(t, gate.CheckUser(context.Background(), 7), ErrRequired)
	mock.ExpectQuery("SELECT EXISTS").WillReturnError(errors.New("database unavailable"))
	require.ErrorIs(t, gate.CheckUser(context.Background(), 7), ErrUnavailable)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGrantRejectsStaleVersionAndWrongTarget(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	gate := NewGate(db, Config{Enabled: true, NodeID: "b", CarID: "1", Issuer: "issuer"})
	now := time.Now().UTC()
	g := Grant{NodeID: "b", CarID: "1", Issuer: "issuer", Subject: "alice", Version: 2, Status: "revoked", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour), LeaseUntil: now}
	mock.ExpectExec("INSERT INTO carpool_node_memberships").WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, gate.Apply(context.Background(), g), ErrVersionConflict)
	g.NodeID = "c"
	require.Error(t, gate.Apply(context.Background(), g))
	g.NodeID = "b"
	g.Status = "active"
	g.LeaseUntil = now.Add(time.Hour)
	require.Error(t, gate.Apply(context.Background(), g))
	require.NoError(t, mock.ExpectationsWereMet())
}
