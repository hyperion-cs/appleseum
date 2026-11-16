package applekit

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/adi"
	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/configurator"
	"github.com/hyperion-cs/appleseum/applekit/internal/grandslam"
	"github.com/hyperion-cs/appleseum/applekit/internal/storekit"
)

var ErrCredentialsRejected = grandslam.ErrCredentialsRejected
var ErrPasswordRejected = grandslam.ErrPasswordRejected
var ErrVerificationCodeRejected = grandslam.ErrVerificationCodeRejected

var ErrVerificationExpired = errors.New("verification must be started again")

type loginFlow interface {
	AuthenticatePassword(context.Context, string, string) (configurator.Authentication, error)
	RequestVerificationCode(context.Context, *configurator.Verification) error
	CompleteVerification(context.Context, *configurator.Verification, string, string, string) (*configurator.Identity, error)
	OpenStore(context.Context, *configurator.Identity) (*storekit.Session, error)
}
type pendingLogin struct {
	flow         loginFlow
	verification *configurator.Verification
	appleId      string
	expires      time.Time
}

// Authenticator retains the installation and pending 2FA challenge, never passwords or codes.
// Reuse it across SignIn calls.
type Authenticator struct {
	mu        sync.Mutex
	options   AuthenticatorOptions
	state     *installation
	pending   *pendingLogin
	amd       *adi.Amd
	now       func() time.Time
	newFlow   func(context.Context, *http.Client) (loginFlow, error)
	transport http.RoundTripper
}

func NewAuthenticator(options AuthenticatorOptions) (*Authenticator, error) {
	if options.Store != nil && options.StatePath != "" {
		return nil, errors.New("choose one Apple installation store")
	}
	if options.Store == nil {
		if options.StatePath == "" {
			return nil, errors.New("Apple installation store required")
		}
		options.Store = fileDeviceStore(options.StatePath)
	}
	defaults := DefaultDeviceSettings()
	options.Language = cmp.Or(options.Language, defaults.Language)
	options.Locale = cmp.Or(options.Locale, defaults.Locale)
	options.TimeZone = cmp.Or(options.TimeZone, defaults.TimeZone)
	options.ComplianceId = cmp.Or(options.ComplianceId, defaults.ComplianceId)
	if err := options.settings().validate(); err != nil {
		return nil, err
	}
	options.Storefront = cmp.Or(options.Storefront, DefaultStorefront)
	if err := (ams.RequestContext{Actor: ams.Configurator, Storefront: options.Storefront, Language: options.Language}).Validate(); err != nil {
		return nil, err
	}
	a := &Authenticator{options: options, now: time.Now, transport: options.Transport}
	a.newFlow = a.prepareFlow
	return a, nil
}

func (a *Authenticator) prepareFlow(ctx context.Context, client *http.Client) (loginFlow, error) {
	if err := provisionIdms(ctx, client, a.state, a.options, a.now().UTC()); err != nil {
		return nil, storekit.NewLoginError("idms_provisioning", err)
	}
	if err := saveInstallation(a.options.Store, a.state); err != nil {
		return nil, storekit.NewLoginError("device_storage", err)
	}
	var err error
	if a.amd, err = a.state.amdProvider(); err != nil {
		return nil, err
	}

	snapshot := func() (configurator.DeviceSnapshot, error) {
		return a.state.snapshot(a.options, a.now().UTC().Truncate(time.Second))
	}

	device, err := snapshot()
	if err != nil {
		return nil, err
	}
	storefront := cmp.Or(a.state.Storefront, a.options.Storefront)
	return configurator.New(configurator.Options{Http: client, Context: a.options.requestContext(storefront, a.now()), Device: device, Amd: &ams.AmdController{Provider: a.amd}, RefreshDevice: snapshot,
		SynchronizeDevice: func(ctx context.Context, status *grandslam.StatusError) error {
			if err := synchronizeIdms(ctx, client, a.state, a.options, status, a.now().UTC()); err != nil {
				return err
			}
			return saveInstallation(a.options.Store, a.state)
		},
	})
}

