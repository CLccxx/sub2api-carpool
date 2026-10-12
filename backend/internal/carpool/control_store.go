package carpool

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/url"
	"time"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

type ControlConnectionInput struct {
	Origin             string `json:"origin"`
	InstanceID         string `json:"node_instance_id"`
	AdminKey           string `json:"admin_key"`
	ExpectedGeneration int64  `json:"expected_generation"`
	Disabled           bool   `json:"disabled"`
}

type ControlConnection struct {
	MainID               string     `json:"main_instance_id"`
	NodeID               string     `json:"node_id"`
	CarID                string     `json:"car_id"`
	Hostname             string     `json:"hostname"`
	Origin               string     `json:"origin"`
	InstanceID           string     `json:"node_instance_id"`
	Generation           int64      `json:"generation"`
	Status               string     `json:"status"`
	CredentialConfigured bool       `json:"credential_configured"`
	CredentialMask       string     `json:"credential_mask"`
	LastErrorCode        string     `json:"last_error_code"`
	CheckOperation       string     `json:"check_operation"`
	VerifiedAt           *time.Time `json:"verified_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type controlInfoReader interface {
	Info(context.Context, string, string) (ControlInfo, error)
}

type ControlStore struct {
	main       *MainService
	mainID     string
	cipher     *CredentialCipher
	client     *ControlClient
	remote     controlInfoReader
	nodeClient controlNodeClient
}

func EnsureControlIdentity(ctx context.Context, db *sql.DB, kind string) (string, error) {
	if db == nil || (kind != "main" && kind != "node") {
		return "", ErrMainInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var id, storedKind string
	err := db.QueryRowContext(ctx, `INSERT INTO carpool_control_instance(singleton,kind,instance_id)
 VALUES(TRUE,$1,$2) ON CONFLICT(singleton) DO UPDATE SET singleton=TRUE
 RETURNING kind,instance_id::text`, kind, uuid.NewString()).Scan(&storedKind, &id)
	if err != nil {
		return "", ErrMainUnavailable
	}
	if storedKind != kind {
		return "", ErrMainConflict
	}
	return id, nil
}

func NewControlStore(ctx context.Context, main *MainService, root string, origins []string) (*ControlStore, error) {
	if main == nil || !main.Enabled() || main.db == nil {
		return nil, ErrMainUnavailable
	}
	cipher, err := NewCredentialCipher(root)
	if err != nil {
		return nil, err
	}
	client, err := NewControlClient(origins)
	if err != nil {
		return nil, err
	}
	id, err := EnsureControlIdentity(ctx, main.db, "main")
	if err != nil {
		return nil, err
	}
	store := &ControlStore{main: main, mainID: id, cipher: cipher, client: client, remote: client, nodeClient: client}
	main.control.Store(store)
	return store, nil
}

func (s *ControlStore) MainID() string { return s.mainID }

func (s *ControlStore) ConnectionForCar(ctx context.Context, carID string) (ControlConnection, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var c ControlConnection
	err := s.main.db.QueryRowContext(ctx, `SELECT n.id,n.car_id,n.hostname,COALESCE(c.origin,''),
 COALESCE(c.expected_instance_id::text,''),COALESCE(c.generation,0),COALESCE(c.status,'unverified'),
 COALESCE(c.last_error_code,''),COALESCE(c.check_operation,''),c.verified_at,COALESCE(c.updated_at,n.created_at)
 FROM carpool_nodes n LEFT JOIN carpool_node_connections c ON c.node_id=n.id WHERE n.car_id=$1`, carID).Scan(
		&c.NodeID, &c.CarID, &c.Hostname, &c.Origin, &c.InstanceID, &c.Generation, &c.Status, &c.LastErrorCode, &c.CheckOperation, &c.VerifiedAt, &c.UpdatedAt)
	if err != nil {
		return c, controlStoreError(err)
	}
	c.MainID = s.mainID
	c.CredentialConfigured = c.Generation > 0
	if c.CredentialConfigured {
		c.CredentialMask = "********"
	}
	return c, nil
}

func (s *ControlStore) nodeCar(ctx context.Context, nodeID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var carID string
	err := s.main.db.QueryRowContext(ctx, `SELECT car_id FROM carpool_nodes WHERE id=$1`, nodeID).Scan(&carID)
	return carID, controlStoreError(err)
}

type controlStoredConnection struct {
	origin, instance, sealed, status, operation string
	generation                                  int64
	pinned                                      bool
}

func readControlConnection(ctx context.Context, tx *sql.Tx, nodeID string) (controlStoredConnection, error) {
	var c controlStoredConnection
	err := tx.QueryRowContext(ctx, `SELECT origin,expected_instance_id::text,credential_ciphertext,generation,status,identity_verified,check_operation
 FROM carpool_node_connections WHERE node_id=$1 FOR UPDATE`, nodeID).Scan(&c.origin, &c.instance, &c.sealed, &c.generation, &c.status, &c.pinned, &c.operation)
	return c, err
}

func controlStoreError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{ErrMainInvalid, ErrMainNotFound, ErrMainConflict, ErrControlCredential, ErrControlTarget, ErrControlAuth, ErrControlCompliance, ErrControlProtocol, ErrControlTransport, ErrRequired, ErrUnavailable, ErrVersionConflict, ErrManagedKeyGone, ErrControlPending, ErrControlGrantWindow} {
		if errors.Is(err, known) {
			return known
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrMainNotFound
	}
	// Database errors can include failing row contents; never retain credential causes.
	return ErrMainUnavailable
}

func (s *ControlStore) SaveConnection(ctx context.Context, actor int64, nodeID string, input ControlConnectionInput) (ControlConnection, error) {
	origin, err := s.client.origin(input.Origin)
	if err != nil {
		return ControlConnection{}, err
	}
	if input.InstanceID == "" {
		carID, lookupErr := s.nodeCar(ctx, nodeID)
		if lookupErr != nil {
			return ControlConnection{}, lookupErr
		}
		var hostname string
		if err = s.main.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, car Car) error {
			if e := activeMainUser(ctx, tx, actor); e != nil {
				return e
			}
			if !input.Disabled && (car.Status == "cancelled" || car.Status == "closed_unfilled") {
				return ErrMainConflict
			}
			if e := tx.QueryRowContext(ctx, `SELECT hostname FROM carpool_nodes WHERE id=$1`, nodeID).Scan(&hostname); e != nil {
				return e
			}
			u, _ := url.Parse(origin)
			if u.Hostname() != hostname {
				return ErrControlTarget
			}
			stored, e := readControlConnection(ctx, tx, nodeID)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if stored.generation != input.ExpectedGeneration || (stored.pinned && stored.origin != origin) {
				return ErrMainConflict
			}
			if stored.pinned {
				input.InstanceID = stored.instance
			}
			return nil
		}); err != nil {
			return ControlConnection{}, controlStoreError(err)
		}
		if input.InstanceID == "" {
			if input.AdminKey == "" {
				return ControlConnection{}, ErrControlCredential
			}
			info, lookupErr := s.remote.Info(ctx, origin, input.AdminKey)
			if lookupErr != nil {
				return ControlConnection{}, lookupErr
			}
			if info.Protocol != 1 || info.MainID != s.mainID || info.NodeID != nodeID || info.CarID != carID {
				return ControlConnection{}, ErrControlTarget
			}
			input.InstanceID = info.InstanceID
		}
	}
	instance, err := uuid.Parse(input.InstanceID)
	if err != nil || instance == uuid.Nil || input.ExpectedGeneration < 0 || input.ExpectedGeneration == math.MaxInt64 {
		return ControlConnection{}, ErrMainInvalid
	}
	carID, err := s.nodeCar(ctx, nodeID)
	if err != nil {
		return ControlConnection{}, err
	}
	err = s.main.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		if err := activeMainUser(ctx, tx, actor); err != nil {
			return err
		}
		if !input.Disabled && (car.Status == "cancelled" || car.Status == "closed_unfilled") {
			return ErrMainConflict
		}
		var hostname string
		if err := tx.QueryRowContext(ctx, `SELECT hostname FROM carpool_nodes WHERE id=$1 AND car_id=$2 FOR UPDATE`, nodeID, carID).Scan(&hostname); err != nil {
			return err
		}
		u, _ := url.Parse(origin)
		if u.Hostname() != hostname {
			return ErrControlTarget
		}
		stored, err := readControlConnection(ctx, tx, nodeID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if stored.generation != input.ExpectedGeneration || (stored.pinned && (stored.origin != origin || stored.instance != instance.String())) {
			return ErrMainConflict
		}
		key := input.AdminKey
		if key == "" && stored.generation > 0 {
			key, err = s.cipher.Open(CredentialBinding{MainID: s.mainID, NodeID: nodeID, Generation: stored.generation}, stored.sealed)
			if err != nil {
				return err
			}
		}
		sealed, err := s.cipher.Seal(CredentialBinding{MainID: s.mainID, NodeID: nodeID, Generation: stored.generation + 1}, key)
		if err != nil {
			return err
		}
		status := "unverified"
		if input.Disabled {
			status = "disabled"
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO carpool_node_connections(node_id,origin,expected_instance_id,credential_ciphertext,generation,status)
 VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(node_id) DO UPDATE SET origin=EXCLUDED.origin,
 expected_instance_id=EXCLUDED.expected_instance_id,credential_ciphertext=EXCLUDED.credential_ciphertext,
 generation=EXCLUDED.generation,status=EXCLUDED.status,check_operation='',last_error_code='',verified_at=NULL,updated_at=clock_timestamp()`,
			nodeID, origin, instance.String(), sealed, stored.generation+1, status)
		if err != nil {
			return err
		}
		return appendMainEvent(ctx, tx, carID, "node_connection_saved", actor, "")
	})
	if err != nil {
		return ControlConnection{}, controlStoreError(err)
	}
	return s.ConnectionForCar(ctx, carID)
}

