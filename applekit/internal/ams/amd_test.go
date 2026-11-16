package ams

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/adi"
	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

type fakeAmdProvider struct {
	provisioned bool
	erased      int
	spim        []byte
	ptm         []byte
	tk          []byte
}

func (p *fakeAmdProvider) BeginAmd(spim []byte, _ time.Time) (adi.AmdProvisioningSession, error) {
	p.spim = bytes.Clone(spim)
	return &fakeAmdProvisioning{owner: p}, nil
}

func (p *fakeAmdProvider) HeadersAmd(time.Time) (adi.AmdHeaders, error) {
	if !p.provisioned {
		return adi.AmdHeaders{}, adi.ErrAmdNotProvisioned
	}
	return adi.AmdHeaders{Otp: bytes.Repeat([]byte{0x44}, 24), MachineId: bytes.Repeat([]byte{0x55}, 60)}, nil
}

func (p *fakeAmdProvider) EraseAmd() error {
	p.erased++
	p.provisioned = false
	return nil
}

type fakeAmdProvisioning struct {
	owner *fakeAmdProvider
	done  bool
}

func (p *fakeAmdProvisioning) ClientData() []byte { return []byte("synthetic-cpim") }

func (p *fakeAmdProvisioning) Finish(ptm, tk []byte) error {
	p.owner.ptm, p.owner.tk = bytes.Clone(ptm), bytes.Clone(tk)
	p.owner.provisioned, p.done = true, true
	return nil
}

func (p *fakeAmdProvisioning) Abort() { p.done = true }

func TestAuthenticateNativeHandlesAmdSpAndRetries(t *testing.T) {
	credentials, device, options := validNativeInput()
	device.Amd, device.AmdMachineId = "", ""
	provider := &fakeAmdProvider{}
	options.Amd = &AmdController{Bag: Bag{
		AMDFinishURL: "https://auth.itunes.apple.com/auth/v1/otp/1.5/finish",
		AMDSyncURL:   "https://auth.itunes.apple.com/auth/v1/otp/1.5/sync",
	}, Provider: provider}
	first := nativeStep(t, credentials, device, options)
	first.Header["X-Apple-Amd"], first.Header["X-Apple-Amd-M"] = nil, nil
	first.Response.Header.Set("X-Apple-AMD-Action", "SP")
	first.Response.Header.Set("X-Apple-AMD-Data", base64.StdEncoding.EncodeToString([]byte("synthetic-spim")))
	reply, err := applebackemu.PlistResponse(map[string]any{"settingInfo": base64.StdEncoding.EncodeToString([]byte("synthetic-ptm")), "transportKey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x77}, 16))})
	if err != nil {
		t.Fatal(err)
	}
	finish, err := applebackemu.PlistRequest("https://auth.itunes.apple.com/auth/v1/otp/1.5/finish", map[string]any{"guid": credentials.Guid, "clientData": base64.StdEncoding.EncodeToString([]byte("synthetic-cpim"))}, http.Header{"Content-Type": {"application/x-apple-plist"}}, reply)
	if err != nil {
		t.Fatal(err)
	}
	retry := nativeStep(t, credentials, device, options)
	retry.Header.Set("X-Apple-AMD", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, 24)))
	retry.Header.Set("X-Apple-AMD-M", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x55}, 60)))
	fixture := nativeFixture(t, applebackemu.InMemory, first, finish, retry)
	client := fixture.Client()
	if _, err := AuthenticateNative(context.Background(), client, "https://auth.itunes.apple.com/auth/v1/native", &nativeSigner{}, credentials, device, options); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Requests()) != 3 || string(provider.spim) != "synthetic-spim" || string(provider.ptm) != "synthetic-ptm" || len(provider.tk) != 16 {
		t.Fatal("AMD SP exchange did not reach the provider")
	}
}

func TestAmdActionSmIsExplicitlyUnavailableAndRpIsLocal(t *testing.T) {
	provider := &fakeAmdProvider{provisioned: true}
	controller := &AmdController{Bag: Bag{AMDSyncURL: "https://auth.itunes.apple.com/auth/v1/otp/1.5/sync"}, Provider: provider}
	metadata := RequestContext{Actor: Configurator, Storefront: "143517-2,34", Language: "en-RU", UtcOffsetSeconds: 10_800}
	header := make(http.Header)
	header.Set("X-Apple-AMD-Action", "SM")
	header.Set("X-Apple-AMD-Data", base64.StdEncoding.EncodeToString([]byte("synthetic-sim")))
	client := nativeFixture(t, applebackemu.InMemory).Client()
	if handled, err := controller.HandleAction(context.Background(), client, metadata, header, "001122334455", time.Now()); handled || !errors.Is(err, ErrAmdSynchronizeUnavailable) {
		t.Fatalf("unexpected SM result: handled=%v err=%v", handled, err)
	}
	header = make(http.Header)
	header.Set("X-Apple-AMD-Action", "RP")
	if handled, err := controller.HandleAction(context.Background(), client, metadata, header, "001122334455", time.Now()); err != nil || !handled || provider.erased != 1 {
		t.Fatalf("RP failed: handled=%v err=%v erased=%d", handled, err, provider.erased)
	}
}

func TestAmdActionRejectsMalformedHeaders(t *testing.T) {
	controller := &AmdController{Bag: Bag{}, Provider: &fakeAmdProvider{}}
	client := nativeFixture(t, applebackemu.InMemory).Client()
	metadata := RequestContext{Actor: Configurator, Storefront: "143517", Language: "en-RU"}
	for _, fields := range [][2]string{
		{"SP", "%%%"},
		{"RP", base64.StdEncoding.EncodeToString([]byte("unexpected"))},
		{"unknown", ""},
	} {
		header := make(http.Header)
		header.Set("X-Apple-AMD-Action", fields[0])
		if fields[1] != "" {
			header.Set("X-Apple-AMD-Data", fields[1])
		}
		if handled, err := controller.HandleAction(context.Background(), client, metadata, header, "guid", time.Now()); handled || err == nil {
			t.Fatalf("malformed action accepted: handled=%v err=%v", handled, err)
		}
	}
}

func TestAuthenticateNativeStopsBeforeFinalAmdAction(t *testing.T) {
	credentials, device, options := validNativeInput()
	device.Amd, device.AmdMachineId = "", ""
	provider := &fakeAmdProvider{provisioned: true}
	options.Amd = &AmdController{Provider: provider, Bag: Bag{AMDFinishURL: "https://auth.itunes.apple.com/auth/v1/otp/1.5/finish"}}
	steps := make([]applebackemu.Step, 6)
	for i := range steps {
		steps[i] = nativeStep(t, credentials, device, options)
		steps[i].Response.Header.Set("X-Apple-AMD-Action", "RP")
		if i == 0 {
			steps[i].Header.Set("X-Apple-AMD", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, 24)))
			steps[i].Header.Set("X-Apple-AMD-M", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x55}, 60)))
		} else {
			steps[i].Header["X-Apple-Amd"], steps[i].Header["X-Apple-Amd-M"] = nil, nil
		}
	}
	fixture := nativeFixture(t, applebackemu.InMemory, steps...)
	client := fixture.Client()
	_, err := AuthenticateNative(context.Background(), client, "https://auth.itunes.apple.com/auth/v1/native", &nativeSigner{}, credentials, device, options)
	var native *NativeError
	if !errors.As(err, &native) || native.Reason != "amd_action_limit" || len(fixture.Requests()) != 6 || provider.erased != 5 {
		t.Fatalf("retry limit: requests=%d erasures=%d err=%v", len(fixture.Requests()), provider.erased, err)
	}
}
