package applekit

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/adi"
	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/configurator"
	"github.com/hyperion-cs/appleseum/applekit/internal/grandslam"
	"github.com/hyperion-cs/appleseum/applekit/internal/storekit"
)

type fakeLoginFlow struct {
	uuid       [16]byte
	amd        *adi.Amd
	http       *http.Client
	t          *testing.T
	calls      []string
	password   string
	requestErr error
	needsCode  bool
	authErr    error
	verifyErr  error
}

func (f *fakeLoginFlow) AuthenticatePassword(_ context.Context, email, password string) (configurator.Authentication, error) {
	f.calls = append(f.calls, "password")
	if email != "person@example.invalid" || password != f.password {
		f.t.Fatal("password stage changed credentials")
	}
	if f.authErr != nil {
		return configurator.Authentication{}, f.authErr
	}
	if f.needsCode {
		return configurator.Authentication{Verification: &configurator.Verification{}}, nil
	}
	return configurator.Authentication{Identity: &configurator.Identity{}}, nil
}

func (f *fakeLoginFlow) RequestVerificationCode(context.Context, *configurator.Verification) error {
	f.calls = append(f.calls, "request-code")
	return f.requestErr
}

func (f *fakeLoginFlow) CompleteVerification(_ context.Context, _ *configurator.Verification, code, email, password string) (*configurator.Identity, error) {
	f.calls = append(f.calls, "verify-code")
	if code != "123456" || email != "person@example.invalid" || password != "second-password" {
		f.t.Fatal("code continuation lost credentials or concatenated password and code")
	}
	if f.verifyErr != nil {
		return nil, f.verifyErr
	}
	return &configurator.Identity{}, nil
}

func (f *fakeLoginFlow) OpenStore(context.Context, *configurator.Identity) (*storekit.Session, error) {
	f.calls = append(f.calls, "native")
	jar := f.http
	if jar == nil {
		jar = storekit.NewSessionHttpClient()
	}
	credentials := ams.NativeCredentials{AppleId: "person@example.invalid", Guid: strings.ToUpper(hex.EncodeToString(f.uuid[:6])), DirectoryServicesId: "123"}
	bag := &ams.Bag{DownloadURL: "https://downloaddispatch.itunes.apple.com/WebObjects/DownloadDispatch.woa/wa/ent/download"}
	session, err := storekit.SessionFromNative(jar, credentials, ams.NativeSession{PasswordToken: "synthetic-token", DirectoryServicesId: "123", Storefront: "143517-2,34"}, bag)
	if err != nil {
		return nil, err
	}
	return session, session.ConfigureAmd(&ams.AmdController{Provider: f.amd, Bag: *bag}, ams.RequestContext{Actor: ams.Configurator, Storefront: "143517", Language: "en-us"})
}

func testAuthenticator(t *testing.T, flow *fakeLoginFlow) *Authenticator {
	t.Helper()
	a, err := NewAuthenticator(AuthenticatorOptions{StatePath: filepath.Join(t.TempDir(), "apple-device.json")})
	if err != nil {
		t.Fatal(err)
	}

	a.newFlow = func(_ context.Context, client *http.Client) (loginFlow, error) {
		flow.uuid = a.state.Uuid
		flow.http = client
		var err error
		flow.amd, err = adi.NewAmd(flow.uuid)
		a.amd = flow.amd
		return flow, err
	}

	return a
}

func TestAuthenticatorResumesSameFlowAndPersistsModernSession(t *testing.T) {
	flow := &fakeLoginFlow{t: t, password: "first-password", needsCode: true}
	a := testAuthenticator(t, flow)
	if _, err := a.SignIn(context.Background(), "person@example.invalid", "first-password", ""); !errors.Is(err, ErrTwoFactorRequired) {
		t.Fatal(err)
	}
	if a.pending == nil || !a.VerificationPending() {
		t.Fatal("verified 2FA continuation not retained")
	}
	client, err := a.SignIn(context.Background(), "person@example.invalid", "second-password", "123456")
	if err != nil {
		t.Fatal(err)
	}
	if a.VerificationPending() || a.pending != nil || !reflect.DeepEqual(flow.calls, []string{"password", "request-code", "verify-code", "native"}) {
		t.Fatal(flow.calls)
	}
	secrets, err := client.SessionSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if len(a.state.Amd) == 0 {
		t.Fatal("modern AMD context lost")
	}
	restored, err := a.OpenSession(client.Account(), client.OwnerId(), client.SessionParameters(), secrets, client.HttpClient())
	if err != nil || restored.OwnerId() != "123" {
		t.Fatal(err)
	}
	state, err := os.ReadFile(a.options.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"first-password", "second-password", "123456", "synthetic-token"} {
		if strings.Contains(string(state), secret) {
			t.Fatal("login secret persisted in device state")
		}
	}
}