func (s *ControlStore) CheckConnection(ctx context.Context, actor int64, nodeID string) (ControlConnection, error) {
	carID, err := s.nodeCar(ctx, nodeID)
	if err != nil {
		return ControlConnection{}, err
	}
	operation := uuid.NewString()
	var snapshot controlStoredConnection
	err = s.main.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, _ Car) error {
		if err := activeMainUser(ctx, tx, actor); err != nil {
			return err
		}
		var err error
		snapshot, err = readControlConnection(ctx, tx, nodeID)
		if err != nil {
			return err
		}
		if snapshot.status == "disabled" {
			return ErrMainConflict
		}
		_, err = tx.ExecContext(ctx, `UPDATE carpool_node_connections SET check_operation=$2,status='unverified',verified_at=NULL,last_error_code='',updated_at=clock_timestamp() WHERE node_id=$1`, nodeID, operation)
		return err
	})
	if err != nil {
		return ControlConnection{}, controlStoreError(err)
	}
	// No transaction remains open while communicating with the node.
	key, checkErr := s.cipher.Open(CredentialBinding{MainID: s.mainID, NodeID: nodeID, Generation: snapshot.generation}, snapshot.sealed)
	if checkErr == nil {
		var info ControlInfo
		info, checkErr = s.remote.Info(ctx, snapshot.origin, key)
		if checkErr == nil && (info.MainID != s.mainID || info.NodeID != nodeID || info.CarID != carID || info.InstanceID != snapshot.instance) {
			checkErr = ErrControlTarget
		}
		if checkErr == nil && info.Protocol != 1 {
			checkErr = ErrControlProtocol
		}
	}
	checkErr = controlStoreError(checkErr)
	err = s.main.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, _ Car) error {
		current, err := readControlConnection(ctx, tx, nodeID)
		if err != nil {
			return err
		}
		if current.generation != snapshot.generation || current.operation != operation || current.status == "disabled" {
			return ErrMainConflict
		}
		if checkErr == nil {
			_, err = tx.ExecContext(ctx, `UPDATE carpool_node_connections SET status='ready',identity_verified=TRUE,verified_at=clock_timestamp(),updated_at=clock_timestamp(),last_error_code='' WHERE node_id=$1`, nodeID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE carpool_node_connections SET status='error',verified_at=NULL,last_error_code=$2,updated_at=clock_timestamp() WHERE node_id=$1`, nodeID, apperrors.Reason(checkErr))
		}
		if err != nil {
			return err
		}
		return appendMainEvent(ctx, tx, carID, "node_connection_checked", actor, "")
	})
	if err != nil {
		return ControlConnection{}, controlStoreError(err)
	}
	result, err := s.ConnectionForCar(ctx, carID)
	if err != nil {
		return result, err
	}
	return result, checkErr
}
