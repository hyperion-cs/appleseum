package configurator

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/grandslam"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
	"github.com/hyperion-cs/appleseum/applekit/internal/sap"
	"github.com/hyperion-cs/appleseum/applekit/internal/storekit"
)

// New copies CPD so caller mutation cannot split GrandSlam and native identities.
type DeviceSnapshot struct {
	Cpd          map[string]any
	Native       ams.NativeDevice
	Guid         string
	ClientInfo   string
	Language     string
	Locale       string
	TimeZone     string
	ComplianceId string
	ClientTime   time.Time
}

type Options struct {
	Http    *http.Client
	Context ams.RequestContext
	Device  DeviceSnapshot
	Amd     *ams.AmdController
	// RefreshDevice generates fresh IdMS headers for each protocol stage.
	RefreshDevice     func() (DeviceSnapshot, error)
	SynchronizeDevice func(context.Context, *grandslam.StatusError) error
}

type Flow struct {
	http              *http.Client
	context           ams.RequestContext
	device            DeviceSnapshot
	amd               *ams.AmdController
	deps              dependencies
	refreshDevice     func() (DeviceSnapshot, error)
	synchronizeDevice func(context.Context, *grandslam.StatusError) error
}

// A verified PET identity is bound to its originating Flow.
type Identity struct {
	AppleId string
	owner   *Flow
	session grandslam.SessionData
}

// A pending 2FA identity is bound to its originating Flow.
type Verification struct {
	mu      sync.Mutex
	used    bool
	owner   *Flow
	appleId string
	session grandslam.SessionData
}

// Authentication contains exactly one of Identity or Verification.
type Authentication struct {
	Identity     *Identity
	Verification *Verification
}

type dependencies struct {
	authenticate func(context.Context, *http.Client, grandslam.Metadata, string, string, map[string]any) (grandslam.SessionData, error)
	requestCode  func(context.Context, *http.Client, grandslam.Metadata, grandslam.SessionData, map[string]any) error
	verifyCode   func(context.Context, *http.Client, grandslam.Metadata, grandslam.SessionData, map[string]any, string) error
	fetchBag     func(context.Context, *http.Client, ams.RequestContext) (*ams.Bag, error)
	newSigner    func(context.Context, sap.Config) (sap.ActionSigner, error)
	native       func(context.Context, *http.Client, string, ams.ActionSigner, ams.NativeCredentials, ams.NativeDevice, ams.NativeOptions) (ams.NativeSession, error)
	bridge       func(*http.Client, ams.NativeCredentials, ams.NativeSession, *ams.Bag) (*storekit.Session, error)
}

type StageError struct {
	Stage string
	Cause error
}

func (e *StageError) Error() string { return "configurator " + e.Stage + ": " + e.Cause.Error() }

func (e *StageError) Unwrap() error { return e.Cause }

func stageError(stage string, err error) error { return &StageError{Stage: stage, Cause: err} }

func productionDependencies() dependencies {
	return dependencies{
		authenticate: func(ctx context.Context, client *http.Client, metadata grandslam.Metadata, username, password string, cpd map[string]any) (grandslam.SessionData, error) {
			return (grandslam.Service{Http: client, Metadata: metadata}).Authenticate(ctx, username, password, cpd)
		},
		requestCode: func(ctx context.Context, client *http.Client, metadata grandslam.Metadata, session grandslam.SessionData, cpd map[string]any) error {
			return (grandslam.Service{Http: client, Metadata: metadata}).RequestTrustedDeviceCode(ctx, session, cpd)
		},
		verifyCode: func(ctx context.Context, client *http.Client, metadata grandslam.Metadata, session grandslam.SessionData, cpd map[string]any, code string) error {
			return (grandslam.Service{Http: client, Metadata: metadata}).VerifyTrustedDeviceCode(ctx, session, cpd, code)
		},
		fetchBag:  ams.FetchBag,
		newSigner: sap.NewPureSigner,
		native:    ams.AuthenticateNative,
		bridge:    storekit.SessionFromNative,
	}
}

func New(options Options) (*Flow, error) {
	if options.Http == nil || options.Http.Jar == nil {
		return nil, errors.New("configurator: shared cookie-bearing HTTP client required")
	}
	if options.Amd == nil || options.Amd.Provider == nil {
		return nil, errors.New("configurator: AMD provider required")
	}
	if err := options.Context.Validate(); err != nil || options.Context.Actor != ams.Configurator {
		return nil, errors.New("configurator: invalid request context")
	}
	device, err := copyAndValidateDevice(options.Device)
	if err != nil {
		return nil, err
	}
	return &Flow{
		http: options.Http, context: options.Context, device: device,
		amd: &ams.AmdController{Provider: options.Amd.Provider}, deps: productionDependencies(), refreshDevice: options.RefreshDevice, synchronizeDevice: options.SynchronizeDevice,
	}, nil
}