func TestAuthenticatorRejectsUnboundExpiredAndReplayedCode(t *testing.T) {
	for _, mode := range []string{"missing", "wrong-account", "expired", "replayed"} {
		t.Run(mode, func(t *testing.T) {
			flow := &fakeLoginFlow{t: t, password: "first-password", needsCode: true}
			a := testAuthenticator(t, flow)
			at := time.Date(2_026, 11, 2, 15, 0, 0, 0, time.UTC)

			a.now = func() time.Time { return at }

			if mode != "missing" {
				if _, err := a.SignIn(context.Background(), "person@example.invalid", "first-password", ""); !errors.Is(err, ErrTwoFactorRequired) {
					t.Fatal(err)
				}
			}
			email := "person@example.invalid"
			if mode == "wrong-account" {
				email = "other@example.invalid"
			}
			if mode == "expired" {
				at = at.Add(11 * time.Minute)
			}
			if mode == "replayed" {
				if _, err := a.SignIn(context.Background(), email, "second-password", "123456"); err != nil {
					t.Fatal(err)
				}
			}
			before := len(flow.calls)
			if _, err := a.SignIn(context.Background(), email, "second-password", "123456"); !errors.Is(err, ErrVerificationExpired) {
				t.Fatal(err)
			}
			if len(flow.calls) != before || a.pending != nil {
				t.Fatal("unbound code sent to Apple")
			}
		})
	}
}

func TestAuthenticatorReturns2FaOnlyAfterSuccessfulRequest(t *testing.T) {
	cause := errors.New("synthetic code delivery rejection")
	flow := &fakeLoginFlow{t: t, password: "password", needsCode: true, requestErr: cause}
	a := testAuthenticator(t, flow)
	_, err := a.SignIn(context.Background(), "person@example.invalid", "password", "")
	if !errors.Is(err, cause) || errors.Is(err, ErrTwoFactorRequired) || a.pending != nil {
		t.Fatal(err)
	}
	var diagnostic *LoginError
	if !errors.As(err, &diagnostic) || diagnostic.Stage != "verification_request" {
		t.Fatal(err)
	}
}

