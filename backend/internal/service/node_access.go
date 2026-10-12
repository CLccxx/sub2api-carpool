package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
)

type NodeMembershipGate interface {
	CheckUser(context.Context, int64) error
	CheckIdentity(context.Context, string, string) error
}

func (s *AuthService) NodeMode() bool { return s != nil && s.cfg != nil && s.cfg.CarpoolNode.Enabled }

func (s *AuthService) CheckNodeUser(ctx context.Context, user *User) error {
	if !s.NodeMode() {
		return nil
	}
	if user == nil {
		return carpool.ErrRequired
	}
	// Recovery/admin management is independent of model access. API-key auth has no bypass.
	if user.Role == RoleAdmin {
		return nil
	}
	if s.cfg.CarpoolNode.Managed() {
		return carpool.ErrRequired
	}
	if s.nodeAccess == nil {
		return carpool.ErrUnavailable
	}
	return s.nodeAccess.CheckUser(ctx, user.ID)
}

func (s *AuthService) CheckNodeIdentity(ctx context.Context, provider, issuer, subject string) error {
	if !s.NodeMode() {
		return nil
	}
	if s.cfg.CarpoolNode.Managed() || provider != "oidc" {
		return carpool.ErrRequired
	}
	if s.nodeAccess == nil {
		return carpool.ErrUnavailable
	}
	return s.nodeAccess.CheckIdentity(ctx, issuer, subject)
}

func (s *APIKeyService) CheckNodeUser(ctx context.Context, userID int64) error {
	if s == nil || s.cfg == nil || !s.cfg.CarpoolNode.Enabled {
		return nil
	}
	if s.nodeAccess == nil {
		return carpool.ErrUnavailable
	}
	return s.nodeAccess.CheckUser(ctx, userID)
}
