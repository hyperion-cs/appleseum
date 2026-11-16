package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"
	_ "time/tzdata"

	"github.com/hyperion-cs/appleseum/applekit"
	"github.com/hyperion-cs/appleseum/applekit/hosting"

	"appleseum/backend/internal/app"
	"appleseum/backend/internal/crashreport"
	"appleseum/backend/internal/netcompat"
	"appleseum/backend/internal/product"
	"appleseum/backend/internal/rawlog"
	"appleseum/frontend"
)

func main() {
	if crashreport.RunMonitor() {
		return
	}
	err := run()
	if err != nil {
		rawlog.Log("server exited error=%s", rawlog.Error(err))
	}
	if closeErr := rawlog.Close(); closeErr != nil {
		fmt.Fprintf(os.Stdout, "Could not close raw debug log: %v\n", closeErr)
	}
	crashreport.Stop()
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	// The shutdown watchdog is armed last and released last: it bounds every cleanup below.
	var finishShutdown func()
	defer func() {
		if finishShutdown != nil {
			finishShutdown()
		}
	}()
	log.SetFlags(log.Ltime)
	o, err := parseOptions(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	ish := applekit.RunningInIsh()
	if ish && o.public {
		return errors.New("--public is not supported in iSH")
	}
	if err := prepareIshRuntime(ish); err != nil {
		return err
	}
	failureMessage := startupFailureMessage(ish)
	startupCtx, finishStartup := startupDeadline(startupTimeout, func() {
		fmt.Fprintln(os.Stdout, failureMessage)
		os.Exit(1)
	})
	defer finishStartup()
	if err := startDiagnostics(ish, o.debug); err != nil {
		return err
	}
	if ish {
		log.Print("iSH detected")
	}
	dir, err := dataDirectory(o.data)
	if err != nil {
		return err
	}
	addresses := inventory(startupCtx, o, ish)
	initialize := rawlog.Begin("startup server.New")
	service, err := app.New(app.Config{
		Admin:         o.settings,
		Debug:         o.debug == debugWeb,
		Ish:           ish,
		Public:        o.public,
		SharedLibrary: o.sharedLibrary,
		DataDir:       dir,
		Web:           frontend.Assets(),
		Hosts:         addresses.hosts,
		Resolver:      o.resolver,
		BonjourName:   addresses.bonjour,
		HttpPort:      port(o.http),
		ManifestPort:  port(o.https),
	})
	if err != nil {
		return err
	}
	initialize()
	defer func() { end := rawlog.Begin("shutdown app.Close"); defer end(); service.Close() }()

	listeners, err := listen(o, service, addresses.hosts)
	defer func() {
		for _, l := range listeners {
			l.raw.Close()
		}
	}()
	if err != nil {
		return err
	}
	result := make(chan error, len(listeners))
	for _, l := range listeners {
		go func() { result <- l.server.Serve(l.serving) }()
	}
	probeReady := rawlog.Begin("startup readiness probe")
	if err := waitForStartup(startupCtx, o.http, listeners[0].raw.Addr(), result); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return errors.New(failureMessage)
		}
		return err
	}
	probeReady()
	if !o.public && !slices.ContainsFunc(addresses.console, net.IPv6loopback.Equal) &&
		canReachIpv6Loopback(startupCtx, listeners[0].raw.Addr(), false) &&
		canReachIpv6Loopback(startupCtx, listeners[1].raw.Addr(), true) {
		addresses.console = append(slices.Clone(addresses.console), net.IPv6loopback)
	}
	// Cancel the startup timer before printing the ready message. It must
	// never apply to a running server, including while iSH is backgrounded.
	finishStartup()
	rawlog.Ready(o.http, listeners[0].raw.Addr())
	announce(o, service, addresses)

	ctx, beginShutdown, finish := shutdownSignals(shutdownTimeout, os.Exit)
	finishShutdown = finish
	if ish {
		location := rawlog.Begin("background open location device")
		releaseLocation := startIshBackground("/dev/location")
		location()
		defer releaseLocation()
	}
	select {
	case <-ctx.Done():
	case err = <-result:
		beginShutdown()
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	beginShutdown()
	stop(service, listeners)
	return nil
}

