package carpool

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
)

func (c *ControlClient) CreateKey(ctx context.Context, origin, credential string, target ManagedTarget, subject, operation, name string) (ManagedKey, error) {
	if !validManagedUUID(subject) || !validManagedUUID(operation) || !ValidManagedKeyName(name) {
		return ManagedKey{}, ErrMainInvalid
	}
	if err := target.Validate(); err != nil {
		return ManagedKey{}, err
	}
	body, err := json.Marshal(struct {
		Name string `json:"name"`
	}{Name: name})
	if err != nil {
		return ManagedKey{}, ErrMainInvalid
	}
	data, err := c.requestCommand(ctx, origin, credential, http.MethodPost, controlPrefix+"/members/"+subject+"/keys", bytes.NewReader(body), &target, operation)
	if err != nil {
		return ManagedKey{}, err
	}
	var key ManagedKey
	if json.Unmarshal(data, &key) != nil || key.ID <= 0 || key.Status != "active" || key.ExpiresAt == nil || key.Key == "" || len(key.Key) > 512 {
		return ManagedKey{}, ErrControlProtocol
	}
	return key, nil
}

func (c *ControlClient) UpdateKey(ctx context.Context, origin, credential string, target ManagedTarget, subject string, keyID int64, input ManagedKeyUpdate) (ManagedKey, error) {
	if !validManagedUUID(subject) || keyID <= 0 || input.Validate() != nil {
		return ManagedKey{}, ErrMainInvalid
	}
	if err := target.Validate(); err != nil {
		return ManagedKey{}, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return ManagedKey{}, ErrMainInvalid
	}
	data, err := c.requestBound(ctx, origin, credential, http.MethodPatch, controlPrefix+"/members/"+subject+"/keys/"+strconv.FormatInt(keyID, 10), bytes.NewReader(body), &target)
	if err != nil {
		return ManagedKey{}, err
	}
	var key ManagedKey
	if json.Unmarshal(data, &key) != nil || key.ID != keyID {
		return ManagedKey{}, ErrControlProtocol
	}
	key.Key = ""
	return key, nil
}

func (c *ControlClient) DeleteKey(ctx context.Context, origin, credential string, target ManagedTarget, subject string, keyID int64) error {
	if !validManagedUUID(subject) || keyID <= 0 {
		return ErrMainInvalid
	}
	if err := target.Validate(); err != nil {
		return err
	}
	data, err := c.requestBound(ctx, origin, credential, http.MethodDelete, controlPrefix+"/members/"+subject+"/keys/"+strconv.FormatInt(keyID, 10), nil, &target)
	if err != nil {
		return err
	}
	var ack struct {
		ID      int64 `json:"id"`
		Deleted bool  `json:"deleted"`
	}
	if json.Unmarshal(data, &ack) != nil || ack.ID != keyID || !ack.Deleted {
		return ErrControlProtocol
	}
	return nil
}