func (f *Flow) refresh() error {
	if f.refreshDevice == nil {
		return nil
	}
	next, err := f.refreshDevice()
	if err != nil {
		return err
	}
	next, err = copyAndValidateDevice(next)
	if err != nil {
		return err
	}
	if next.Guid != f.device.Guid || next.Native.LocalUser != f.device.Native.LocalUser || next.Cpd["X-Mme-Device-Id"] != f.device.Cpd["X-Mme-Device-Id"] {
		return errors.New("configurator: refreshed device changed installation identity")
	}
	f.device = next
	return nil
}

func (f *Flow) metadata() grandslam.Metadata {
	return grandslam.Metadata{ClientInfo: f.device.ClientInfo, Language: f.device.Language}
}

// AuthenticatePassword returns a PET identity or a pending 2FA verification.
func (f *Flow) AuthenticatePassword(ctx context.Context, appleId, password string) (Authentication, error) {
	if f == nil || f.deps.authenticate == nil {
		return Authentication{}, errors.New("configurator: nil flow")
	}
	if err := f.refresh(); err != nil {
		return Authentication{}, stageError("device", err)
	}
	session, err := f.deps.authenticate(ctx, f.http, f.metadata(), appleId, password, cloneMap(f.device.Cpd))
	var challenge *grandslam.StatusError
	if err != nil && f.synchronizeDevice != nil && errors.As(err, &challenge) && challenge.Code == -22_421 && challenge.ServerStatus == 434 && challenge.DeviceTarget == "native" && challenge.DeviceData != "" {
		if err := f.synchronizeDevice(ctx, challenge); err != nil {
			return Authentication{}, stageError("idms_sync", err)
		}
		if err := f.refresh(); err != nil {
			return Authentication{}, stageError("device", err)
		}
		session, err = f.deps.authenticate(ctx, f.http, f.metadata(), appleId, password, cloneMap(f.device.Cpd))
	}
	if err != nil {
		return Authentication{}, stageError("grandslam", err)
	}
	if _, err := session.PasswordEquivalentToken(); err != nil {
		if session.Adsid == "" || session.Idms == "" {
			return Authentication{}, errors.New("configurator: GrandSlam returned no PET or verified 2FA identity")
		}
		return Authentication{Verification: &Verification{owner: f, appleId: appleId, session: session}}, nil
	}
	if session.DirectoryServicesId == "" {
		return Authentication{}, errors.New("configurator: PET session has no directory identifier")
	}
	return Authentication{Identity: &Identity{AppleId: appleId, owner: f, session: session}}, nil
}

func (f *Flow) RequestVerificationCode(ctx context.Context, pending *Verification) error {
	if f == nil || pending == nil || pending.owner != f {
		return errors.New("configurator: verification belongs to another flow")
	}
	pending.mu.Lock()
	defer pending.mu.Unlock()
	if pending.used {
		return errors.New("configurator: verification is already consumed")
	}
	if err := f.refresh(); err != nil {
		return stageError("device", err)
	}
	if err := f.deps.requestCode(ctx, f.http, f.metadata(), pending.session, cloneMap(f.device.Cpd)); err != nil {
		return stageError("verification_request", err)
	}
	return nil
}

// After verifying 2FA, repeat SRP to obtain the PET.
func (f *Flow) CompleteVerification(ctx context.Context, pending *Verification, code, appleId, password string) (*Identity, error) {
	if f == nil || pending == nil || pending.owner != f {
		return nil, errors.New("configurator: verification belongs to another flow")
	}
	pending.mu.Lock()
	defer pending.mu.Unlock()
	if pending.used {
		return nil, errors.New("configurator: verification is already consumed")
	}
	pending.used = true
	if appleId != pending.appleId {
		return nil, errors.New("configurator: verification Apple ID mismatch")
	}
	if err := f.refresh(); err != nil {
		return nil, stageError("device", err)
	}
	if err := f.deps.verifyCode(ctx, f.http, f.metadata(), pending.session, cloneMap(f.device.Cpd), code); err != nil {
		if errors.Is(err, grandslam.ErrVerificationCodeRejected) {
			pending.used = false
		}
		return nil, stageError("verification", err)
	}
	result, err := f.AuthenticatePassword(ctx, appleId, password)
	if err != nil {
		return nil, err
	}
	if result.Identity == nil {
		return nil, errors.New("configurator: verification succeeded but fresh SRP returned no PET")
	}
	return result.Identity, nil
}

