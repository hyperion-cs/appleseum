package wire

const (
	Queued      DownloadStatus = "queued"
	Downloading DownloadStatus = "downloading"
	Preparing   DownloadStatus = "preparing"
	Canceling   DownloadStatus = "canceling"
	Done        DownloadStatus = "done"
	Failed      DownloadStatus = "failed"
	Canceled    DownloadStatus = "canceled"
)

const (
	CatalogIdle    CatalogStatus = "idle"
	CatalogLoading CatalogStatus = "loading"
	CatalogReady   CatalogStatus = "ready"
	CatalogFailed  CatalogStatus = "failed"
)

const (
	English Language = "en"
	Russian Language = "ru"
)

const (
	Light Theme = "light"
	Dark  Theme = "dark"
)

const (
	CatalogTable Table = "catalog"
	LibraryTable Table = "library"
	SharesTable  Table = "shares"
)

const (
	Ascending  Direction = "asc"
	Descending Direction = "desc"
)

const (
	AuthReady    AuthState = "ready"
	AuthPassword AuthState = "password"
	AuthCode     AuthState = "code"
)

const (
	InstallWaiting      InstallationStage = "waiting"
	InstallManifest     InstallationStage = "manifest"
	InstallWaitingIpa   InstallationStage = "waitingIpa"
	InstallTransferring InstallationStage = "transferring"
	InstallInterrupted  InstallationStage = "interrupted"
	InstallFailed       InstallationStage = "failed"
	InstallComplete     InstallationStage = "complete"
)

// DownloadStatus is the state of one transfer; the schema's CHECK lists the same values.
type DownloadStatus string

var DownloadStatuses = []DownloadStatus{Queued, Downloading, Preparing, Canceling, Done, Failed, Canceled}

// CatalogStatus is the state of the account's purchase list refresh.
type CatalogStatus string

var CatalogStatuses = []CatalogStatus{CatalogIdle, CatalogLoading, CatalogReady, CatalogFailed}

type Language string

var Languages = []Language{English, Russian}

type Theme string

var Themes = []Theme{Light, Dark}

// Table names a list whose sorting and filters are saved per browser session.
type Table string

var TableNames = []Table{CatalogTable, LibraryTable, SharesTable}

type Direction string

var Directions = []Direction{Ascending, Descending}

// AuthState is what signing in waits for: nothing, the password again, or a verification code.
type AuthState string

var AuthStates = []AuthState{AuthReady, AuthPassword, AuthCode}

// InstallationStage is how far the server has seen one installation attempt
// get: nothing yet, the manifest, the IPA files, or the end of their delivery.
// An interrupted transfer may still resume; a failed manifest ends the attempt.
type InstallationStage string

var InstallationStages = []InstallationStage{InstallWaiting, InstallManifest, InstallWaitingIpa, InstallTransferring, InstallInterrupted, InstallFailed, InstallComplete}
