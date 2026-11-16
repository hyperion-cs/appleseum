package account

import (
	"context"
	"errors"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/db"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

const (
	profileRefreshInterval = 3 * time.Second
)

// Profile is the account summary the profile screen shows: the cached Apple
// profile and the purchase counts.
func Profile(ctx context.Context, s *db.Db, owner string) (wire.ProfileSummary, error) {
	cache, err := s.Profile(ctx, owner)
	if err != nil {
		return wire.ProfileSummary{}, err
	}
	result := wire.ProfileSummary{AppleError: cache.Error}
	if p := cache.Data; p != nil {
		result.Apple = &wire.AppleProfile{Nickname: p.Nickname, FamilyRole: p.FamilyRole, Balance: p.Balance, Subscriptions: p.Subscriptions}
	}
	result.CatalogApps, result.DelistedApps, err = s.CatalogCounts(ctx, owner)
	return result, err
}

// RefreshProfile asks Apple again unless the last attempt was moments ago. A
// failed attempt keeps the previous profile and records why.
func RefreshProfile(ctx context.Context, s *db.Db, owner string, fetch func(context.Context) (applekit.AccountProfile, error)) (wire.ProfileSummary, error) {
	cache, err := s.Profile(ctx, owner)
	if err != nil {
		return wire.ProfileSummary{}, err
	}
	if age := time.Since(cache.Attempted); cache.Attempted.IsZero() || age < 0 || age >= profileRefreshInterval {
		data, err := fetch(ctx)
		var code fault.Code
		switch {
		case err == nil:
			cache.Data = &data
		case errors.As(err, &code):
		case errors.Is(err, applekit.ErrSessionExpired):
			code = fault.ProfileAuthenticationRequired
		default:
			code = fault.ProfileUnavailable
		}
		if err = ctx.Err(); err != nil {
			return wire.ProfileSummary{}, err
		}
		cache.Error = code
		if err = s.SaveProfile(ctx, owner, cache); err != nil {
			return wire.ProfileSummary{}, err
		}
	}
	return Profile(ctx, s, owner)
}