func (f *Flow) OpenStore(ctx context.Context, identity *Identity) (*storekit.Session, error) {
	if f == nil || identity == nil || identity.owner != f {
		return nil, errors.New("configurator: identity belongs to another flow")
	}
	if err := f.refresh(); err != nil {
		return nil, stageError("device", err)
	}
	ctx = httpx.WithPlatform(ctx, f.context.Platform)
	pet, err := identity.session.PasswordEquivalentToken()
	if err != nil {
		return nil, err
	}
	bag, err := f.deps.fetchBag(ctx, f.http, f.context)
	if err != nil {
		return nil, stageError("bag", err)
	}
	if bag == nil {
		return nil, errors.New("configurator: bag is nil")
	}
	amd := &ams.AmdController{Bag: *bag, Provider: f.amd.Provider}
	hardwareId, err := hex.DecodeString(f.device.Guid)
	if err != nil || len(hardwareId) != 6 {
		return nil, errors.New("configurator: invalid hardware identifier")
	}
	sapContext := f.context
	sapContext.Actor = ams.Accounts
	headers, _ := sapContext.Headers()
	signer, err := f.deps.newSigner(httpx.WithProfile(ctx, httpx.Accounts), sap.Config{
		SetupUrl: bag.SAPSetupURL, CertificateUrl: bag.SAPCertificateURL,
		Version: sap.ConfiguratorProtocolVersion, HardwareId: hardwareId, RequestHeaders: headers, HttpClient: f.http,
	})
	if err != nil {
		return nil, stageError("sap", err)
	}
	if signer == nil {
		return nil, errors.New("configurator: SAP returned no signer")
	}
	defer signer.Close()
	credentials := ams.NativeCredentials{
		AppleId: identity.AppleId, PasswordEquivalent: pet, Adsid: identity.session.Adsid,
		DirectoryServicesId: identity.session.DirectoryServicesId, Guid: f.device.Guid,
	}
	native, err := f.deps.native(ctx, f.http, bag.AuthenticateURL, signer, credentials, f.device.Native, ams.NativeOptions{
		Context: f.context, Locale: f.device.Locale, TimeZone: f.device.TimeZone,
		ComplianceId: f.device.ComplianceId, ClientTime: f.device.ClientTime, Amd: amd,
	})
	if err != nil {
		return nil, stageError("native", err)
	}
	session, err := f.deps.bridge(f.http, credentials, native, bag)
	if err != nil {
		return nil, stageError("store", err)
	}
	if session == nil {
		return nil, errors.New("configurator: bridge returned no session")
	}
	if err := session.ConfigureAmd(amd, f.context); err != nil {
		return nil, stageError("amd", err)
	}
	return session, nil
}

func copyAndValidateDevice(input DeviceSnapshot) (DeviceSnapshot, error) {
	input.Guid = strings.ToUpper(input.Guid)
	if raw, err := hex.DecodeString(input.Guid); err != nil || len(raw) != 6 {
		return DeviceSnapshot{}, errors.New("configurator: device GUID must be 12 hexadecimal characters")
	}
	if input.ClientTime.IsZero() || input.ClientInfo == "" || input.Language == "" || input.Locale == "" || input.TimeZone == "" || len(input.ComplianceId) != 2 {
		return DeviceSnapshot{}, errors.New("configurator: incomplete device metadata")
	}
	for _, c := range input.ComplianceId {
		if c < 'A' || c > 'Z' {
			return DeviceSnapshot{}, errors.New("configurator: invalid compliance identifier")
		}
	}
	want := map[string]string{
		"X-Apple-I-MD": input.Native.Md, "X-Apple-I-MD-M": input.Native.MachineId,
		"X-Apple-I-MD-LU": input.Native.LocalUser, "X-Apple-I-MD-RINFO": input.Native.Routing,
		"X-Apple-I-Client-Time": input.ClientTime.UTC().Format(time.RFC3339),
	}
	for key, value := range want {
		if value == "" || input.Cpd[key] != value {
			return DeviceSnapshot{}, fmt.Errorf("configurator: CPD and native snapshot disagree at %s", key)
		}
	}
	for key, value := range map[string]string{"X-Apple-Locale": input.Locale, "X-Apple-I-TimeZone": input.TimeZone} {
		if current, present := input.Cpd[key]; present && current != value {
			return DeviceSnapshot{}, fmt.Errorf("configurator: CPD and native snapshot disagree at %s", key)
		}
	}
	if input.Native.Amd != "" || input.Native.AmdMachineId != "" {
		return DeviceSnapshot{}, errors.New("configurator: AMD fields must come from the AMD controller")
	}
	input.Cpd = cloneMap(input.Cpd)
	return input, nil
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = cloneValue(value)
	}
	return out
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = cloneValue(item)
		}
		return out
	case []byte:
		return bytes.Clone(typed)
	case []string:
		return slices.Clone(typed)
	case map[string]string:
		return maps.Clone(typed)
	default:
		return value
	}
}
