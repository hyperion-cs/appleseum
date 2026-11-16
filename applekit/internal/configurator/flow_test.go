package configurator

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/adi"
	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
	"github.com/hyperion-cs/appleseum/applekit/internal/grandslam"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
	"github.com/hyperion-cs/appleseum/applekit/internal/sap"
	"github.com/hyperion-cs/appleseum/applekit/internal/storekit"
)

type testAmdProvider struct{}

func (testAmdProvider) BeginAmd([]byte, time.Time) (adi.AmdProvisioningSession, error) {
	return nil, errors.New("synthetic provider does not implement SP")
}

func (testAmdProvider) HeadersAmd(time.Time) (adi.AmdHeaders, error) {
	return adi.AmdHeaders{}, adi.ErrAmdNotProvisioned
}

func (testAmdProvider) EraseAmd() error { return nil }

func (testAmdProvider) MarshalBinary() ([]byte, error) {
	provider, err := adi.NewAmd([16]byte{1})
	if err != nil {
		return nil, err
	}
	return provider.MarshalBinary()
}

type testSigner struct{ closed bool }

func (*testSigner) Sign([]byte) ([]byte, error) { return bytes.Repeat([]byte{0x55}, 501), nil }

func (s *testSigner) Close() error { s.closed = true; return nil }

func testDevice() DeviceSnapshot {
	at := time.Date(2_026, 11, 1, 12, 30, 0, 0, time.UTC)
	native := ams.NativeDevice{
		LocalUser: "synthetic-local-user", Md: "synthetic-md", MachineId: "synthetic-mid",
		Routing: "17106176",
	}
	return DeviceSnapshot{
		Cpd: map[string]any{
			"X-Apple-I-MD": native.Md, "X-Apple-I-MD-M": native.MachineId,
			"X-Apple-I-MD-LU": native.LocalUser, "X-Apple-I-MD-RINFO": native.Routing,
			"X-Mme-Device-Id":       "00000000-0000-4000-8000-000000000001",
			"X-Apple-I-Client-Time": at.Format(time.RFC3339), "X-Apple-Locale": "en_RU",
			"X-Apple-I-TimeZone": "Europe/Moscow", "svct": "itunesstore", "at": uint64(2),
			"capp": "Apple Configurator", "cbid": "com.apple.configurator.ui",
		},
		Native: native, Guid: "001122aabbcc",
		ClientInfo: "<Synthetic Mac> <macOS;26.5;25F71> <com.apple.AuthKit/1>",
		Language:   "en-GB,en;q=0.9", Locale: "en_RU", TimeZone: "Europe/Moscow",
		ComplianceId: "RU", ClientTime: at,
	}
}

