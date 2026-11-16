package storekit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

const (
	accountProfileQuery = `query requestAccountSummary {
 account { appleId familySharing { isHoh isInPaymentSharingFamily isNonHoHMemberInFamilyPlan isFamilyPaymentFlexMember } }
 authorizations { authorizedComputers linkedDevices }
 purchases { subscriptions preOrders }
 payments { balance balanceWithFreeContentCredit }
 settings { nickname }
 community { reviewsAndRatings ratingsAndReviewsBaseUrl }
}`
)

// Finance uses Mac App Store TLS; its auth failures do not imply download expiry.
func (s *Session) financeJson(ctx context.Context, endpoint string, input, output any, operation string) error {
	var body []byte
	method := http.MethodGet
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil {
			return err
		}
		method = http.MethodPost
	}

	if s.amd.Provider != nil {
		ctx = httpx.WithPlatform(ctx, s.metadata.Platform)
	}
	ctx = httpx.WithProfile(ctx, httpx.MacAppStore)
	client := httpx.NoRedirect(s.Client)
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", httpx.UserAgentFor(ctx))
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Accept-Encoding", "br, gzip, deflate")
		req.Header.Set("Accept-Language", "en-us")
		req.Header.Set("X-Apple-Client-Application", "com.apple.AppStore")
		req.Header.Set("X-Mme-Client-Info", httpx.PlatformFromContext(ctx).ClientInfo("com.apple.AppStore/3.0 (com.apple.AppStore/3.0)"))
		req.Header.Set("X-Apple-Store-Front", strings.Split(s.Account.StoreFront, "-")[0]+"-2,13")
		req.Header.Set("X-Dsid", s.Account.DirectoryServicesId)
		if input != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if err := s.addAmdHeaders(req.Header); err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		raw, err := httpx.ReadResponseBody(resp, 33_554_432)
		resp.Body.Close()
		if err != nil {
			return err
		}
		if action := resp.Header.Get("X-Apple-AMD-Action"); action != "" {
			if s.amd.Provider == nil {
				return fmt.Errorf("%s requires AMD action %q without a controller", operation, action)
			}
			if attempt == 2 {
				return fmt.Errorf("%s exceeded AMD action limit", operation)
			}
			handled, err := s.amd.HandleAction(ctx, s.Client, s.metadata, resp.Header, s.Account.Guid, s.now())
			if err != nil {
				return fmt.Errorf("%s AMD action: %w", operation, err)
			}
			if handled {
				continue
			}
		}
		// A JSON message alone cannot supply the SPIM needed by the AMD controller.
		if bytes.Contains(raw, []byte("X-Apple-AMD-Action:")) {
			var message struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(raw, &message) == nil && strings.HasPrefix(message.Message, "X-Apple-AMD-Action:") {
				return fmt.Errorf("%s returned an AMD challenge without complete action headers", operation)
			}
		}
		if authenticationRejected(resp.StatusCode, raw) {
			return s.rejected(operation, resp.StatusCode)
		}
		if resp.StatusCode != http.StatusOK {
			return &httpx.HttpError{StatusCode: resp.StatusCode}
		}
		if err = json.Unmarshal(raw, output); err != nil {
			return fmt.Errorf("decode %s: %w", operation, err)
		}
		return nil
	}
	return fmt.Errorf("%s exceeded AMD action limit", operation)
}

func (s *Session) buyHost() string {
	if s.Account.Pod != "" {
		return "p" + s.Account.Pod + "-buy.itunes.apple.com"
	}
	return "buy.itunes.apple.com"
}

// Nil counts mean Apple omitted a value; zero is a reported count.
type AccountProfile struct {
	Nickname            string `json:"nickname"`
	FamilyRole          string `json:"familyRole"`
	Balance             string `json:"balance"`
	AuthorizedComputers *int64 `json:"authorizedComputers"`
	LinkedDevices       *int64 `json:"linkedDevices"`
	Subscriptions       *int64 `json:"subscriptions"`
	PreOrders           *int64 `json:"preOrders"`
	ReviewsAndRatings   *int64 `json:"reviewsAndRatings"`
	ReviewsUrl          string `json:"reviewsUrl"`
}

func (s *Session) AccountProfile(ctx context.Context) (AccountProfile, error) {
	var response struct {
		Errors []json.RawMessage `json:"errors"`
		Data   *struct {
			Account *struct {
				AppleId       string `json:"appleId"`
				FamilySharing *struct {
					IsHoh                  bool `json:"isHoh"`
					InPaymentSharingFamily bool `json:"isInPaymentSharingFamily"`
					NonHohMember           bool `json:"isNonHoHMemberInFamilyPlan"`
					PaymentFlexMember      bool `json:"isFamilyPaymentFlexMember"`
				} `json:"familySharing"`
			} `json:"account"`
			Authorizations struct {
				Computers *int64 `json:"authorizedComputers"`
				Devices   *int64 `json:"linkedDevices"`
			} `json:"authorizations"`
			Purchases struct {
				Subscriptions *int64 `json:"subscriptions"`
				PreOrders     *int64 `json:"preOrders"`
			} `json:"purchases"`
			Payments struct {
				Balance               string `json:"balance"`
				WithFreeContentCredit string `json:"balanceWithFreeContentCredit"`
			} `json:"payments"`
			Settings struct {
				Nickname string `json:"nickname"`
			} `json:"settings"`
			Community struct {
				Reviews *int64 `json:"reviewsAndRatings"`
				Url     string `json:"ratingsAndReviewsBaseUrl"`
			} `json:"community"`
		} `json:"data"`
	}
	input := struct {
		Query string `json:"query"`
	}{accountProfileQuery}
	if err := s.financeJson(ctx, "https://"+s.buyHost()+"/account/v1/summary/graphql", input, &response, "profile"); err != nil {
		return AccountProfile{}, err
	}
	if len(response.Errors) > 0 || response.Data == nil || response.Data.Account == nil || response.Data.Account.AppleId == "" {
		return AccountProfile{}, fmt.Errorf("account summary is missing or contains GraphQL errors")
	}
	d := response.Data
	p := AccountProfile{Nickname: d.Settings.Nickname, Balance: d.Payments.Balance,
		AuthorizedComputers: d.Authorizations.Computers, LinkedDevices: d.Authorizations.Devices,
		Subscriptions: d.Purchases.Subscriptions, PreOrders: d.Purchases.PreOrders,
		ReviewsAndRatings: d.Community.Reviews}
	if p.Balance == "" {
		p.Balance = d.Payments.WithFreeContentCredit
	}
	if f := d.Account.FamilySharing; f != nil {
		p.FamilyRole = "none"
		if f.IsHoh {
			p.FamilyRole = "organizer"
		} else if f.InPaymentSharingFamily || f.NonHohMember || f.PaymentFlexMember {
			p.FamilyRole = "member"
		}
	}
	if u, err := url.Parse(d.Community.Url); err == nil && u.Scheme == "https" && u.User == nil && httpx.IsAppleHost(u.Hostname()) {
		p.ReviewsUrl = u.String()
	}
	s.authenticated()
	return p, nil
}
