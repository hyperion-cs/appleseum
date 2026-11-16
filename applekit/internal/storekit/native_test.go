package storekit

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
)

func TestSessionFromNativeBridgesIntoDownloadState(t *testing.T) {
	client := NewSessionHttpClient()
	cookieUrl, _ := url.Parse("https://auth.itunes.apple.com/auth/v1/native")
	client.Jar.SetCookies(cookieUrl, []*http.Cookie{{Name: "session", Value: "synthetic", Path: "/"}})
	credentials := ams.NativeCredentials{
		AppleId: "person@example.invalid", DirectoryServicesId: "123456789",
		Guid: "001122AABBCC",
	}
	native := ams.NativeSession{
		PasswordToken: "synthetic-password-token", DirectoryServicesId: "123456789",
		Storefront: "143517-2,34", Pod: "42",
	}
	endpoints := &ams.Bag{
		DownloadURL:   "https://p42-buy.itunes.apple.com/WebObjects/MZBuy.woa/wa/volumeStoreDownloadProduct",
		RedownloadURL: "https://p42-buy.itunes.apple.com/WebObjects/MZBuy.woa/wa/redownloadProduct",
	}
	session, err := SessionFromNative(client, credentials, native, endpoints)
	if err != nil {
		t.Fatal(err)
	}
	if session.Client != client || session.Account.PasswordToken != native.PasswordToken || session.Account.Guid != credentials.Guid || session.amd.Bag.DownloadURL != endpoints.DownloadURL {
		t.Fatal("native result was not mapped into the existing session")
	}
	if encoded, err := session.Export(); err != nil || len(encoded) == 0 {
		t.Fatalf("bridged native session is not persistable: bytes=%d err=%v", len(encoded), err)
	}
}

func TestSessionFromNativeRejectsSplitOrMismatchedState(t *testing.T) {
	credentials := ams.NativeCredentials{AppleId: "person@example.invalid", DirectoryServicesId: "1", Guid: "001122AABBCC"}
	native := ams.NativeSession{PasswordToken: "token", DirectoryServicesId: "2", Storefront: "143517"}
	endpoints := &ams.Bag{DownloadURL: "https://buy.itunes.apple.com/download"}
	if _, err := SessionFromNative(NewSessionHttpClient(), credentials, native, endpoints); err == nil {
		t.Fatal("mismatched DSID was accepted")
	}
	client := &http.Client{Jar: newJar()}
	native.DirectoryServicesId = credentials.DirectoryServicesId
	if _, err := SessionFromNative(client, credentials, native, endpoints); err != nil {
		t.Fatalf("persistent shared jar was rejected: %v", err)
	}
	client.Jar, _ = cookiejar.New(nil)
	if _, err := SessionFromNative(client, credentials, native, endpoints); err == nil {
		t.Fatal("client without the shared persistent jar was accepted")
	}
}
