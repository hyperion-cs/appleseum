package ams

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

const (
	Configurator Actor = iota
	Accounts
)

type Actor uint8

// RequestContext contains metadata that must agree in the query and headers.
type RequestContext struct {
	Platform         httpx.Platform `json:",omitzero"`
	Actor            Actor
	Storefront       string
	Language         string
	UtcOffsetSeconds int `json:"UTCOffsetSeconds"`
}

type actorInfo struct {
	product, version, profile string
	tls                       httpx.Profile
}

var storefrontPattern = regexp.MustCompile(`^[0-9]{6}(-[0-9]{1,6})?(,[0-9]{1,6})?$`)

func (a Actor) info() (actorInfo, error) {
	switch a {
	case Configurator:
		return actorInfo{"Configurator", httpx.ConfiguratorVersion, "Accounts", httpx.Configurator}, nil
	case Accounts:
		return actorInfo{"com.apple.accountsd", httpx.AccountsVersion, "AppStore", httpx.Accounts}, nil
	default:
		return actorInfo{}, errors.New("unknown AMS actor")
	}
}

func (c RequestContext) Validate() error {
	if err := c.Platform.Validate(); err != nil {
		return err
	}
	if _, err := c.Actor.info(); err != nil {
		return err
	}
	if !storefrontPattern.MatchString(c.Storefront) {
		return errors.New("invalid storefront")
	}
	if strings.TrimSpace(c.Language) == "" || len(c.Language) > 100 {
		return errors.New("invalid language")
	}
	for _, r := range c.Language {
		if r < ' ' || r > '~' {
			return errors.New("invalid language")
		}
	}
	if c.UtcOffsetSeconds < -24*60*60 || c.UtcOffsetSeconds > 24*60*60 {
		return errors.New("invalid UTC offset")
	}
	return nil
}

func (c RequestContext) Headers() (http.Header, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	actor, _ := c.Actor.info()
	application := actor.product
	if c.Actor == Configurator {
		application = "com.apple.configurator.ui"
	}
	return http.Header{
		"User-Agent": {c.Platform.UserAgent(actor.tls)}, "Accept": {"*/*"},
		"Accept-Encoding": {"br, gzip, deflate"}, "Accept-Language": {c.Language},
		"Priority": {"u=3, i"}, "X-Apple-Client-Application": {application},
		"X-Apple-Store-Front": {c.Storefront}, "X-Apple-Tz": {strconv.Itoa(c.UtcOffsetSeconds)},
	}, nil
}

type Bag struct {
	AuthenticateURL   string              `json:"authenticateAccount"`
	SAPSetupURL       string              `json:"sign-sap-setup"`
	SAPCertificateURL string              `json:"sign-sap-setup-cert"`
	AMDFinishURL      string              `json:"amd-finish-provisioning"`
	AMDSyncURL        string              `json:"amd-sync-machine"`
	MDFinishTemplate  string              `json:"md-finish-provisioning"`
	DownloadURL       string              `json:"volumeStoreDownloadProduct"`
	RedownloadURL     string              `json:"redownloadProduct"`
	AMDDomains        []string            `json:"amd-domains"`
	SAPRequestRules   map[string][]string `json:"sign-sap-request"`
}

// Without a bag cache, conditional requests could return an unusable 304.
func FetchBag(ctx context.Context, client *http.Client, metadata RequestContext) (*Bag, error) {
	if client == nil {
		return nil, errors.New("AMS bag requires the shared HTTP client")
	}
	headers, err := metadata.Headers()
	if err != nil {
		return nil, err
	}
	actor, _ := metadata.Actor.info()
	endpoint := &url.URL{Scheme: "https", Host: "bag.itunes.apple.com", Path: "/bag.xml"}
	endpoint.RawQuery = url.Values{
		"deviceClass": {"Macintosh"}, "format": {"json"}, "os": {"OS X"},
		"osVersion": {metadata.Platform.Defaults().Version}, "product": {actor.product}, "productVersion": {actor.version},
		"profile": {actor.profile}, "profileVersion": {"1"}, "storefront": {metadata.Storefront},
	}.Encode()
	request, err := http.NewRequestWithContext(httpx.WithProfile(httpx.WithPlatform(ctx, metadata.Platform), actor.tls), http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header = headers
	request.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch AMS bag: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &httpx.HttpError{StatusCode: response.StatusCode}
	}
	body, err := httpx.ReadResponseBody(response, 2_097_152)
	if err != nil {
		return nil, fmt.Errorf("read AMS bag: %w", err)
	}
	return ParseBag(body)
}

func ParseBag(body []byte) (*Bag, error) {
	if len(body) > 2_097_152 {
		return nil, errors.New("AMS bag exceeds size limit")
	}
	var bag Bag
	if err := json.Unmarshal(body, &bag); err != nil {
		return nil, fmt.Errorf("decode AMS bag: %w", err)
	}
	for _, endpoint := range []struct {
		name, value string
		required    bool
	}{
		{"authenticateAccount", bag.AuthenticateURL, true},
		{"sign-sap-setup", bag.SAPSetupURL, true},
		{"sign-sap-setup-cert", bag.SAPCertificateURL, true},
		{"amd-finish-provisioning", bag.AMDFinishURL, false},
		{"amd-sync-machine", bag.AMDSyncURL, false},
		{"volumeStoreDownloadProduct", bag.DownloadURL, false},
		{"redownloadProduct", bag.RedownloadURL, false},
	} {
		if endpoint.value == "" && endpoint.required {
			return nil, fmt.Errorf("AMS bag is missing %s", endpoint.name)
		}
		if endpoint.value == "" {
			continue
		}
		if !httpx.IsAppleUrl(endpoint.value) {
			return nil, fmt.Errorf("AMS bag has an invalid %s URL", endpoint.name)
		}
	}
	return &bag, nil
}

// NeedsAmd applies the bag's exact-host and leading-star rules.
func (b *Bag) NeedsAmd(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, rule := range b.AMDDomains {
		rule = strings.ToLower(rule)
		if strings.HasPrefix(rule, "*") {
			if suffix := rule[1:]; suffix != "" && strings.HasSuffix(host, suffix) {
				return true
			}
		} else if host == rule {
			return true
		}
	}
	return false
}
