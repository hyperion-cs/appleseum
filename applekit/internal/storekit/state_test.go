package storekit

import (
	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func TestTypedSessionKeepsTransportAndForgetsCredentials(t *testing.T) {
	client := NewSessionHttpClient()
	u, _ := url.Parse("https://buy.itunes.apple.com/auth/login")
	client.Jar.SetCookies(u, []*http.Cookie{
		{Name: "token", Value: "parent", Domain: ".itunes.apple.com", Path: "/", Secure: true},
		{Name: "token", Value: "host", Path: "/auth", HttpOnly: true},
		{Name: "deleted", Value: "old", Path: "/"},
	})
	client.Jar.SetCookies(u, []*http.Cookie{{Name: "deleted", MaxAge: -1, Path: "/"}})
	account := Account{DirectoryServicesId: "123", Guid: "001122334455", PasswordToken: "synthetic-token", StoreFront: "143441"}
	s := &Session{Account: account, Client: client, amd: ams.AmdController{Bag: ams.Bag{DownloadURL: "https://buy.itunes.apple.com/download"}}, authenticatedAt: time.Unix(1_700_000_000, 0)}
	parameters := s.Parameters()
	secrets, err := s.Secrets()
	if err != nil {
		t.Fatal(err)
	}
	original := client.Jar.Cookies(u)
	transport := client.Transport
	s.ForgetSecrets()
	if s.Account.PasswordToken != "" || len(client.Jar.Cookies(u)) != 0 || client.Transport != transport {
		t.Fatal("locking the session retained secrets or discarded its transport")
	}
	for range 3 {
		s, err = OpenSession(client, account, parameters, secrets)
		if err != nil {
			t.Fatal(err)
		}
		if s.Client.Transport != transport || !reflect.DeepEqual(s.Client.Jar.Cookies(u), original) || s.Account.PasswordToken != "synthetic-token" {
			t.Fatal("reopening changed cookie scope, precedence, credentials or transport")
		}
		secrets, err = s.Secrets()
		if err != nil {
			t.Fatal(err)
		}
		client = s.Client
		s.ForgetSecrets()
	}
}
