package wire

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"appleseum/backend/internal/fault"
)

const (
	MaxSelectionSize  = 512
	MaxPageSize       = 128
	MinSearchLength   = 3
	MaxFilenameBytes  = 512
	MaxMetadataLength = 256
	MaxBundleIdLength = 255
)

const (
	maxQueryLength         = 200
	maxLoginLength         = 320
	maxPasswordLength      = 1_024
	verificationCodeLength = 6
	maxVersionCursorLength = 1_024
	maxNameLength          = 100
	maxNumberLength        = 32
)

// Text reports whether value is valid UTF-8 of at most max bytes without control characters.
func Text(value string, max int) bool {
	return len(value) <= max && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

// Search reports whether a query is empty or long enough for the trigram index.
func Search(value string) bool {
	return Text(value, maxQueryLength) && (value == "" || utf8.RuneCountInString(value) >= MinSearchLength)
}

// Uuid reports whether value is a UUID in its canonical form.
func Uuid(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

// Number reports whether value is a positive decimal integer such as an App Store or version ID.
func Number(value string) bool {
	return value != "" && len(value) <= maxNumberLength && strings.Trim(value, "0123456789") == "" && strings.TrimLeft(value, "0") != ""
}

func numbers(ids []string) bool {
	return !slices.ContainsFunc(ids, func(id string) bool { return !Number(id) })
}

func invalid(ok bool) error {
	if ok {
		return nil
	}
	return fault.InvalidRequest
}

func distinctUuids(ids []string) bool {
	if len(ids) > MaxSelectionSize {
		return false
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !Uuid(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// Validate accepts either explicit ids or a filter, never both. Ids are
// checked by the list they name: see For.
func (s Selection) Validate() error {
	if (s.Filter == nil) == (len(s.Ids) == 0) || len(s.Ids) > MaxSelectionSize || s.Filter != nil && !Search(s.Filter.Query) {
		return fault.InvalidRequest
	}
	return nil
}

// For checks a selection of a table's rows: catalog apps by App Store id,
// library files and shares by UUID, each list with only its own filter.
func (s Selection) For(table Table) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if f := s.Filter; f != nil && (f.Delisted && table != CatalogTable || f.Mine && table != LibraryTable) {
		return fault.InvalidRequest
	}
	if table == CatalogTable {
		return invalid(numbers(s.Ids))
	}
	return invalid(distinctUuids(s.Ids))
}

func (r DownloadRequest) Validate() error {
	if err := r.Selection.For(CatalogTable); err != nil {
		return err
	}
	if len(r.Versions) > MaxSelectionSize || r.Selection.Filter != nil && len(r.Versions) > 0 {
		return fault.InvalidRequest
	}
	return invalid(!slices.ContainsFunc(r.Versions, func(v DownloadVersion) bool { return !Number(v.AppId) || !Number(v.VersionId) }))
}

func (r LoginRequest) Validate() error {
	if !Text(r.Email, maxLoginLength) || !Text(r.Password, maxPasswordLength) {
		return fault.InvalidCredentials
	}
	code := r.Code == "" || len(r.Code) == verificationCodeLength && strings.Trim(r.Code, "0123456789") == ""
	if !code {
		return fault.InvalidCredentials
	}
	return nil
}

func (r VersionPageRequest) Validate() error {
	return invalid(r.Limit >= 0 && r.Limit <= MaxPageSize && Text(r.Cursor, maxVersionCursorLength) &&
		(r.Revision == "" || Uuid(r.Revision)) && (r.Move == "" || r.Move == "next" || r.Move == "prev"))
}

func (p PreferencesInput) Validate() error {
	return invalid(slices.Contains(Languages, p.Language) && slices.Contains(Themes, p.Theme))
}

func order(sort string, direction Direction) error {
	return invalid(Text(sort, maxNameLength) && sort != "" && slices.Contains(Directions, direction))
}

func (v CatalogView) Validate() error { return order(v.Sort, v.Direction) }

func (v LibraryView) Validate() error { return order(v.Sort, v.Direction) }

func (v ShareView) Validate() error { return order(v.Sort, v.Direction) }

func (d DeviceSettings) Validate() error {
	ok := Text(d.Model, maxNameLength) && Text(d.OsVersion, maxNameLength) &&
		Text(d.Locale, maxNameLength) && Text(d.TimeZone, maxNameLength)
	if !ok {
		return fault.InvalidDeviceSettings
	}
	return nil
}

func (r DebugExportRequest) Validate() error {
	if len(r.Ids) == 0 || len(r.Ids) > MaxSelectionSize {
		return fault.InvalidRequest
	}
	seen := make(map[int64]bool, len(r.Ids))
	for _, id := range r.Ids {
		if id <= 0 || seen[id] {
			return fault.InvalidRequest
		}
		seen[id] = true
	}
	return nil
}