// startDiagnostics enables crash notifications and, for --debug=raw, the process log.
func startDiagnostics(ish bool, debug debugMode) error {
	if err := crashreport.Start(ish, debug == debugRaw); err != nil {
		return fmt.Errorf("enable critical error notifications: %w", err)
	}
	if debug != debugRaw {
		return nil
	}
	if _, err := rawlog.Start("debug.log"); err != nil {
		return fmt.Errorf("open raw debug log: %w", err)
	}
	path, _ := filepath.Abs("debug.log")
	fmt.Printf("Raw debug log: %s\n", path)
	return nil
}

func dataDirectory(data string) (string, error) {
	defer rawlog.Begin("startup data directory")()
	dir, err := filepath.Abs(data)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Join(dir, "packages"), 0o700); err != nil {
		return "", fmt.Errorf("create data directory: %w", err)
	}
	rawlog.Log("data directory=%q", dir)
	return dir, nil
}

// listener is one bound socket and the server that will answer on it.
type listener struct {
	server *http.Server
	// raw is the bound socket; serving adds TLS and diagnostics on top of it.
	raw, serving net.Listener
}

// listen binds the HTTP listener and, outside public mode, the manifest HTTPS
// listener. It returns what it bound even on failure, so the caller closes it.
func listen(o options, service *app.App, hosts []string) ([]listener, error) {
	type endpoint struct {
		address string
		handler http.Handler
		secure  bool
	}
	endpoints := []endpoint{{o.http, service.Handler(), false}}
	var certificates *tls.Config
	if o.https != "" {
		end := rawlog.Begin("startup TLS certificates")
		var err error
		certificates, err = hosting.Tls(hosts)
		end()
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, endpoint{o.https, service.Distribution(), true})
	}
	var listeners []listener
	for _, endpoint := range endpoints {
		bind := rawlog.Begin("startup listen " + endpoint.address)
		raw, err := netcompat.Listen("tcp", endpoint.address)
		bind()
		if err != nil {
			return listeners, fmt.Errorf("listen %s: %w", endpoint.address, err)
		}
		server := &http.Server{
			Handler:           rawlog.Handler(endpoint.handler),
			ConnState:         rawlog.ConnState,
			ErrorLog:          service.HttpErrorLog(),
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    65_536,
		}
		serving := raw
		if endpoint.secure {
			serving = tls.NewListener(raw, certificates)
		} else {
			server.WriteTimeout = 150 * time.Second
		}
		listeners = append(listeners, listener{server, raw, service.DebugListener(server, serving)})
	}
	return listeners, nil
}

func announce(o options, service *app.App, addresses hostInventory) {
	if o.public {
		fmt.Printf("%s v%s started (public mode)\nHTTP: %s\nRequires a reverse proxy with a valid HTTPS certificate.\n", product.DisplayName, product.Version, o.http)
	} else {
		fmt.Printf("%s v%s started\nHTTP: %s\nManifest HTTPS: %s (self-signed)\n", product.DisplayName, product.Version, o.http, o.https)
	}
	if o.settings {
		fmt.Println("Settings enabled: /settings")
	}
	if o.debug == debugWeb {
		fmt.Println("Debug mode enabled: /debug")
	}
	if o.public {
		fmt.Printf("\nTemporary root account:\n  Login: root@appleseum\n  Password: %s\n", service.ServicePassword())
		return
	}
	if addresses.bonjourErr != nil {
		fmt.Println("Same-device installation unavailable.")
		fmt.Println("Restart with --localhost-resolver=nip to enable it.")
	}
	console := addresses.console
	host, _, _ := net.SplitHostPort(o.http)
	if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
		console = []net.IP{ip}
	}
	fmt.Println("\nAvailable at:")
	if addresses.bonjour != "" {
		fmt.Printf("  - http://%s\n", net.JoinHostPort(addresses.bonjour, port(o.http)))
	}
	for _, ip := range console {
		fmt.Printf("  - http://%s\n", net.JoinHostPort(ip.String(), port(o.http)))
	}
}

// stop cancels running work, then gives open requests a moment to finish.
func stop(service *app.App, listeners []listener) {
	fmt.Println("Stopping server; press Ctrl+C again to force exit. Shutdown limit: 15s.")
	rawlog.Log("shutdown requested; forced exit after %s", shutdownTimeout)
	cancelWork := rawlog.Begin("shutdown cancel active work")
	service.BeginShutdown()
	cancelWork()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, l := range listeners {
		if err := rawlog.Call("shutdown HTTP", func() error { return l.server.Shutdown(ctx) }); err != nil {
			l.server.Close()
		}
	}
}
