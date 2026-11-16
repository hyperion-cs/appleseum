package storekit

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/adi"
	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
	"github.com/hyperion-cs/appleseum/applekit/internal/testfixture"
)

type downloadAmd struct {
	ready  bool
	erased int
}

func (p *downloadAmd) HeadersAmd(time.Time) (adi.AmdHeaders, error) {
	if !p.ready {
		return adi.AmdHeaders{}, adi.ErrAmdNotProvisioned
	}
	return adi.AmdHeaders{Otp: bytes.Repeat([]byte{1}, 24), MachineId: bytes.Repeat([]byte{2}, 60)}, nil
}

func (p *downloadAmd) BeginAmd(b []byte, _ time.Time) (adi.AmdProvisioningSession, error) {
	if string(b) != "spim" {
		return nil, errors.New("wrong spim")
	}
	return &downloadProvision{p}, nil
}

func (p *downloadAmd) EraseAmd() error { p.ready = false; p.erased++; return nil }

type downloadProvision struct{ p *downloadAmd }

func (p *downloadProvision) ClientData() []byte { return []byte("cpim") }

func (p *downloadProvision) Finish(ptm, tk []byte) error {
	if string(ptm) != "ptm" || len(tk) != 16 {
		return errors.New("wrong finish")
	}
	p.p.ready = true
	return nil
}

func (p *downloadProvision) Abort() {}

func TestConfiguratorDownloadHandlesSpBeforeClassifyingPlist(t *testing.T) {
	account := downloadAccount()
	firstResponse := downloadReply(t, map[string]any{"failureType": "", "customerMessage": "AMD-Action:ent/download:SP", "m-allowed": false})
	firstResponse.Header.Set("X-Apple-AMD-Action", "SP")
	firstResponse.Header.Set("X-Apple-AMD-Data", base64.StdEncoding.EncodeToString([]byte("spim")))
	first := modernDownloadStep(dispatchStep(t, account, fixtureDispatchUrl, 123, "fixture-kbsync", "", firstResponse), "en-RU")
	first.Header["X-Apple-Amd"], first.Header["X-Apple-Amd-M"] = nil, nil
	finish := applebackemu.Step{Name: "finish download AMD provisioning", Method: "POST", Url: "https://auth.itunes.apple.com/auth/v1/otp/1.5/finish",
		Response: downloadReply(t, map[string]any{"settingInfo": base64.StdEncoding.EncodeToString([]byte("ptm")), "transportKey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 16))}),
		Header:   http.Header{"User-Agent": {fixtureConfiguratorAgent}, "Content-Type": {"application/x-apple-plist"}},
	}
	retryResponse := downloadReply(t, map[string]any{"songList": []any{map[string]any{"URL": fixtureAssetUrl, "sinfs": []any{map[string]any{"sinf": []byte("fixture")}}}}})
	retry := modernDownloadStep(dispatchStep(t, account, fixtureDispatchUrl, 123, "fixture-kbsync", "", retryResponse), "en-RU")
	retry.Header.Set("X-Apple-AMD", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 24)))
	retry.Header.Set("X-Apple-AMD-M", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 60)))
	s, backend := downloadBackend(t, applebackemu.InMemory, account, fixtureDispatchUrl, first, finish, retry)
	p := &downloadAmd{}
	controller := &ams.AmdController{Provider: p, Bag: ams.Bag{AMDFinishURL: finish.Url}}
	if err := s.ConfigureAmd(controller, ams.RequestContext{Language: "en-RU"}); err != nil {
		t.Fatal(err)
	}
	profiles := observeDownloadProfiles(s)
	data, status, err := s.volumeStoreDownload(httpx.WithProfile(context.Background(), httpx.Accounts), 123, "fixture-kbsync", "", false)
	if err != nil || status != 200 || servedItem(data, true) == nil || len(backend.Requests()) != 3 || !p.ready {
		t.Fatalf("SP flow failed: status=%d requests=%d err=%v", status, len(backend.Requests()), err)
	}
	profiles.check(t, 3)
}

func TestDownloadReportsSpWithoutCallingSessionExpired(t *testing.T) {
	account := downloadAccount()
	reply := downloadReply(t, map[string]any{"customerMessage": "AMD-Action:ent/download:SP"})
	reply.Header.Set("X-Apple-AMD-Action", "SP")
	step := dispatchStep(t, account, fixtureDispatchUrl, 123, "kb", "", reply)
	s, _ := downloadBackend(t, applebackemu.InMemory, account, fixtureDispatchUrl, step)
	_, _, err := s.volumeStoreDownload(context.Background(), 123, "kb", "", false)
	var response *DownloadResponseError
	if !errors.As(err, &response) || response.AmdAction != "SP" || response.CustomerMessage != "AMD-Action:ent/download:SP" || errors.Is(err, ErrSessionExpired) {
		t.Fatalf("incorrect diagnosis: %v", err)
	}
}

