package carpool

import (
	"strings"
	"time"
	"unicode/utf8"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrManagedKeyGone = apperrors.Conflict("CARPOOL_KEY_GONE", "managed key is no longer available")

type ManagedKey struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at"`
	Key       string     `json:"key,omitempty"`
}

type ManagedKeyUpdate struct {
	Name   *string `json:"name,omitempty"`
	Status *string `json:"status,omitempty"`
}

func ValidManagedKeyName(name string) bool {
	return utf8.ValidString(name) && strings.TrimSpace(name) != "" && utf8.RuneCountInString(name) <= 100
}

func (input ManagedKeyUpdate) Validate() error {
	if (input.Name == nil && input.Status == nil) || (input.Name != nil && !ValidManagedKeyName(*input.Name)) || (input.Status != nil && *input.Status != "active" && *input.Status != "disabled") {
		return ErrMainInvalid
	}
	return nil
}