// An empty code starts sign-in; a code resumes this authenticator's pending 2FA.
func (a *Authenticator) SignIn(ctx context.Context, appleId, password, code string) (client *Client, err error) {
	if a == nil {
		return nil, errors.New("nil Apple authenticator")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	stage := "device"
	defer func() {
		if err != nil && !errors.Is(err, ErrTwoFactorRequired) {
			var diagnostic *storekit.LoginError
			if !errors.As(err, &diagnostic) {
				var flowError *configurator.StageError
				if errors.As(err, &flowError) {
					stage = flowError.Stage
				}
				err = storekit.NewLoginError(stage, err)
			}
		}
	}()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	appleId = strings.TrimSpace(appleId)
	code = strings.TrimSpace(code)
	if appleId == "" || password == "" {
		return nil, errors.New("Apple ID and password required")
	}
	if code != "" && (len(code) != 6 || strings.Trim(code, "0123456789") != "") {
		return nil, errors.New("invalid verification code")
	}
	var flow loginFlow
	var identity *configurator.Identity
	if code != "" {
		stage = "verification"
		pending := a.pending
		a.pending = nil
		if pending == nil || pending.appleId != appleId || !a.now().Before(pending.expires) {
			return nil, ErrVerificationExpired
		}
		flow = pending.flow
		identity, err = flow.CompleteVerification(ctx, pending.verification, code, appleId, password)
		if err != nil {
			var step *configurator.StageError
			if errors.As(err, &step) && step.Stage == "verification" && errors.Is(err, ErrVerificationCodeRejected) && a.now().Before(pending.expires) {
				a.pending = pending
			}
			return nil, err
		}
	} else {
		a.pending = nil
		if err = a.loadState(); err != nil {
			return nil, err
		}
		client := storekit.NewSessionHttpClientWithTransport(a.transport)
		a.transport = client.Transport
		flow, err = a.newFlow(ctx, client)
		if err != nil {
			return nil, err
		}
		stage = "grandslam"
		result, authErr := flow.AuthenticatePassword(ctx, appleId, password)
		if authErr != nil {
			return nil, authErr
		}
		if result.Identity == nil {
			if result.Verification == nil {
				return nil, errors.New("GrandSlam returned no identity or verification")
			}
			stage = "verification_request"
			if err = flow.RequestVerificationCode(ctx, result.Verification); err != nil {
				return nil, err
			}
			a.pending = &pendingLogin{flow: flow, verification: result.Verification, appleId: appleId, expires: a.now().Add(10 * time.Minute)}
			return nil, ErrTwoFactorRequired
		}
		identity = result.Identity
	}
	stage = "store"
	// Save committed AMD even if a subsequent server-directed step fails.
	defer func() {
		if a.amd != nil {
			encoded, saveErr := a.amd.MarshalBinary()
			if saveErr == nil {
				a.state.Amd = encoded
				saveErr = saveInstallation(a.options.Store, a.state)
			}
			if saveErr != nil {
				client = nil
				err = errors.Join(err, storekit.NewLoginError("device_storage", saveErr))
			}
		}
	}()
	session, err := flow.OpenStore(ctx, identity)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, errors.New("native returned no store session")
	}
	a.state.Storefront = session.Account.StoreFront
	return &Client{s: session}, nil
}

func (a *Authenticator) VerificationPending() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pending != nil && a.now().Before(a.pending.expires)
}

func (a *Authenticator) CancelVerification() {
	if a != nil {
		a.mu.Lock()
		a.pending = nil
		a.mu.Unlock()
	}
}

var defaultLogin struct {
	sync.Once
	auth *Authenticator
	err  error
}

func defaultAuthenticator() (*Authenticator, error) {
	defaultLogin.Do(func() {
		dir, err := os.UserConfigDir()
		if err != nil {
			defaultLogin.err = err
			return
		}
		path, err := defaultStatePath(dir)
		if err != nil {
			defaultLogin.err = err
			return
		}
		defaultLogin.auth, defaultLogin.err = NewAuthenticator(AuthenticatorOptions{StatePath: path})
	})
	return defaultLogin.auth, defaultLogin.err
}