func TestAuthenticatorCancellationDoesNotCreateDevice(t *testing.T) {
	flow := &fakeLoginFlow{t: t}
	a := testAuthenticator(t, flow)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.SignIn(ctx, "person@example.invalid", "password", ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(flow.calls) != 0 {
		t.Fatal("canceled login reached flow")
	}
	if _, err := os.Stat(a.options.StatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled login created state")
	}
}

func TestInstallationIdentitySurvivesRestartAndRejectsCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device.json")
	first, err := loadInstallation(fileDeviceStore(path), DefaultDeviceSettings())
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadInstallation(fileDeviceStore(path), DefaultDeviceSettings())
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("installation changed on restart", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("device state is not private", err)
	}
	if err = os.WriteFile(path, []byte(`{"Version":99}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadInstallation(fileDeviceStore(path), DefaultDeviceSettings()); err == nil {
		t.Fatal("corrupt state silently replaced")
	}
}

func TestDeviceChangesDiscardVerificationOnlyAfterSaving(t *testing.T) {
	for _, reset := range []bool{false, true} {
		flow := &fakeLoginFlow{t: t, password: "first-password", needsCode: true}
		a := testAuthenticator(t, flow)
		if _, err := a.SignIn(context.Background(), "person@example.invalid", "first-password", ""); !errors.Is(err, ErrTwoFactorRequired) {
			t.Fatal(err)
		}
		flow.verifyErr = &configurator.StageError{Stage: "verification", Cause: &grandslam.StatusError{Status: grandslam.Status{Code: -21_669}}}
		if _, err := a.SignIn(context.Background(), "person@example.invalid", "second-password", "123456"); !errors.Is(err, ErrVerificationCodeRejected) || !a.VerificationPending() {
			t.Fatal("rejected code did not preserve the pending flow", err)
		}
		settings := DefaultDeviceSettings()
		invalid := settings
		invalid.TimeZone = "Invalid/Zone"
		if _, err := a.SetDeviceSettings(invalid); err == nil || !a.VerificationPending() {
			t.Fatal("invalid settings discarded verification")
		}
		store := a.options.Store
		a.options.Store = fileDeviceStore(t.TempDir())
		if _, err := a.SetDeviceSettings(settings); err == nil || !a.VerificationPending() {
			t.Fatal("failed save discarded verification")
		}
		a.options.Store = store
		var err error
		if reset {
			_, err = a.ResetDevice()
		} else {
			settings.TimeZone = "Etc/GMT+1"
			_, err = a.SetDeviceSettings(settings)
		}
		if err != nil || a.VerificationPending() {
			t.Fatal("device update retained verification", err)
		}
		if _, err := a.SignIn(context.Background(), "person@example.invalid", "second-password", "123456"); !errors.Is(err, ErrVerificationExpired) {
			t.Fatal("old code continued after changing device settings", err)
		}
		if !reflect.DeepEqual(flow.calls, []string{"password", "request-code", "verify-code"}) {
			t.Fatal("changing device settings sent an authentication request", flow.calls)
		}
	}
}

func TestAuthenticatorRejectsPasswordWithoutRequesting2Fa(t *testing.T) {
	cause := &grandslam.StatusError{HttpStatus: 200, Status: grandslam.Status{Code: -22_406}}
	flow := &fakeLoginFlow{t: t, password: "wrong-password", authErr: &configurator.StageError{Stage: "grandslam", Cause: cause}}
	a := testAuthenticator(t, flow)
	_, err := a.SignIn(context.Background(), "person@example.invalid", "wrong-password", "")
	if !errors.Is(err, ErrPasswordRejected) || !errors.Is(err, ErrCredentialsRejected) || errors.Is(err, ErrTwoFactorRequired) || a.pending != nil || !reflect.DeepEqual(flow.calls, []string{"password"}) {
		t.Fatal("bad credentials prompted for 2FA", err, flow.calls)
	}
	var diagnostic *LoginError
	if !errors.As(err, &diagnostic) || diagnostic.Reason != "password_rejected" {
		t.Fatal(err)
	}
}

func TestAuthenticatorRejectedCodeRetriesWithoutNewPasswordExchange(t *testing.T) {
	cause := &grandslam.StatusError{HttpStatus: 200, Status: grandslam.Status{Code: -21_669}}
	flow := &fakeLoginFlow{t: t, password: "first-password", needsCode: true, verifyErr: &configurator.StageError{Stage: "verification", Cause: cause}}
	a := testAuthenticator(t, flow)
	if _, err := a.SignIn(context.Background(), "person@example.invalid", "first-password", ""); !errors.Is(err, ErrTwoFactorRequired) {
		t.Fatal(err)
	}
	if _, err := a.SignIn(context.Background(), "person@example.invalid", "second-password", "123456"); !errors.Is(err, ErrVerificationCodeRejected) {
		t.Fatal(err)
	}
	if a.pending == nil {
		t.Fatal("rejected code discarded verified password stage")
	}
	flow.verifyErr = nil
	if _, err := a.SignIn(context.Background(), "person@example.invalid", "second-password", "123456"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(flow.calls, []string{"password", "request-code", "verify-code", "verify-code", "native"}) {
		t.Fatal("code retry resent password or requested another code", flow.calls)
	}
}

func TestDefaultStatePathPreservesLegacyIdentity(t *testing.T) {
	for _, scenario := range []string{"fresh", "legacy", "existing", "invalid", "blocked"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "applekit", "apple-device.json")
			legacy := filepath.Join(dir, "ipa_offline", "apple-device.json")
			state, err := newInstallation()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "fresh" {
				if err := fileDeviceStore(legacy).saveRaw(raw); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "existing":
				state.Uuid[0] ^= 1
				other, _ := json.Marshal(state)
				if err := fileDeviceStore(path).saveRaw(other); err != nil {
					t.Fatal(err)
				}
				raw = other
			case "invalid":
				if err := os.WriteFile(legacy, []byte("invalid"), 0600); err != nil {
					t.Fatal(err)
				}
			case "blocked":
				if err := os.WriteFile(filepath.Dir(path), []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := defaultStatePath(dir)
			if scenario == "invalid" || scenario == "blocked" {
				if err == nil {
					t.Fatal("invalid or unwritable state was accepted")
				}
				if _, err := os.Stat(legacy); err != nil {
					t.Fatal("original identity was removed on failure", err)
				}
				return
			}
			if err != nil || got != path {
				t.Fatal(got, err)
			}
			if scenario == "fresh" {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("path lookup created an identity", err)
				}
				return
			}
			saved, err := os.ReadFile(path)
			if err != nil || string(saved) != string(raw) {
				t.Fatal("stored identity changed during migration", err)
			}
			if scenario == "legacy" {
				if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("legacy record remains after successful migration", err)
				}
			}
		})
	}
}
