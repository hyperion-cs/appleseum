package product

const (
	Name                = "appleseum"
	DisplayName         = "Appleseum"
	Tagline             = "Your apps are here"
	TaglineRu           = "Ваши приложения здесь"
	RepositoryUrl       = "https://github.com/hyperion-cs/appleseum"
	DefaultHttpPort     = "8080"
	DefaultManifestPort = "8443"
)

// Release builds override Version with -ldflags -X.
var Version = "1.0.0"
