package session

import (
	"cmp"
	"context"
	"errors"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/account"
	"appleseum/backend/internal/db"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/rawlog"
	"appleseum/backend/internal/wire"
)

const (
	signInTimeout = 2 * time.Minute
	// After an automatic sign-in, successful or not, the next one waits this long.
	reauthInterval = time.Minute
)

// call runs one Apple operation; the caller holds the apple lock. When Apple
// rejects the session, it signs in once with the remembered password and
// repeats the operation. Network failures never trigger a sign-in.
//
// It returns a fault code when the account needs the user's attention, and
// Apple's own error otherwise.
func (w *Workspace) call(ctx context.Context, action func(account.Session) error) error {
	w.mu.Lock()
	identity, login := w.identity, w.login
	w.mu.Unlock()
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case identity == nil:
		return context.Canceled
	case login.required:
		return cmp.Or(login.err, fault.AppleUnavailable)
	}
	err := w.perform(ctx, action)
	if !errors.Is(err, applekit.ErrSessionExpired) || ctx.Err() != nil {
		return err
	}
	w.mu.Lock()
	login = w.login
	w.mu.Unlock()
	if login.required || login.busy || time.Now().Before(login.retryAfter) {
		return cmp.Or(login.err, fault.AppleUnavailable)
	}
	password, e := w.rememberedPassword(ctx)
	if e != nil {
		w.r.cfg.Report("credentials_read", e)
		return fault.Of(e)
	}
	if password == "" {
		return err
	}
	w.setReauth(true)
	defer w.setReauth(false)
	authCtx, cancel := context.WithTimeout(ctx, signInTimeout)
	defer cancel()
	e = w.authenticate(authCtx, wire.LoginRequest{Email: identity.Account.Email, Password: password}, identity.Owner)
	if e != nil {
		code := account.LoginFailure(e)
		// Only Apple's explicit refusal asks the user; a network failure is retried later.
		if code == fault.TwoFactorRequired || errors.Is(e, applekit.ErrCredentialsRejected) || errors.Is(e, applekit.ErrPasswordRejected) {
			w.mu.Lock()
			w.login.required, w.login.verification, w.login.err = true, code == fault.TwoFactorRequired, code
			w.mu.Unlock()
		}
		return code
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err = w.perform(ctx, action); errors.Is(err, applekit.ErrSessionExpired) {
		// A fresh sign-in did not restore access: ask the user instead of
		// signing in again from the next poll or queued job.
		w.mu.Lock()
		w.login.required, w.login.err = true, fault.ReauthenticationFailed
		w.mu.Unlock()
		return fault.ReauthenticationFailed
	}
	return err
}

func (w *Workspace) setReauth(busy bool) {
	w.mu.Lock()
	w.login.busy, w.login.retryAfter = busy, time.Now().Add(reauthInterval)
	w.mu.Unlock()
}

// perform opens the saved session on its device, runs the action and saves
// what the session changed. Workspaces sharing the device take turns.
func (w *Workspace) perform(ctx context.Context, action func(account.Session) error) error {
	defer rawlog.Begin("apple call")()
	deviceId, err := w.r.db.SessionDevice(ctx, w.Id)
	if err != nil {
		return fault.Of(err)
	}
	w.mu.Lock()
	if deviceId != w.device.Id() {
		w.device = w.r.db.Device(deviceId, w.Id)
		err = w.renewAuthenticator()
	}
	epoch := w.accountCtx
	w.mu.Unlock()
	if err != nil {
		return fault.Of(err)
	}
	device := w.r.deviceLock(deviceId)
	if err = device.Lock(ctx); err != nil {
		return err
	}
	defer device.Unlock()
	session, err := w.open(ctx)
	if err != nil {
		return fault.Of(err)
	}
	err = action(session)
	saveErr := w.save(ctx, session, epoch)
	w.mu.Lock()
	if transport := account.Release(session); transport != nil {
		w.transport = transport
	}
	w.mu.Unlock()
	if saveErr != nil {
		w.r.cfg.Report("session_save", saveErr)
		return fault.Of(saveErr)
	}
	return err
}

func (w *Workspace) open(ctx context.Context) (account.Session, error) {
	record, err := w.r.db.ReadSession(ctx, w.Id)
	if err != nil {
		return nil, err
	}
	secrets, err := account.Open(w.sessionKey(), w.Id, record.Sealed)
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	auth, transport := w.auth, w.transport
	w.mu.Unlock()
	return w.r.cfg.Apple.Open(auth, record.Identity, applekit.SessionSecrets{PasswordToken: secrets.PasswordToken, Cookies: secrets.Cookies}, transport)
}

// save stores the session's renewed secrets, in one transaction with the
// device registration a live session may have changed. An account that left
// meanwhile is not written back.
func (w *Workspace) save(ctx context.Context, session account.Session, epoch context.Context) error {
	ctx = context.WithoutCancel(ctx)
	w.mu.Lock()
	current, device, auth := w.accountCtx == epoch && epoch.Err() == nil, w.device, w.auth
	w.mu.Unlock()
	if !current {
		return nil
	}
	password, err := w.rememberedPassword(ctx)
	if err != nil {
		return err
	}
	write, err := w.seal(session, password)
	if err != nil {
		return err
	}
	device.SaveWith(&write)
	err = account.Register(auth, session)
	if committed := device.SaveWith(nil); err != nil || committed {
		return err
	}
	return write.Commit(ctx, w.r.db)
}

func (w *Workspace) seal(session account.Session, password string) (db.SessionWrite, error) {
	secrets, err := session.SessionSecrets()
	if err != nil {
		return db.SessionWrite{}, err
	}
	sealed, err := account.Seal(w.sessionKey(), w.Id, account.Secrets{Password: password, PasswordToken: secrets.PasswordToken, Cookies: secrets.Cookies})
	return db.SessionWrite{Id: w.Id, Parameters: session.SessionParameters(), Sealed: sealed}, err
}

// rememberedPassword is the password saved at sign-in, or "" when the user chose not to save it.
func (w *Workspace) rememberedPassword(ctx context.Context) (string, error) {
	sealed, err := w.r.db.SealedSession(ctx, w.Id)
	if err != nil || len(sealed) == 0 {
		return "", err
	}
	secrets, err := account.Open(w.sessionKey(), w.Id, sealed)
	return secrets.Password, err
}

// appleFailure names an error of call for the client. A session Apple no
// longer accepts, with no password to renew it, signs the account out.
func (w *Workspace) appleFailure(ctx context.Context, err error) fault.Code {
	var code fault.Code
	switch {
	case errors.As(err, &code):
		return code
	// Purchase history has its own authorization; its refusal says nothing about the store session.
	case applekit.IsPurchaseHistoryAuthentication(err):
		return fault.PurchaseHistoryAuthentication
	case !errors.Is(err, applekit.ErrSessionExpired):
		return fault.AppleUnavailable
	}
	w.mu.Lock()
	signedIn := w.identity != nil
	if signedIn {
		w.accountCancel()
		w.identity = nil
		w.catalog.status, w.catalog.err = wire.CatalogFailed, fault.SessionExpired
	}
	w.mu.Unlock()
	if signedIn {
		w.r.cfg.Report("session_expire", w.r.db.ForgetSecrets(context.WithoutCancel(ctx), w.Id))
	}
	return fault.SessionExpired
}
