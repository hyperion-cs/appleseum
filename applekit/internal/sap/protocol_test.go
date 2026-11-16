package sap

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
	"github.com/hyperion-cs/appleseum/applekit/internal/sapkey"
)

func sapBackend(t *testing.T, steps ...applebackemu.Step) *applebackemu.Server {
	t.Helper()
	server, err := applebackemu.New(applebackemu.InMemory, steps...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		server.Close()
		if err := server.Verify(); err != nil {
			t.Error(err)
		}
	})
	return server
}

func TestSetupActorHeadersAndCookieJar(t *testing.T) {
	ctx := httpx.WithProfile(context.Background(), httpx.Accounts)
	headers := http.Header{
		"Accept-Encoding":            {"br, gzip, deflate"},
		"Accept-Language":            {"en-RU"},
		"Priority":                   {"u=3, i"},
		"X-Apple-Client-Application": {"com.apple.accountsd"},
		"X-Apple-Store-Front":        {"143469-2,42"},
		"X-Apple-Tz":                 {"10800"},
	}
	expected := headers.Clone()
	expected.Set("User-Agent", "com.apple.accountsd/113 (Macintosh; OS X 26.5; 25F71) AppleWebKit/1624.2.5.11.4 AMS/1 (dt:1)")
	expected.Set("Accept", "*/*")
	certificate, err := applebackemu.PlistResponse(map[string]any{"sign-sap-setup-cert": []byte("synthetic reply")})
	if err != nil {
		t.Fatal(err)
	}
	certificate.Header.Set("Set-Cookie", "sap-session=synthetic; Path=/; Secure")
	certificate, err = applebackemu.GzipResponse(certificate)
	if err != nil {
		t.Fatal(err)
	}
	start, err := applebackemu.PlistRequest("https://fpinit.itunes.apple.com/cert", nil, expected, certificate)
	if err != nil {
		t.Fatal(err)
	}
	expected.Set("Cookie", "sap-session=synthetic")
	expected.Set("Content-Type", "application/x-apple-plist")
	reply, err := applebackemu.PlistResponse(map[string]any{"sign-sap-setup-buffer": []byte("synthetic reply")})
	if err != nil {
		t.Fatal(err)
	}
	reply, err = applebackemu.GzipResponse(reply)
	if err != nil {
		t.Fatal(err)
	}
	finish, err := applebackemu.PlistRequest("https://fpinit.itunes.apple.com/setup", map[string]any{"sign-sap-setup-buffer": []byte("client buffer")}, expected, reply)
	if err != nil {
		t.Fatal(err)
	}
	backend := sapBackend(t, start, finish)
	client := backend.Client()
	protocol := setupProtocol{client: client, headers: headers}
	if _, err := protocol.certificate(ctx, "https://fpinit.itunes.apple.com/cert"); err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.exchange(ctx, "https://fpinit.itunes.apple.com/setup", []byte("client buffer")); err != nil {
		t.Fatal(err)
	}
	if len(backend.Requests()) != 2 || len(headers) != 6 {
		t.Fatal("request metadata was mutated")
	}
	if headers.Get("User-Agent") != "" || headers.Get("Content-Type") != "" {
		t.Fatal("headers leaked between requests")
	}
}

func TestClosedSigner(t *testing.T) {
	signer := new(pureSigner)
	if err := signer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := signer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := signer.Sign(nil); !errors.Is(err, errSignerClosed) {
		t.Fatalf("closed signer: %v", err)
	}
}

func TestSignerFreshNoncesWithinSession(t *testing.T) {
	// Synthetic Configurator state with an explicit zero transport key.
	var state sapkey.ConfiguratorRequestState
	key := make([]byte, 16)
	plain := make([]byte, 1_248)
	for _, offset := range []int{0, 20, 40} {
		binary.BigEndian.PutUint32(plain[offset:], 16)
	}
	reply := make([]byte, 21+len(plain)+159)
	reply[0] = 2
	binary.BigEndian.PutUint32(reply[17:], uint32(len(plain)))
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	cipher.NewCBCEncrypter(block, reply[1:17]).CryptBlocks(reply[21:21+len(plain)], plain)
	prepared, err := sapkey.NewConfiguratorSession(state, reply)
	if err != nil {
		t.Fatal(err)
	}
	envelope := bytes.Clone(prepared.Sign([16]byte{}, nil)[33:])
	signer := &pureSigner{session: prepared}
	defer signer.Close()
	var group sync.WaitGroup
	signatures := make(chan []byte, 32)
	for i := 0; i < cap(signatures); i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			signature, err := signer.Sign([]byte("the same synthetic action"))
			if err != nil {
				t.Error(err)
				return
			}
			signatures <- signature
		}()
	}
	group.Wait()
	close(signatures)
	seen := make(map[string]bool)
	for signature := range signatures {
		if len(signature) != 501 || signature[0] != 2 {
			t.Fatal("invalid signature framing")
		}
		nonce := string(signature[1:17])
		if seen[nonce] {
			t.Fatal("signer reused a nonce for another action")
		}
		seen[nonce] = true
		if !bytes.Equal(signature[33:], envelope) {
			t.Fatal("session envelope changed between actions")
		}
	}
	if len(seen) != 32 {
		t.Fatal("missing signatures")
	}
}

func TestSapHttpStatusSurvivesErrorWrapping(t *testing.T) {
	backend := sapBackend(t, applebackemu.Step{Method: "GET", Url: "https://fpinit.itunes.apple.com/cert", Response: applebackemu.Response{StatusCode: 503, Body: []byte("unavailable")}})
	protocol := setupProtocol{client: backend.Client()}
	_, err := protocol.certificate(context.Background(), "https://fpinit.itunes.apple.com/cert")
	var upstream *httpx.HttpError
	if !errors.As(err, &upstream) || upstream.StatusCode != 503 {
		t.Fatal("lost upstream HTTP classification", err)
	}
}