func TestConfiguratorDownloadAmdStateSurvivesRestore(t *testing.T) {
	d := testfixture.Load(t)
	uuid, record := d.Uuid[:], d.Amd.Record
	encoded := append(append(append([]byte("AMD1"), uuid...), 1), record...)
	provider, err := adi.ParseAmd(encoded)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{Account: Account{Guid: d.Guid, DirectoryServicesId: "123", PasswordToken: "token", StoreFront: "143517-2,34"}, Client: NewSessionHttpClient(), amd: ams.AmdController{Bag: ams.Bag{DownloadURL: "https://downloaddispatch.itunes.apple.com/download"}}}
	controller := &ams.AmdController{Provider: provider, Bag: ams.Bag{AMDFinishURL: "https://auth.itunes.apple.com/auth/v1/otp/1.5/finish", AMDSyncURL: "https://auth.itunes.apple.com/auth/v1/otp/1.5/sync"}}
	if err = s.ConfigureAmd(controller, ams.RequestContext{Platform: httpx.Platform{Model: "MacBookAir9,1", Version: "15.7", Build: "24G222"}, Language: "en-RU"}); err != nil {
		t.Fatal(err)
	}
	before, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(before)
	if err != nil {
		t.Fatal(err)
	}
	if restored.amd.Provider == nil || restored.amd.Bag.AMDFinishURL != controller.Bag.AMDFinishURL || restored.metadata.Platform != s.metadata.Platform {
		t.Fatal("lost AMD action context")
	}
	at := time.Unix(d.Time, 0)
	amd, mid, err := controller.Headers(at)
	if err != nil {
		t.Fatal(err)
	}
	gotAmd, gotMid, err := restored.amd.Headers(at)
	if err != nil || gotAmd != amd || gotMid != mid {
		t.Fatal("restored session changed AMD identity")
	}
}

func TestConfiguratorDownloadStopsBeforeFinalAmdAction(t *testing.T) {
	provider := &downloadAmd{ready: true}
	account := downloadAccount()
	account.StoreFront = "143441-2,34"
	steps := make([]applebackemu.Step, 6)
	for i := range steps {
		response := downloadReply(t, map[string]any{})
		response.Header.Set("X-Apple-AMD-Action", "RP")
		step := modernDownloadStep(dispatchStep(t, account, fixtureDispatchUrl, 123, "kb", "", response), "en-US")
		if i == 0 {
			step.Header.Set("X-Apple-AMD", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 24)))
			step.Header.Set("X-Apple-AMD-M", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 60)))
		} else {
			step.Header["X-Apple-Amd"], step.Header["X-Apple-Amd-M"] = nil, nil
		}
		steps[i] = step
	}
	s, backend := downloadBackend(t, applebackemu.InMemory, account, fixtureDispatchUrl, steps...)
	if err := s.ConfigureAmd(&ams.AmdController{Provider: provider, Bag: ams.Bag{}}, ams.RequestContext{Language: "en-US"}); err != nil {
		t.Fatal(err)
	}
	_, status, err := s.volumeStoreDownload(context.Background(), 123, "kb", "", false)
	var response *DownloadResponseError
	if !errors.As(err, &response) || response.Reason != "AMD_action_limit" || response.AmdAction != "RP" || status != 200 || len(backend.Requests()) != 6 || provider.erased != 5 {
		t.Fatalf("retry limit: requests=%d erasures=%d status=%d err=%v", len(backend.Requests()), provider.erased, status, err)
	}
}

func TestConfigureAmdPreservesSessionEndpoints(t *testing.T) {
	bag := ams.Bag{
		DownloadURL:   "https://buy.itunes.apple.com/download",
		RedownloadURL: "https://buy.itunes.apple.com/redownload",
	}
	s := &Session{Account: Account{StoreFront: "143441"}, amd: ams.AmdController{Bag: bag}}
	controller := &ams.AmdController{Provider: &downloadAmd{}, Bag: ams.Bag{
		DownloadURL:  "https://buy.itunes.apple.com/other-download",
		AMDFinishURL: "https://auth.itunes.apple.com/finish",
		AMDSyncURL:   "https://auth.itunes.apple.com/sync",
	}}
	if err := s.ConfigureAmd(controller, ams.RequestContext{Language: "en-US"}); err != nil {
		t.Fatal(err)
	}
	bag.AMDFinishURL, bag.AMDSyncURL = controller.Bag.AMDFinishURL, controller.Bag.AMDSyncURL
	controller.Bag.DownloadURL, controller.Bag.AMDFinishURL = "", ""
	if got := s.Parameters().Bag; !reflect.DeepEqual(got, bag) {
		t.Fatalf("saved endpoints differ from authenticated session: %#v", got)
	}
}
