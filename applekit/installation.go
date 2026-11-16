package applekit

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/adi"
	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/configurator"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

type AuthenticatorOptions struct {
	Transport http.RoundTripper
	// An omitted model and OS select a random compatible pair for a new store.
	Model     string
	OsVersion string
	Store     DeviceStore
	// StatePath selects file storage; Store supplies custom storage.
	StatePath string
	// Storefront is a bootstrap hint; authentication supplies the account country.
	Storefront   string
	Language     string
	Locale       string
	TimeZone     string
	ComplianceId string
}

// DeviceStore must save atomically and return os.ErrNotExist when empty.
// Use one authenticator per store; it serializes access.
type DeviceStore interface {
	Load() (DeviceRecord, error)
	Save(DeviceRecord) error
}

// DeviceRecord persists identity and registrations; GUID/build are derived from UUID/settings.
type DeviceRecord struct {
	Uuid           [16]byte
	LocalUser      string
	IdmsGeneration int
	Idms           []byte
	Routing        uint64
	Amd            []byte
	Storefront     string
	Settings       DeviceSettings
}

func ReadDeviceRecord(raw []byte) (DeviceRecord, error) {
	s, err := parseInstallation(raw)
	if err != nil {
		return DeviceRecord{}, err
	}
	return s.record(), nil
}

func (s *installation) record() DeviceRecord {
	settings := DefaultDeviceSettings()
	if s.Settings != nil {
		settings = *s.Settings
	}
	settings = settings.withPlatformDefaults()
	return DeviceRecord{s.Uuid, s.LocalUser, s.IdmsGeneration, s.Idms, s.Routing, s.Amd, s.Storefront, settings}
}

func installationFromRecord(d DeviceRecord) (*installation, error) {
	return validateInstallation(&installation{Version: 1, Uuid: d.Uuid, LocalUser: d.LocalUser, IdmsGeneration: d.IdmsGeneration, Idms: d.Idms, Routing: d.Routing, Amd: d.Amd, Storefront: d.Storefront, Settings: &d.Settings})
}

func ValidateDeviceRecord(d DeviceRecord) error {
	_, err := installationFromRecord(d)
	return err
}

type installation struct {
	Version        int
	Uuid           [16]byte `json:"UUID"`
	LocalUser      string
	IdmsGeneration int    `json:"IDMSGeneration,omitempty"`
	Idms           []byte `json:"IDMS"`
	Routing        uint64
	Amd            []byte `json:"AMD"`
	Storefront     string
	Settings       *DeviceSettings `json:",omitempty"`
	idms           *adi.Idms       // immutable parsed state; never serialized
}

func loadInstallation(store DeviceStore, settings DeviceSettings) (*installation, error) {
	record, err := store.Load()
	if errors.Is(err, os.ErrNotExist) {
		settings, err = initialDeviceSettings(settings)
		if err != nil {
			return nil, err
		}
		s, err := newInstallation()
		if err != nil {
			return nil, err
		}
		s.Settings = &settings
		if err = saveInstallation(store, s); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	return installationFromRecord(record)
}

func ValidateDeviceState(raw []byte) error {
	_, err := parseInstallation(raw)
	return err
}

func parseInstallation(raw []byte) (*installation, error) {
	var s installation
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, errors.New("invalid Apple installation state")
	}
	return validateInstallation(&s)
}

func validateInstallation(s *installation) (*installation, error) {
	if s.Version != 1 || s.Uuid == [16]byte{} || len(s.LocalUser) != 64 {
		return nil, errors.New("invalid Apple installation identity")
	}
	if _, err := hex.DecodeString(s.LocalUser); err != nil {
		return nil, errors.New("invalid Apple installation local user")
	}
	if s.Settings != nil {
		if err := s.Settings.validate(); err != nil {
			return nil, err
		}
	}
	if len(s.Idms) > 0 {
		if _, err := s.idmsRecord(); err != nil {
			return nil, err
		}
	}
	if len(s.Amd) > 0 {
		if _, err := adi.ParseAmd(s.Amd); err != nil {
			return nil, err
		}
		if !bytes.Equal(s.Amd[4:20], s.Uuid[:]) {
			return nil, errors.New("AMD belongs to another installation")
		}
	}
	return s, nil
}

func newInstallation() (*installation, error) {
	s := &installation{Version: 1}
	if _, err := rand.Read(s.Uuid[:]); err != nil {
		return nil, err
	}
	s.Uuid[6] = s.Uuid[6]&15 | 64
	s.Uuid[8] = s.Uuid[8]&63 | 128
	var local [32]byte
	if _, err := rand.Read(local[:]); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(local[:])
	s.LocalUser = strings.ToUpper(hex.EncodeToString(digest[:]))
	return s, nil
}

func saveInstallation(store DeviceStore, s *installation) error {
	return store.Save(s.record())
}

func (s *installation) deviceId() string {
	b := s.Uuid
	return fmt.Sprintf("%X-%X-%X-%X-%X", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (s *installation) guid() string { return strings.ToUpper(hex.EncodeToString(s.Uuid[:6])) }

func (s *installation) amdProvider() (*adi.Amd, error) {
	if len(s.Amd) > 0 {
		return adi.ParseAmd(s.Amd)
	}
	return adi.NewAmd(s.Uuid)
}

func (s *installation) snapshot(options AuthenticatorOptions, at time.Time) (configurator.DeviceSnapshot, error) {
	state, err := s.idmsRecord()
	if err != nil {
		return configurator.DeviceSnapshot{}, err
	}
	mid, otp, err := state.Headers(at)
	if err != nil {
		return configurator.DeviceSnapshot{}, err
	}
	device := ams.NativeDevice{LocalUser: s.LocalUser, Md: base64.StdEncoding.EncodeToString(otp), MachineId: base64.StdEncoding.EncodeToString(mid), Routing: strconv.FormatUint(s.Routing, 10)}
	cpd := map[string]any{
		"X-Apple-I-MD": device.Md, "X-Apple-I-MD-M": device.MachineId, "X-Apple-I-MD-LU": device.LocalUser, "X-Apple-I-MD-RINFO": device.Routing,
		"X-Mme-Device-Id": s.deviceId(), "X-Apple-I-Client-Time": at.UTC().Format(time.RFC3339), "X-Apple-Locale": options.Locale, "X-Apple-I-TimeZone": options.TimeZone,
		"svct": "itunesstore", "at": uint64(2), "capp": "Apple Configurator", "cbid": "com.apple.configurator.ui", "bootstrap": true,
	}
	return configurator.DeviceSnapshot{Cpd: cpd, Native: device, Guid: s.guid(), ClientInfo: clientInfo(options), Language: options.Language, Locale: options.Locale, TimeZone: options.TimeZone, ComplianceId: options.ComplianceId, ClientTime: at}, nil
}

func clientInfo(options AuthenticatorOptions) string {
	return options.platform().ClientInfo("com.apple.AuthKit/1 (com.apple.configurator.ui/" + httpx.ConfiguratorVersion + ")")
}

func (s *installation) idmsRecord() (*adi.Idms, error) {
	if s.idms == nil {
		var err error
		s.idms, err = adi.ParseIdms(s.Idms)
		if err != nil {
			return nil, err
		}
	}
	return s.idms, nil
}
