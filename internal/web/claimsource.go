package web

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wxalerts/pws-gateway/internal/claim"
)

// NewClaimSource adapts the claim manager to ClaimSource.
func NewClaimSource(m *claim.Manager) ClaimSource { return claimSource{m: m} }

// claimSource adapts claim.Manager to ClaimSource.
type claimSource struct{ m *claim.Manager }

func (c claimSource) Claim() ClaimStatus { return toWebClaim(c.m.Status()) }

func (c claimSource) ClaimCancel() ClaimStatus { return toWebClaim(c.m.Cancel()) }

func (c claimSource) ClaimUnlink() error { return c.m.Unlink() }

func (c claimSource) ClaimStart(ctx context.Context) (ClaimStatus, error) {
	st, err := c.m.Start(ctx)
	switch {
	case err == nil:
		return toWebClaim(st), nil
	case errors.Is(err, claim.ErrClaimed):
		return ClaimStatus{}, ErrClaimLinked
	case errors.Is(err, claim.ErrAlreadyClaimed):
		return ClaimStatus{}, ErrClaimTaken
	case errors.Is(err, claim.ErrRateLimited):
		return ClaimStatus{}, ErrClaimBusy
	default:
		return ClaimStatus{}, fmt.Errorf("%w: %w", ErrClaimUnreachable, err)
	}
}

func toWebClaim(s claim.Status) ClaimStatus {
	opt := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	out := ClaimStatus{
		State: string(s.State), UserCode: opt(s.UserCode), VerificationURI: opt(s.VerificationURI),
		VerificationURIComplete: opt(s.VerificationURIComplete), DeviceID: opt(s.DeviceID), Error: opt(s.Error),
	}
	if s.ExpiresAt != nil {
		out.ExpiresAt = opt(s.ExpiresAt.UTC().Format(time.RFC3339))
	}
	return out
}
