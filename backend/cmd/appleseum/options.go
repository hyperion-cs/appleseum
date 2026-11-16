package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/hyperion-cs/appleseum/applekit/hosting"

	"appleseum/backend/internal/product"
)

// options is the validated command line.
type options struct {
	settings, public, sharedLibrary bool
	debug                           debugMode
	data                            string
	resolver                        hosting.Resolver
	// http and https are host:port; https is empty in public mode, where a proxy terminates TLS.
	http, https string
}

// parseOptions reads the command line; --help returns flag.ErrHelp.
func parseOptions(args []string) (options, error) {
	var o options
	flags := flag.NewFlagSet(product.Name, flag.ContinueOnError)
	flags.BoolVar(&o.settings, "settings", false, "enable the settings tab and its API")
	flags.Var(&o.debug, "debug", "debug mode: web (default when enabled) or raw")
	flags.BoolVar(&o.public, "public", false, "use separate browser sessions behind an HTTPS reverse proxy")
	flags.BoolVar(&o.sharedLibrary, "shared-library", false, "share the local library among all users in public mode")
	flags.StringVar(&o.data, "data", product.DefaultDataDir, "store runtime data in `PATH`")
	flags.StringVar(&o.http, "http", product.DefaultHttpPort, "HTTP listen address")
	flags.StringVar(&o.https, "https", product.DefaultManifestPort, "HTTPS listen address")
	flags.TextVar(&o.resolver, "localhost-resolver", hosting.Bonjour, "hostname resolver for same-device installation in iSH")
	flags.Usage = func() { usage(flags.Output()) }
	if err := flags.Parse(args); err != nil {
		return o, err
	}
	if flags.NArg() != 0 {
		return o, fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}
	explicitHttps := false
	flags.Visit(func(option *flag.Flag) { explicitHttps = explicitHttps || option.Name == "https" })
	switch {
	case o.public && (o.debug == debugWeb || explicitHttps):
		return o, errors.New("--public cannot be combined with --debug=web or --https")
	case o.sharedLibrary && !o.public:
		return o, errors.New("--shared-library requires --public")
	}
	if o.public {
		o.https = ""
	}
	for _, option := range []struct {
		name    string
		address *string
	}{{"http", &o.http}, {"https", &o.https}} {
		if *option.address == "" {
			continue
		}
		address, err := listenAddress(*option.address)
		if err != nil {
			return o, fmt.Errorf("--%s: %w", option.name, err)
		}
		*option.address = address
	}
	return o, nil
}

// addresses are the configured listeners, HTTP first.
func (o options) addresses() []string {
	if o.https == "" {
		return []string{o.http}
	}
	return []string{o.http, o.https}
}

// listenAddress accepts PORT or IP:PORT; a bare port listens on all network interfaces.
func listenAddress(address string) (string, error) {
	if _, err := strconv.ParseUint(address, 10, 16); err == nil {
		address = net.JoinHostPort("0.0.0.0", address)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return address, err
	}
	if host != "" && net.ParseIP(host) == nil {
		return address, errors.New("expected a port or IP:port")
	}
	if _, err = strconv.ParseUint(port, 10, 16); err != nil {
		return address, errors.New("invalid port")
	}
	return address, nil
}

// port is the port of an address listenAddress accepted, or "" for none.
func port(address string) string {
	_, port, _ := net.SplitHostPort(address)
	return port
}

func usage(output io.Writer) {
	fmt.Fprintf(output, `%s v%s
%s. Even apps delisted from the App Store – install them on iPhone/iPad over your local network, without a cable.

Usage:
  %[4]s [options]

Options:
  --http PORT|IP:PORT
    HTTP listener for frontend, our API and IPA files.
    If only a port is specified, the server listens on all network interfaces.
    Default: %[5]s

  --https PORT|IP:PORT
    HTTPS listener for installation manifests, using a self-signed certificate.
    If only a port is specified, the server listens on all network interfaces.
    Cannot be specified with --public.
    Default: %[6]s; disabled with --public

  --localhost-resolver bonjour|nip
    Hostname resolver for same-device installation in iSH.
    bonjour: use the device's local Bonjour service (default).
    nip: resolve the local IP via nip.io.
    Warning: nip uses third-party DNS outside this device and Apple;
    no HTTP requests are sent to nip.io.

  --public
    Use separate browser sessions.
    Disable the built-in HTTPS listener and serve everything on --http.
    Requires a reverse proxy with a valid HTTPS certificate for the entire utility.
    Cannot be combined with --debug=web or --https.

  --shared-library
    Share the local library among all users; otherwise, show only the current account’s apps.
    Requires --public. Other accounts’ apps remain read-only.

  --data PATH
    Runtime data directory.
    Default: %[7]s

  --settings
    Enable the settings tab and its API.

  --debug[=web|raw]
    --debug or --debug=web: enable the debug tab, HTTP logs and internal errors.
    Web logs are stored in logs/ inside the data directory.
    --debug=raw: write process, network and database diagnostics to debug.log
    in the current directory, without the debug tab.
    Only web mode is incompatible with --public.

  -h, --help
    Show this help and exit.

Examples:
  %[4]s
  %[4]s --public --http 127.0.0.1:%[5]s
`, product.DisplayName, product.Version, product.Tagline, product.Name, product.DefaultHttpPort, product.DefaultManifestPort, product.DefaultDataDir)
}
