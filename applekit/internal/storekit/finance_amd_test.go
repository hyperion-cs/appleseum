package storekit

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"howett.net/plist"

	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

func TestFinanceHandlesSpBeforeAuthenticationAndRetriesTheSameBody(t *testing.T) {
	expectedContext := catalogContext()
	expectedContext.Header = http.Header{
		"User-Agent":        {"MacAppStore/3.0 MacAppStore/3.0 (Macintosh; OS X 15.7; 24G222) AppleWebKit/1624.2.5.11.4 AMS/1 (dt:1)"},
		"X-Mme-Client-Info": {"<MacBookAir9,1> <macOS;15.7;24G222> <com.apple.AppStore/3.0 (com.apple.AppStore/3.0)>"},
	}
	input := map[string]any{"dateRangeType": "CUSTOM", "customYear": 2_026, "contentTypeGroups": []string{"APP"}}
	challenge := catalogReply(`{"message":"X-Apple-AMD-Action:SP"}`)
	challenge.StatusCode = 401
	challenge.Header.Set("X-Apple-AMD-Action", "SP")
	challenge.Header.Set("X-Apple-AMD-Data", base64.StdEncoding.EncodeToString([]byte("spim")))
	challenge.Header.Set("Set-Cookie", "challenge=kept; Path=/; Domain=.apple.com; Secure")
	first, err := applebackemu.FinanceRequest(expectedContext, "https://ph2.itunes.apple.com/purchaseHistory", input, challenge)
	if err != nil {
		t.Fatal(err)
	}
	first.Header["X-Apple-Amd"], first.Header["X-Apple-Amd-M"] = nil, nil
	finishResponse, err := applebackemu.PlistResponse(map[string]any{"settingInfo": base64.StdEncoding.EncodeToString([]byte("ptm")), "transportKey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 16))})
	if err != nil {
		t.Fatal(err)
	}
	finish := applebackemu.Step{
		Name: "AMD finish for history", Method: "POST", Url: "https://auth.itunes.apple.com/auth/v1/otp/1.5/finish", Response: finishResponse,
		Header: http.Header{
			"Content-Type": {"application/x-apple-plist"}, "Cookie": {"challenge=kept"},
			"User-Agent": {"Configurator/2.21 Configurator/2.21 (Macintosh; OS X 15.7; 24G222) AppleWebKit/1624.2.5.11.4 AMS/1 (dt:1)"},
		},
		Check: func(request applebackemu.Request) error {
			var body map[string]any
			if _, err := plist.Unmarshal(request.Body, &body); err != nil || len(body) != 2 || body["guid"] != "001122AABBCC" || body["clientData"] != base64.StdEncoding.EncodeToString([]byte("cpim")) {
				return fmt.Errorf("AMD finish body mismatch")
			}
			return nil
		},
	}
	retry, err := applebackemu.FinanceRequest(expectedContext, "https://ph2.itunes.apple.com/purchaseHistory", input, catalogReply(`{"purchases":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	retry.Header.Set("X-Apple-AMD", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 24)))
	retry.Header.Set("X-Apple-AMD-M", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 60)))
	retry.Header.Set("Cookie", "challenge=kept")
	s, backend := catalogBackend(t, applebackemu.InMemory, first, finish, retry)
	p := &downloadAmd{}
	controller := &ams.AmdController{Provider: p, Bag: ams.Bag{AMDFinishURL: "https://auth.itunes.apple.com/auth/v1/otp/1.5/finish"}}
	if err := s.ConfigureAmd(controller, ams.RequestContext{Language: "en-us"}); err != nil {
		t.Fatal(err)
	}
	s.metadata.Platform = httpx.Platform{Model: "MacBookAir9,1", Version: "15.7", Build: "24G222"}
	var page purchasePage
	err = s.financeJson(context.Background(), "https://ph2.itunes.apple.com/purchaseHistory", purchaseCriteria{"CUSTOM", 2_026, []string{"APP"}}, &page, "purchases")
	if err != nil || page.Purchases == nil || !p.ready {
		t.Fatal("SP treated as expired login or empty history", err)
	}
	requests := backend.Requests()
	if len(requests) != 3 || !bytes.Equal(requests[0].Body, requests[2].Body) {
		t.Fatal("AMD retry changed history criteria")
	}
}

func TestFinanceAmdActionsAreBoundedAndIncompleteChallengesAreExplicit(t *testing.T) {
	for _, kind := range []string{"RP", "missing-data", "message-only", "no-controller"} {
		t.Run(kind, func(t *testing.T) {
			response := catalogReply(`{"message":"X-Apple-AMD-Action:SP"}`)
			want := 1
			if kind == "RP" {
				response.Header.Set("X-Apple-AMD-Action", "RP")
				want = 3
			} else if kind != "message-only" {
				response.Header.Set("X-Apple-AMD-Action", "SP")
			}
			steps := make([]applebackemu.Step, want)
			for i := range steps {
				steps[i] = settingsStep(t)
				steps[i].Response = response
			}
			s, backend := catalogBackend(t, applebackemu.InMemory, steps...)
			if kind != "no-controller" {
				if err := s.ConfigureAmd(&ams.AmdController{Provider: &downloadAmd{}, Bag: ams.Bag{}}, ams.RequestContext{Language: "en-us"}); err != nil {
					t.Fatal(err)
				}
			}
			err := s.financeJson(context.Background(), "https://ph2.itunes.apple.com/settings", nil, new(any), "purchases")
			if err == nil || errors.Is(err, ErrSessionExpired) || !strings.Contains(err.Error(), "AMD") || len(backend.Requests()) != want {
				t.Fatal("challenge lost or repeated without a bound", len(backend.Requests()), err)
			}
		})
	}
}
