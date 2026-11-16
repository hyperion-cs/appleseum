package storekit

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

func TestAccountProfileUsesMacContextAndPreservesOptionalCounts(t *testing.T) {
	s, _ := catalogBackend(t, applebackemu.InMemory, summaryStep(t, `{"data":{"account":{"appleId":"fixture@example.test","familySharing":{"isHoh":true}},"authorizations":{"authorizedComputers":0,"linkedDevices":2},"purchases":{"subscriptions":null,"preOrders":0},"payments":{"balance":null,"balanceWithFreeContentCredit":"0.00 USD"},"settings":{"nickname":"Fixture"},"community":{"reviewsAndRatings":3,"ratingsAndReviewsBaseUrl":"https://itunes.apple.com/reviews"}}}`))
	p, err := s.AccountProfile(context.Background())
	if err != nil || p.FamilyRole != "organizer" || p.Nickname != "Fixture" || p.Balance != "0.00 USD" || p.Subscriptions != nil || p.PreOrders == nil || *p.PreOrders != 0 || p.AuthorizedComputers == nil || *p.AuthorizedComputers != 0 || p.LinkedDevices == nil || *p.LinkedDevices != 2 || p.ReviewsUrl != "https://itunes.apple.com/reviews" {
		t.Fatal("profile confused absent/zero data or lost account fields", p, err)
	}
}

func TestAccountProfileRejectsErrorsAndUntrustedReviewLinks(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"data":{}}`, `{"data":{"account":{"appleId":"fixture"}},"errors":[{"message":"private detail"}]}`} {
		s, _ := catalogBackend(t, applebackemu.InMemory, summaryStep(t, body))
		if _, err := s.AccountProfile(context.Background()); err == nil || strings.Contains(err.Error(), "private detail") {
			t.Fatal("invalid summary accepted or error body leaked", err)
		}
	}
	for _, link := range []string{"javascript:alert(1)", "https://other.example.test/reviews", "https://user@itunes.apple.com/"} {
		s, _ := catalogBackend(t, applebackemu.InMemory, summaryStep(t, `{"data":{"account":{"appleId":"fixture"},"community":{"ratingsAndReviewsBaseUrl":`+strconv.Quote(link)+`}}}`))
		p, err := s.AccountProfile(context.Background())
		if err != nil || p.ReviewsUrl != "" {
			t.Fatal("untrusted link exposed", err)
		}
	}
}

func TestAccountProfileAuthenticationRejectionIsScoped(t *testing.T) {
	step := summaryStep(t, `{}`)
	step.Response.StatusCode = 401
	s, _ := catalogBackend(t, applebackemu.InMemory, step)
	_, err := s.AccountProfile(context.Background())
	var rejected *SessionRejection
	if !errors.As(err, &rejected) || rejected.Operation != "profile" || !errors.Is(err, ErrSessionExpired) {
		t.Fatal("lost scoped profile rejection", err)
	}
}
