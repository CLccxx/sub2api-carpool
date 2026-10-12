package carpool

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
)

const (
	ControlHeaderMain     = "X-Carpool-Main-ID"
	ControlHeaderNode     = "X-Carpool-Node-ID"
	ControlHeaderCar      = "X-Carpool-Car-ID"
	ControlHeaderInstance = "X-Carpool-Instance-ID"
)

func validManagedUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func (t ManagedTarget) Validate() error {
	for _, value := range []string{t.MainID, t.NodeID, t.CarID, t.InstanceID} {
		if !validManagedUUID(value) {
			return ErrControlTarget
		}
	}
	return nil
}

func (c *ControlClient) Provision(ctx context.Context, origin, credential string, target ManagedTarget, subject string) (ManagedMember, error) {
	if !validManagedUUID(subject) {
		return ManagedMember{}, ErrMainInvalid
	}
	if err := target.Validate(); err != nil {
		return ManagedMember{}, err
	}
	data, err := c.requestBound(ctx, origin, credential, http.MethodPut, controlPrefix+"/members/"+subject, bytes.NewBufferString("{}"), &target)
	if err != nil {
		return ManagedMember{}, err
	}
	var member ManagedMember
	if json.Unmarshal(data, &member) != nil || member.Subject != subject || member.UserID <= 0 || member.GroupID <= 0 || member.Version < 0 {
		return ManagedMember{}, ErrControlProtocol
	}
	return member, nil
}

func (c *ControlClient) ApplyGrant(ctx context.Context, origin, credential string, target ManagedTarget, subject string, grant ManagedGrant) error {
	if !validManagedUUID(subject) {
		return ErrMainInvalid
	}
	if err := target.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(grant.canonical())
	if err != nil {
		return ErrMainInvalid
	}
	data, err := c.requestBound(ctx, origin, credential, http.MethodPut, controlPrefix+"/members/"+subject+"/grant", bytes.NewReader(body), &target)
	if err != nil {
		return err
	}
	var ack struct {
		Version int64  `json:"version"`
		Status  string `json:"status"`
	}
	if json.Unmarshal(data, &ack) != nil || ack.Version != grant.Version || ack.Status != grant.Status {
		return ErrControlProtocol
	}
	return nil
}
