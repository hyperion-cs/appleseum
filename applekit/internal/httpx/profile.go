package httpx

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"

	utls "github.com/refraction-networking/utls"
)

const (
	Configurator Profile = iota
	Accounts
	NativeAuthentication
	AuthKit
	AuthKitPostData
	AmsAccounts
	MacAppStore
)

const (
	ConfiguratorVersion          = "2.21"
	AccountsVersion              = "113"
	DefaultDeviceModel           = "MacBookPro18,1"
	DefaultMacOsVersion          = "26.5"
	DefaultMacOsBuild            = "25F71"
	DefaultMacUserAgentSuffix    = " (Macintosh; OS X " + DefaultMacOsVersion + "; " + DefaultMacOsBuild + ") AppleWebKit/1624.2.5.11.4 AMS/1 (dt:1)"
	DefaultConfiguratorUserAgent = "Configurator/" + ConfiguratorVersion + " Configurator/" + ConfiguratorVersion + DefaultMacUserAgentSuffix
	DefaultAccountsUserAgent     = "com.apple.accountsd/" + AccountsVersion + DefaultMacUserAgentSuffix
	DefaultAuthKitUserAgent      = "akd/1.0 CFNetwork/3860.600.21 Darwin/25.5.0"
	DefaultAmsAccountsUserAgent  = "com.apple.amsaccountsd/1.0" + DefaultMacUserAgentSuffix
	DefaultMacAppStoreUserAgent  = "MacAppStore/3.0 MacAppStore/3.0" + DefaultMacUserAgentSuffix
)

// NativeAuthentication uses accountsd's TLS profile on behalf of Configurator.
type Profile uint8

type profileKey struct{}

func WithProfile(ctx context.Context, profile Profile) context.Context {
	return context.WithValue(ctx, profileKey{}, profile)
}

func profileFromContext(ctx context.Context) Profile {
	profile, _ := ctx.Value(profileKey{}).(Profile)
	return profile
}

func profileFor(req *http.Request) Profile { return profileFromContext(req.Context()) }

func UserAgentFor(ctx context.Context) string {
	return PlatformFromContext(ctx).UserAgent(profileFromContext(ctx))
}

func (p Profile) UserAgent() string {
	switch p {
	case MacAppStore:
		return DefaultMacAppStoreUserAgent
	case Accounts:
		return DefaultAccountsUserAgent
	case AuthKit, AuthKitPostData:
		return DefaultAuthKitUserAgent
	case AmsAccounts:
		return DefaultAmsAccountsUserAgent
	default:
		return DefaultConfiguratorUserAgent
	}
}

// Pinned presets keep the ClientHello layout stable.
func (p Profile) clientHelloSpec() (utls.ClientHelloSpec, error) {
	if p > MacAppStore {
		return utls.ClientHelloSpec{}, fmt.Errorf("httpx: unknown client profile %d", p)
	}
	spec, err := utls.UTLSIdToSpec(utls.HelloSafari_26_3)
	if err != nil {
		return spec, fmt.Errorf("httpx build ClientHello: %w", err)
	}
	spec.TLSVersMin, spec.TLSVersMax = 0, 0
	for _, extension := range spec.Extensions {
		switch extension := extension.(type) {
		case *utls.SupportedVersionsExtension:
			if p == Accounts || p == NativeAuthentication {
				extension.Versions = append(extension.Versions, utls.VersionTLS11, utls.VersionTLS10)
			}
		case *utls.ALPNExtension:
			if p == AuthKitPostData {
				extension.AlpnProtocols = []string{"http/1.1"}
			}
		}
	}
	return spec, nil
}

func (p Profile) connection(conn net.Conn, host string) (*utls.UConn, error) {
	roots, err := rootsForHost(host)
	if err != nil {
		return nil, err
	}
	return p.connectionWithRoots(conn, host, roots)
}

func (p Profile) connectionWithRoots(conn net.Conn, host string, roots *x509.CertPool) (*utls.UConn, error) {
	spec, err := p.clientHelloSpec()
	if err != nil {
		return nil, err
	}
	for _, extension := range spec.Extensions {
		if sni, ok := extension.(*utls.SNIExtension); ok {
			sni.ServerName = host
		}
	}
	config := &utls.Config{ServerName: host, MinVersion: spec.TLSVersMin, RootCAs: roots}
	uconn := utls.UClient(conn, config, utls.HelloCustom)
	if err := uconn.ApplyPreset(&spec); err != nil {
		return nil, fmt.Errorf("httpx apply ClientHello: %w", err)
	}
	return uconn, nil
}