func testFlow(t *testing.T) *Flow {
	t.Helper()
	flow, err := New(Options{
		Http: storekit.NewSessionHttpClient(),
		Context: ams.RequestContext{
			Actor: ams.Configurator, Storefront: "143517-2,34", Language: "en-RU", UtcOffsetSeconds: 10_800,
		},
		Device: testDevice(), Amd: &ams.AmdController{Provider: testAmdProvider{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return flow
}

func backendAccount() applebackemu.AuthAccount {
	return applebackemu.AuthAccount{AppleId: "person@example.invalid", Password: "synthetic-password", Adsid: "synthetic-adsid", Idms: "synthetic-idms", DirectoryServicesId: "123456789", Pet: "synthetic-pet=PET"}
}

func backendLoginSteps(t *testing.T, rejectFirstCode bool) []applebackemu.Step {
	t.Helper()
	device := testDevice()
	options := applebackemu.GrandSlamOptions{
		Account: backendAccount(), Device: applebackemu.AuthDevice{ClientInfo: device.ClientInfo, Language: device.Language, ClientData: device.Cpd}, RequireVerification: true,
	}
	first, err := applebackemu.GrandSlam(options)
	if err != nil {
		t.Fatal(err)
	}
	factor, err := applebackemu.TrustedDevice(applebackemu.TrustedDeviceOptions{Device: options.Device, Adsid: options.Account.Adsid, Idms: options.Account.Idms, Code: "123456"})
	if err != nil {
		t.Fatal(err)
	}
	steps := append(first, factor[0])
	if rejectFirstCode {
		rejected := factor[1]
		rejected.Header = rejected.Header.Clone()
		rejected.Header.Set("Security-Code", "111111")
		rejected.Response, err = applebackemu.PlistResponse(map[string]any{"ec": -21_669})
		if err != nil {
			t.Fatal(err)
		}
		steps = append(steps, rejected)
	}
	steps = append(steps, factor[1])
	options.RequireVerification = false
	options.RequireVerifiedCookie = true
	fresh, err := applebackemu.GrandSlam(options)
	if err != nil {
		t.Fatal(err)
	}
	return append(steps, fresh...)
}

func backendFlow(t *testing.T, mode applebackemu.Mode, steps ...applebackemu.Step) (*Flow, *applebackemu.Server) {
	t.Helper()
	fixture, err := applebackemu.New(mode, steps...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		fixture.Close()
		if err := fixture.Verify(); err != nil {
			t.Error(err)
		}
	})
	flow, err := New(Options{
		Http:    storekit.NewSessionHttpClientWithTransport(fixture.Client().Transport),
		Context: ams.RequestContext{Actor: ams.Configurator, Storefront: "143517-2,34", Language: "en-RU", UtcOffsetSeconds: 10_800},
		Device:  testDevice(), Amd: &ams.AmdController{Provider: testAmdProvider{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return flow, fixture
}

func TestFlowComposesVerificationNativeAndStoreSession(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode applebackemu.Mode
	}{{"memory", applebackemu.InMemory}, {"https", applebackemu.HTTPS}} {
		t.Run(mode.name, func(t *testing.T) {
			steps := backendLoginSteps(t, false)
			bag, err := applebackemu.ServiceBag(applebackemu.BagOptions{Storefront: "143517-2,34", Language: "en-RU", UtcOffsetSeconds: 10_800})
			if err != nil {
				t.Fatal(err)
			}
			native, err := applebackemu.NativeAuthentication(applebackemu.NativeOptions{
				Account: backendAccount(), Guid: "001122AABBCC", Signature: bytes.Repeat([]byte{0x55}, 501), PasswordToken: "synthetic-password-token", Storefront: "143517-2,34", Pod: "42",
				Header: http.Header{
					"Accept-Language": {"en-RU"}, "X-Apple-Store-Front": {"143517-2,34"}, "X-Apple-Tz": {"10800"},
					"X-Apple-I-MD": {"synthetic-md"}, "X-Apple-I-MD-M": {"synthetic-mid"}, "X-Apple-I-MD-LU": {"synthetic-local-user"}, "X-Apple-I-MD-RINFO": {"17106176"},
					"X-Apple-I-Client-Time": {"2026-11-01T12:30:00Z"}, "X-Apple-I-Locale": {"en_RU"}, "X-Apple-I-TimeZone": {"Europe/Moscow"}, "X-Apple-Age-Area-Compliance-ID": {"RU"},
					"X-Apple-AMD": nil, "X-Apple-AMD-M": nil, "Cookie": nil,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			native.Response.Header.Set("Set-Cookie", "store-session=issued; Domain=.itunes.apple.com; Path=/; Secure")
			flow, fixture := backendFlow(t, mode.mode, append(steps, bag, native)...)
			signer := &testSigner{}
			// SAP cryptography has independent tests. Only its signer is substituted;
			// GrandSlam, 2FA, bag, native auth and the store bridge use real client code.

			flow.deps.newSigner = func(ctx context.Context, config sap.Config) (sap.ActionSigner, error) {
				if config.HttpClient != flow.http || config.SetupUrl != "https://fpinit.itunes.apple.com/v1/signSapSetup/legacy" || !bytes.Equal(config.HardwareId, []byte{0, 0x11, 0x22, 0xaa, 0xbb, 0xcc}) {
					t.Error("SAP stage lost identity or bag")
				}
				if httpx.UserAgentFor(ctx) != httpx.DefaultAccountsUserAgent || config.RequestHeaders.Get("User-Agent") != httpx.DefaultAccountsUserAgent || config.RequestHeaders.Get("X-Apple-Client-Application") != "com.apple.accountsd" || config.RequestHeaders.Get("X-Apple-Store-Front") != "143517-2,34" {
					t.Error("SAP stage lost accountsd metadata")
				}
				return signer, nil
			}

			account := backendAccount()
			first, err := flow.AuthenticatePassword(context.Background(), account.AppleId, account.Password)
			if err != nil || first.Verification == nil || first.Identity != nil {
				t.Fatal("first SRP did not yield 2FA", err)
			}
			if err := flow.RequestVerificationCode(context.Background(), first.Verification); err != nil {
				t.Fatal(err)
			}
			identity, err := flow.CompleteVerification(context.Background(), first.Verification, "123456", account.AppleId, account.Password)
			if err != nil {
				t.Fatal(err)
			}
			if len(fixture.Requests()) != 6 {
				t.Fatal("2FA did not force fresh SRP")
			}
			if _, err := flow.CompleteVerification(context.Background(), first.Verification, "123456", account.AppleId, account.Password); err == nil {
				t.Fatal("verification state reused")
			}
			session, err := flow.OpenStore(context.Background(), identity)
			if err != nil {
				t.Fatal(err)
			}
			if session.Client != flow.http || session.Account.PasswordToken != "synthetic-password-token" || !signer.closed {
				t.Fatal("store session lost shared client, token or signer cleanup")
			}
			if secrets, err := session.Secrets(); err != nil || secrets.PasswordToken == "" {
				t.Fatal("session has no credentials", err)
			}
			if len(fixture.Requests()) != 8 {
				t.Fatal("unexpected login exchange count")
			}
		})
	}
}

func TestFlowRejectsSplitSnapshotsBeforeAnyExchange(t *testing.T) {
	base := testDevice()
	for name, mutate := range map[string]func(*DeviceSnapshot){
		"guid":       func(d *DeviceSnapshot) { d.Guid = "bad" },
		"md":         func(d *DeviceSnapshot) { d.Cpd["X-Apple-I-MD"] = "other" },
		"time":       func(d *DeviceSnapshot) { d.Cpd["X-Apple-I-Client-Time"] = "2020-01-01T00:00:00Z" },
		"timezone":   func(d *DeviceSnapshot) { d.Cpd["X-Apple-I-TimeZone"] = "UTC" },
		"native-amd": func(d *DeviceSnapshot) { d.Native.Amd = "captured-value" },
	} {
		t.Run(name, func(t *testing.T) {
			device := base
			device.Cpd = cloneMap(base.Cpd)
			mutate(&device)
			if _, err := New(Options{
				Http: storekit.NewSessionHttpClient(), Context: ams.RequestContext{Actor: ams.Configurator, Storefront: "143517", Language: "en-RU"},
				Device: device, Amd: &ams.AmdController{Provider: testAmdProvider{}},
			}); err == nil {
				t.Fatal("split device snapshot accepted")
			}
		})
	}
}

func TestFlowStopsAtFailedStageAndBindsIntermediateState(t *testing.T) {
	first := testFlow(t)
	second := testFlow(t)
	pending := &Verification{owner: first, appleId: "person@example.invalid", session: grandslam.SessionData{Adsid: "a", Idms: "i"}}
	if err := second.RequestVerificationCode(context.Background(), pending); err == nil {
		t.Fatal("verification crossed flow boundary")
	}
	identity := &Identity{
		AppleId: "person@example.invalid", owner: first,
		session: grandslam.SessionData{
			Adsid: "a", DirectoryServicesId: "1",
			Tokens: map[string]grandslam.ServiceToken{"com.apple.gs.idms.pet": {Token: "pet"}},
		},
	}
	next := 0

	first.deps.fetchBag = func(context.Context, *http.Client, ams.RequestContext) (*ams.Bag, error) {
		return nil, errors.New("synthetic bag failure")
	}

	first.deps.newSigner = func(context.Context, sap.Config) (sap.ActionSigner, error) { next++; return nil, nil }

	if _, err := first.OpenStore(context.Background(), identity); err == nil || next != 0 {
		t.Fatal("flow continued after failed bag stage")
	}
}

func TestFlowUnprovisionedAmdCanReachBag(t *testing.T) {
	var hostUuid [16]byte
	copy(hostUuid[:], []byte("synthetic-host-1"))
	provider, err := adi.NewAmd(hostUuid)
	if err != nil {
		t.Fatal(err)
	}
	device := testDevice()
	flow, err := New(Options{
		Http:    storekit.NewSessionHttpClient(),
		Context: ams.RequestContext{Actor: ams.Configurator, Storefront: "143517", Language: "en-RU"},
		Device:  device, Amd: &ams.AmdController{Provider: provider},
	})
	if err != nil {
		t.Fatal(err)
	}
	network := 0
	bagError := errors.New("synthetic bag boundary")

	flow.deps.fetchBag = func(context.Context, *http.Client, ams.RequestContext) (*ams.Bag, error) {
		network++
		return nil, bagError
	}

	identity := &Identity{
		AppleId: "person@example.invalid", owner: flow,
		session: grandslam.SessionData{
			Adsid: "a", DirectoryServicesId: "1",
			Tokens: map[string]grandslam.ServiceToken{"com.apple.gs.idms.pet": {Token: "pet"}},
		},
	}
	if _, err := flow.OpenStore(context.Background(), identity); !errors.Is(err, bagError) || network != 1 {
		t.Fatalf("AMD capable of SP did not reach bag: calls=%d err=%v", network, err)
	}
}

func TestFlowRefreshesOtpWithoutChangingInstallation(t *testing.T) {
	flow := testFlow(t)
	snapshots := 0

	flow.refreshDevice = func() (DeviceSnapshot, error) {
		snapshots++
		device := testDevice()
		device.Native.Md = "fresh-md"
		device.Cpd["X-Apple-I-MD"] = "fresh-md"
		device.ClientTime = device.ClientTime.Add(time.Duration(snapshots) * time.Minute)
		device.Cpd["X-Apple-I-Client-Time"] = device.ClientTime.Format(time.RFC3339)
		return device, nil
	}

	calls := 0

	flow.deps.authenticate = func(_ context.Context, _ *http.Client, _ grandslam.Metadata, _ string, _ string, cpd map[string]any) (grandslam.SessionData, error) {
		calls++
		if cpd["X-Apple-I-MD"] != "fresh-md" {
			t.Fatal("stale OTP snapshot")
		}
		return grandslam.SessionData{Adsid: "synthetic", Idms: "synthetic", DirectoryServicesId: "123", Tokens: map[string]grandslam.ServiceToken{"com.apple.gs.idms.pet": {Token: "synthetic-pet"}}}, nil
	}

	if _, err := flow.AuthenticatePassword(context.Background(), "person@example.invalid", "password"); err != nil {
		t.Fatal(err)
	}
	if snapshots != 1 || calls != 1 {
		t.Fatal("snapshot not generated once for stage")
	}

	flow.refreshDevice = func() (DeviceSnapshot, error) { d := testDevice(); d.Guid = "FFFFFFFFFFFF"; return d, nil }

	if _, err := flow.AuthenticatePassword(context.Background(), "person@example.invalid", "password"); err == nil {
		t.Fatal("installation switched between stages")
	}
	if calls != 1 {
		t.Fatal("split installation reached network")
	}
}

func TestFlowRejectedCodeCanRetryButAcceptedCodeCannotReplay(t *testing.T) {
	flow, fixture := backendFlow(t, applebackemu.InMemory, backendLoginSteps(t, true)...)
	account := backendAccount()
	first, err := flow.AuthenticatePassword(context.Background(), account.AppleId, account.Password)
	if err != nil || first.Verification == nil {
		t.Fatal("missing verification", err)
	}
	if err := flow.RequestVerificationCode(context.Background(), first.Verification); err != nil {
		t.Fatal(err)
	}
	if _, err := flow.CompleteVerification(context.Background(), first.Verification, "111111", account.AppleId, account.Password); !errors.Is(err, grandslam.ErrVerificationCodeRejected) {
		t.Fatal(err)
	}
	if _, err := flow.CompleteVerification(context.Background(), first.Verification, "123456", account.AppleId, account.Password); err != nil {
		t.Fatal(err)
	}
	if _, err := flow.CompleteVerification(context.Background(), first.Verification, "123456", account.AppleId, account.Password); err == nil || len(fixture.Requests()) != 7 {
		t.Fatal("accepted code replayed")
	}
}
