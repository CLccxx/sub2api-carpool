package carpool

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
)

type NativeAccount struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

func (n *NodeControl) SubscriptionAccounts(ctx context.Context, target ManagedTarget) ([]NativeAccount, error) {
	if target != n.Target() {
		return nil, ErrControlTarget
	}
	rows, err := n.db.QueryContext(ctx, `SELECT id,name,platform FROM accounts WHERE deleted_at IS NULL AND status='active' AND type='oauth' AND schedulable AND platform IN ('openai','anthropic') ORDER BY id LIMIT 100`)
	if err != nil {
		return nil, controlStoreError(err)
	}
	defer func() { _ = rows.Close() }()
	result := []NativeAccount{}
	for rows.Next() {
		var a NativeAccount
		if err = rows.Scan(&a.ID, &a.Name, &a.Platform); err != nil {
			return nil, controlStoreError(err)
		}
		result = append(result, a)
	}
	return result, controlStoreError(rows.Err())
}

func (c *ControlClient) InitializeSubscription(ctx context.Context, origin, credential string, target ManagedTarget, input NativeSetupInput) (NativeSetup, error) {
	if err := target.Validate(); err != nil {
		return NativeSetup{}, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return NativeSetup{}, ErrMainInvalid
	}
	data, err := c.requestBound(ctx, origin, credential, http.MethodPost, controlPrefix+"/subscription/initialize", bytes.NewReader(body), &target)
	if err != nil {
		return NativeSetup{}, err
	}
	var result NativeSetup
	if json.Unmarshal(data, &result) != nil || result.NativeSetupInput != input || result.GroupID <= 0 || result.WeeklyLimitUSD <= 0 {
		return NativeSetup{}, ErrControlProtocol
	}
	return result, nil
}

func (c *ControlClient) SubscriptionAccounts(ctx context.Context, origin, credential string, target ManagedTarget) ([]NativeAccount, error) {
	if err := target.Validate(); err != nil {
		return nil, err
	}
	data, err := c.requestBound(ctx, origin, credential, http.MethodGet, controlPrefix+"/accounts", nil, &target)
	if err != nil {
		return nil, err
	}
	var result []NativeAccount
	if json.Unmarshal(data, &result) != nil || result == nil || len(result) > 100 {
		return nil, ErrControlProtocol
	}
	for _, a := range result {
		if a.ID <= 0 || (a.Platform != "openai" && a.Platform != "anthropic") {
			return nil, ErrControlProtocol
		}
	}
	return result, nil
}
