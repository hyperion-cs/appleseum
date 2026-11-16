package applebackemu

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// BagJson includes an unknown setting to test forward-compatible parsing.
const (
	BagJson = `{
 "authenticateAccount":"https://auth.itunes.apple.com/auth/v1/native",
 "sign-sap-setup":"https://fpinit.itunes.apple.com/v1/signSapSetup/legacy",
 "sign-sap-setup-cert":"https://s.mzstatic.com/sap/setupCert.plist",
 "amd-finish-provisioning":"https://auth.itunes.apple.com/auth/v1/otp/1.5/finish",
 "amd-sync-machine":"https://auth.itunes.apple.com/auth/v1/otp/1.5/sync",
 "md-finish-provisioning":"https://{p$itspod$-}buy.itunes.apple.com/finish",
 "amd-domains":["auth.itunes.apple.com","*-buy.itunes.apple.com","*.g.apple.com"],
 "sign-sap-request":{"auth/v1":["native"],"auth/v1/native":["fast"]},
 "volumeStoreDownloadProduct":"https://p42-buy.itunes.apple.com/WebObjects/MZBuy.woa/wa/volumeStoreDownloadProduct",
 "unknown-server-setting":true
}`
)

const (
	Configurator BagActor = iota
	Accounts
)

type BagActor uint8

type BagOptions struct {
	Actor            BagActor
	Storefront       string
	Language         string
	UtcOffsetSeconds int
}

// ServiceBag checks the macOS 26.5 bag request and returns gzip JSON with a secure cookie.
func ServiceBag(options BagOptions) (Step, error) {
	product, version, profile, application := "Configurator", "2.21", "Accounts", "com.apple.configurator.ui"
	agent := "Configurator/2.21 Configurator/2.21"
	switch options.Actor {
	case Configurator:
	case Accounts:
		product, version, profile, application = "com.apple.accountsd", "113", "AppStore", "com.apple.accountsd"
		agent = "com.apple.accountsd/113"
	default:
		return Step{}, fmt.Errorf("applebackemu: unknown bag actor %d", options.Actor)
	}
	query := url.Values{
		"deviceClass": {"Macintosh"}, "format": {"json"}, "os": {"OS X"},
		"osVersion": {"26.5"}, "product": {product}, "productVersion": {version},
		"profile": {profile}, "profileVersion": {"1"}, "storefront": {options.Storefront},
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(BagJson)); err != nil {
		return Step{}, err
	}
	if err := writer.Close(); err != nil {
		return Step{}, err
	}
	return Step{
		Name: product + " service bag", Method: http.MethodGet,
		Url: "https://bag.itunes.apple.com/bag.xml?" + query.Encode(),
		Header: http.Header{
			"User-Agent": {agent + " (Macintosh; OS X 26.5; 25F71) AppleWebKit/1624.2.5.11.4 AMS/1 (dt:1)"},
			"Accept":     {"*/*"}, "Accept-Encoding": {"br, gzip, deflate"},
			"Accept-Language": {options.Language}, "Priority": {"u=3, i"},
			"X-Apple-Client-Application": {application}, "X-Apple-Store-Front": {options.Storefront},
			"X-Apple-Tz": {strconv.Itoa(options.UtcOffsetSeconds)}, "Cache-Control": {"no-cache"},
			"If-Modified-Since": nil, "If-None-Match": nil,
		},
		Body: []byte{},
		Response: Response{
			Header: http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}, "Set-Cookie": {"bag-test=synthetic; Path=/; Secure"}},
			Body:   compressed.Bytes(),
		},
	}, nil
}
