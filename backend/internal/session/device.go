package session

import (
	"context"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/account"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

const (
	DeviceSettings DeviceChange = iota
	DeviceDefaults
	DeviceReset
)

// DeviceChange is what an administrator does to the device Apple sees.
type DeviceChange int

// readDevice returns the workspace's device, creating it on first use, and
// whether it belongs to an account this workspace is not signed in to.
func (w *Workspace) readDevice(ctx context.Context, changing bool) (applekit.DeviceProfile, bool, error) {
	w.mu.Lock()
	service, identity, device := w.service, w.identity, w.device
	busy := w.login.busy || changing && identity != nil && (w.login.verification || w.auth.VerificationPending())
	w.mu.Unlock()
	switch {
	case service:
		return applekit.DeviceProfile{}, false, fault.ActionUnavailable
	case busy:
		return applekit.DeviceProfile{}, false, fault.Busy
	}
	deviceId, foreign := device.Id(), false
	if w.r.cfg.Public && deviceId != "" {
		access, err := w.r.db.DeviceAccess(ctx, w.Id, deviceId)
		if err != nil {
			return applekit.DeviceProfile{}, false, err
		}
		foreign = access.Account != "" && (identity == nil || identity.Owner != access.Account) || identity != nil && access.Active != deviceId
	}
	if changing && foreign {
		return applekit.DeviceProfile{}, false, fault.DeviceAuthenticationRequired
	}
	// A separate reader leaves a sign-in in progress on the workspace's authenticator untouched.
	saved := w.r.db.Device(deviceId, w.Id)
	reader, err := applekit.NewAuthenticator(applekit.AuthenticatorOptions{Store: saved})
	if err != nil {
		return applekit.DeviceProfile{}, false, err
	}
	current, err := reader.Device()
	if err == nil && deviceId == "" {
		device.SetId(saved.Id())
	}
	return current, foreign, err
}

// Device describes the device Apple sees; ReadOnly marks another account's device.
func (w *Workspace) Device(ctx context.Context) (wire.DeviceProfile, error) {
	current, foreign, err := w.readDevice(ctx, false)
	profile := account.DeviceProfile(current)
	profile.ReadOnly = foreign
	return profile, err
}

// ChangeDevice applies new settings, the defaults or a new identity. A new
// model or a reset replaces the identity, which signs out every session on the device.
func (w *Workspace) ChangeDevice(ctx context.Context, change DeviceChange, input wire.DeviceSettings) (wire.DeviceProfile, error) {
	var none wire.DeviceProfile
	requested, err := account.AppleSettings(input)
	if change == DeviceSettings && err != nil {
		return none, fault.InvalidDeviceSettings
	}
	if !w.apple.TryLock() {
		return none, fault.Busy
	}
	defer w.apple.Unlock()
	w.mu.Lock()
	device := w.device
	w.mu.Unlock()
	if shared := w.r.deviceLock(device.Id()); !shared.TryLock() {
		return none, fault.Busy
	} else {
		defer shared.Unlock()
	}
	current, _, err := w.readDevice(ctx, true)
	if err != nil {
		return none, err
	}
	settings := current.Settings
	switch change {
	case DeviceSettings:
		settings = requested
		// A change of the OS version alone keeps Apple's stored metadata as it is.
		if unchanged := account.DeviceSettings(current.Settings); unchanged.Model == input.Model && unchanged.Locale == input.Locale && unchanged.TimeZone == input.TimeZone {
			settings = current.Settings
			settings.OsVersion = input.OsVersion
		}
	case DeviceDefaults:
		settings = applekit.DefaultDeviceSettings()
	}
	running, err := w.r.db.SessionTransfersRunning(ctx, w.Id)
	if err != nil {
		return none, err
	}
	w.mu.Lock()
	loading, signedIn := w.catalog.status == wire.CatalogLoading, w.identity != nil
	if !running && !loading {
		err = w.renewAuthenticator()
	}
	auth := w.auth
	w.mu.Unlock()
	switch {
	case running || loading:
		return none, fault.Busy
	case err != nil:
		return none, err
	}
	replaces := change == DeviceReset || settings.Model != current.Settings.Model
	if replaces && signedIn {
		device.ReplacingIdentity(true)
		defer device.ReplacingIdentity(false)
	}
	var result applekit.DeviceProfile
	if change == DeviceReset {
		result, err = auth.ResetDevice()
	} else {
		result, err = auth.SetDeviceSettings(settings)
	}
	if err != nil {
		return none, err
	}
	if replaces {
		w.r.deviceReplaced(device.Id())
	}
	w.mu.Lock()
	if w.identity == nil {
		w.login.required, w.login.verification, w.login.err = false, false, ""
	}
	w.mu.Unlock()
	profile := account.DeviceProfile(result)
	profile.ReadOnly = w.r.cfg.Public && replaces && signedIn
	return profile, nil
}

// deviceReplaced signs the workspace out if it used the device whose identity changed.
func (w *Workspace) deviceReplaced(deviceId string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.device.Id() != deviceId {
		return
	}
	w.accountCancel()
	w.identity = nil
	w.login.required, w.login.verification, w.login.err = false, false, ""
	w.catalog = catalogState{status: catalogIdle}
	w.auth.CancelVerification()
	w.r.cfg.Report("device_reload", w.renewAuthenticator())
}

// UploadContext bounds an upload by the presence of whoever started it:
// the signed-in account, or the workspace for a guest.
func (w *Workspace) UploadContext(ctx context.Context) (context.Context, context.CancelFunc) {
	w.mu.Lock()
	present := w.ctx
	if w.identity != nil {
		present = w.accountCtx
	}
	w.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(present, cancel)
	return ctx, func() { stop(); cancel() }
}
