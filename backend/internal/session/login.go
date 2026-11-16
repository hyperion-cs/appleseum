package session

import (
	"context"
	"errors"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/account"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

// LoginFailed carries Apple's diagnostic beside the code the client receives.
type LoginFailed struct {
	Code   fault.Code
	Detail string
}

func (e *LoginFailed) Error() string { return e.Detail }

func (e *LoginFailed) Unwrap() error { return e.Code }

// Login signs the workspace in: the service account by its startup password,
// an Apple account through Apple. It returns the account, or nil for the service.
func (w *Workspace) Login(ctx context.Context, input wire.LoginRequest) (*wire.Account, error) {
	input.Email = account.NormalizeLogin(input.Email)
	if w.r.cfg.Public && input.Email == ServiceLogin {
		return nil, w.loginService(ctx, input.Password)
	}
	w.mu.Lock()
	switch {
	case w.service:
		w.mu.Unlock()
		return nil, fault.Busy
	case len(w.key) != account.KeySize:
		w.mu.Unlock()
		return nil, fault.SessionKeyRequired
	case w.identity != nil && !w.login.required || w.login.busy:
		w.mu.Unlock()
		return nil, fault.Busy
	}
	w.login.busy = true
	w.mu.Unlock()
	defer func() { w.mu.Lock(); w.login.busy = false; w.mu.Unlock() }()
	if err := w.apple.Lock(ctx); err != nil {
		return nil, err
	}
	defer w.apple.Unlock()

	// A saved account that only needs its verification code signs in with the remembered password.
	w.mu.Lock()
	verification := w.login.verification && input.Password == "" && input.Code != "" && w.identity != nil
	if verification {
		input.Email = w.identity.Account.Email
	}
	w.mu.Unlock()
	if verification {
		var err error
		if input.Password, err = w.rememberedPassword(ctx); err != nil {
			return nil, err
		}
		input.Remember = true
	}
	if input.Email == "" || input.Password == "" {
		return nil, fault.InvalidCredentials
	}
	if !w.r.cfg.AllowLogin(input.Email) {
		return nil, fault.RateLimited
	}
	ctx, cancel := context.WithTimeout(w.r.cfg.Trace(ctx), signInTimeout)
	defer cancel()
	defer context.AfterFunc(w.ctx, cancel)()
	err := w.authenticate(ctx, input, "")
	if err == nil {
		summary := AccountSummary(*w.currentIdentity())
		return &summary, nil
	}
	code := account.LoginFailure(err)
	if code == fault.TwoFactorRequired {
		return nil, code
	}
	w.mu.Lock()
	if verification && !errors.Is(err, applekit.ErrVerificationCodeRejected) {
		w.login.verification = false
	}
	if w.login.required {
		w.login.err = code
	}
	w.mu.Unlock()
	var diagnostic *applekit.LoginError
	if errors.As(err, &diagnostic) {
		return nil, &LoginFailed{Code: code, Detail: diagnostic.Error()}
	}
	return nil, code
}

func (w *Workspace) currentIdentity() *db.Identity {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.identity
}

func (w *Workspace) loginService(ctx context.Context, password string) error {
	if !w.r.cfg.AllowLogin(ServiceLogin) {
		return fault.RateLimited
	}
	if !w.r.serviceLogin(password) {
		return fault.PasswordRejected
	}
	w.mu.Lock()
	busy := w.identity != nil || w.login.busy
	w.mu.Unlock()
	if busy {
		return fault.Busy
	}
	if err := w.r.db.LoginService(ctx, w.Id); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.auth.CancelVerification()
	w.service = true
	return nil
}

// authenticate signs in on the device that belongs to the login and installs
// the session. owner is set for an automatic renewal, which must return the same account.
func (w *Workspace) authenticate(ctx context.Context, input wire.LoginRequest, owner string) error {
	if input.Code == "" {
		if err := w.selectDevice(ctx, input.Email); err != nil {
			return err
		}
	}
	w.mu.Lock()
	deviceId := w.device.Id()
	w.mu.Unlock()
	device := w.r.deviceLock(deviceId)
	if err := device.Lock(ctx); err != nil {
		return err
	}
	defer device.Unlock()
	w.mu.Lock()
	var err error
	// A code continues the challenge the authenticator already holds.
	if input.Code == "" {
		err = w.renewAuthenticator()
	}
	auth := w.auth
	w.mu.Unlock()
	if err != nil {
		return err
	}
	session, err := w.r.cfg.Apple.SignIn(ctx, auth, input.Email, input.Password, input.Code)
	if err != nil {
		return err
	}
	if session == nil || owner != "" && session.OwnerId() != owner {
		return errors.New("unexpected Apple account")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return w.install(ctx, session, input, owner != "")
}

// selectDevice picks the device a login last signed in with, so Apple sees a
// known machine. The alias only selects a device; it authenticates nobody.
func (w *Workspace) selectDevice(ctx context.Context, login string) error {
	w.mu.Lock()
	current, signedOut := w.device.Id(), w.identity == nil
	w.mu.Unlock()
	known, err := w.r.db.FindLoginDevice(ctx, login)
	switch {
	case err != nil:
	case known != "" && known != current:
		w.mu.Lock()
		w.device.SetId(known)
		err = w.renewAuthenticator()
		w.mu.Unlock()
	case known == "" && w.r.cfg.Public && signedOut && current != "":
		// An unknown login must not inherit the device of another account.
		var taken bool
		if taken, err = w.r.db.DeviceKnown(ctx, current); err == nil && taken {
			if err = w.r.db.DetachDevice(ctx, w.Id); err == nil {
				w.mu.Lock()
				w.device = w.r.db.Device("", w.Id)
				err = w.renewAuthenticator()
				w.mu.Unlock()
			}
		}
	}
	if err != nil {
		return err
	}
	w.mu.Lock()
	auth := w.auth
	w.mu.Unlock()
	// Create the device now, so the sign-in runs under its lock.
	_, err = auth.Device()
	return err
}

// install saves a new session and makes its account the workspace's.
func (w *Workspace) install(ctx context.Context, session account.Session, input wire.LoginRequest, automatic bool) (err error) {
	defer func() { w.r.cfg.Report("login_save", err) }()
	ctx = context.WithoutCancel(ctx)
	password := ""
	if automatic {
		if password, err = w.rememberedPassword(ctx); err != nil {
			return err
		}
	} else if input.Remember {
		password = input.Password
	}
	write, err := w.seal(session, password)
	if err != nil {
		return err
	}
	identity := account.IdentityOf(session)
	w.mu.Lock()
	previous, deviceId := w.identity, w.device.Id()
	w.mu.Unlock()
	login := db.Login{Owner: identity.Owner, Name: identity.Account.Name, DeviceId: deviceId, SessionId: w.Id, Session: write,
		CancelDownloads: previous != nil && previous.Owner != identity.Owner}
	if !automatic {
		login.Logins = []string{account.NormalizeLogin(input.Email), account.NormalizeLogin(identity.Account.Email)}
	}
	if err = w.r.db.InstallLogin(ctx, login); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !automatic {
		w.accountCancel()
		w.accountCtx, w.accountCancel = context.WithCancel(w.ctx)
		w.catalog = catalogState{status: catalogIdle}
		w.login.retryAfter = time.Time{}
	}
	w.identity = &identity
	w.login.required, w.login.verification, w.login.err = false, false, ""
	if transport := account.Release(session); transport != nil {
		w.transport = transport
	}
	w.wakeWorker()
	return nil
}

// Logout signs the account out and cancels its downloads. In public mode the
// browser session ends too; it reports that, so the caller drops the cookie.
func (w *Workspace) Logout(ctx context.Context) (ended bool, err error) {
	w.mu.Lock()
	if w.login.busy {
		w.mu.Unlock()
		return false, fault.Busy
	}
	w.accountCancel()
	w.mu.Unlock()
	// Running Apple operations stop with the account; wait for them to leave.
	if err = w.apple.Lock(context.WithoutCancel(ctx)); err != nil {
		return false, err
	}
	defer w.apple.Unlock()
	ctx = context.WithoutCancel(ctx)
	if err = w.r.db.Logout(ctx, w.Id); err != nil {
		return false, err
	}
	w.mu.Lock()
	w.identity = nil
	w.login = loginState{busy: w.login.busy}
	w.catalog = catalogState{status: catalogIdle}
	w.auth.CancelVerification()
	w.mu.Unlock()
	if !w.r.cfg.Public {
		return false, nil
	}
	w.r.remove(w)
	w.r.retire(w)
	return true, w.r.db.ExpireSession(ctx, w.Id)
}
