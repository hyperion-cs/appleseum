package grandslam

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"strings"
	"testing"

	"howett.net/plist"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

func testCpd() map[string]any {
	return map[string]any{
		"X-Apple-I-MD": "synthetic-otp", "X-Apple-I-MD-M": "synthetic-machine-id",
		"X-Apple-I-MD-LU": "synthetic-local-user", "X-Apple-I-MD-RINFO": "17106176",
		"X-Mme-Device-Id": "00000000-0000-4000-8000-000000000001",
		"svct":            "itunesstore", "at": uint64(2), "capp": "Apple Configurator", "cbid": "com.apple.configurator.ui",
		"bootstrap": true, "X-Apple-I-Client-Time": "2026-10-27T10:00:00Z",
		"x-apple-baa-cert": "synthetic-baa-kept-verbatim",
	}
}

func authDevice(cpd map[string]any) applebackemu.AuthDevice {
	return applebackemu.AuthDevice{
		ClientInfo: "<Synthetic Mac> <macOS;26.5;25F71> <com.apple.AuthKit/1 (com.apple.akd/1.0)>",
		Language:   "en-GB,en;q=0.9", ClientData: cpd,
	}
}

func serviceFixture(t *testing.T, mode applebackemu.Mode, steps ...applebackemu.Step) (Service, *applebackemu.Server) {
	t.Helper()
	fixture, err := applebackemu.New(mode, steps...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		fixture.Close()
		if err := fixture.Verify(); err != nil {
			t.Error(err)
		}
	})
	device := authDevice(testCpd())
	return Service{Http: fixture.Client(), Metadata: Metadata{device.ClientInfo, device.Language}}, fixture
}

func testService(t *testing.T, steps ...applebackemu.Step) Service {
	t.Helper()
	service, _ := serviceFixture(t, applebackemu.InMemory, steps...)
	return service
}

func plistBody(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	body, err := plist.Marshal(map[string]any{"Response": fields}, plist.XMLFormat)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func serviceStep(t *testing.T, username, operation string, fields map[string]any, response applebackemu.Response) applebackemu.Step {
	t.Helper()
	step, err := applebackemu.GrandSlamRequest(authDevice(testCpd()), username, operation, fields, response)
	if err != nil {
		t.Fatal(err)
	}
	return step
}

// Frozen independent proofs remain separate from the dynamic server. They catch
// accidental agreement between client and emulator implementations.
func referenceExchange(t *testing.T) []applebackemu.Step {
	t.Helper()
	v := vectors(t)[0]
	encrypted, _ := sessionFixture(t)
	reply := plistBody(t, map[string]any{"Status": map[string]any{"ec": 0}, "s": unhex(t, v.Salt), "B": unhex(t, v.ServerPublic), "i": v.Iterations, "sp": v.Protocol, "c": "synthetic-exchange"})
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(reply); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	start := serviceStep(t, v.Username, "init", map[string]any{"A2k": unhex(t, v.Public)}, applebackemu.Response{
		Header: map[string][]string{"Content-Encoding": {"gzip"}, "Set-Cookie": {"gsa-synthetic=one; Secure; Path=/"}}, Body: compressed.Bytes(),
	})
	finish := serviceStep(t, v.Username, "complete", map[string]any{"M1": unhex(t, v.Proof), "c": "synthetic-exchange"}, applebackemu.Response{Body: plistBody(t, map[string]any{
		"Status": map[string]any{"ec": 0, "au": "secondaryAuth"}, "M2": unhex(t, v.ServerProof), "spd": encrypted, "np": unhex(t, negotiationVectors(t)[0].Proof),
	})})
	finish.Header.Set("Cookie", "gsa-synthetic=one")
	return []applebackemu.Step{start, finish}
}

func TestServiceSrpExchangeAgainstIndependentVector(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode applebackemu.Mode
	}{{"memory", applebackemu.InMemory}, {"https", applebackemu.HTTPS}} {
		t.Run(tc.name, func(t *testing.T) {
			v := vectors(t)[0]
			c, err := newClient(v.Username, v.Password, unhex(t, v.Private))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			s, fixture := serviceFixture(t, tc.mode, referenceExchange(t)...)
			cpd := testCpd()
			start, err := s.Init(context.Background(), InitRequest{v.Username, c.Public(), cpd})
			if err != nil {
				t.Fatal(err)
			}
			proof, err := c.Challenge(start.Challenge)
			if err != nil {
				t.Fatal(err)
			}
			finish, err := s.Complete(context.Background(), CompleteRequest{v.Username, start.Context, proof, cpd})
			if err != nil {
				t.Fatal(err)
			}
			if finish.Status.Auth != "secondaryAuth" || len(finish.NegotiationProof) != 32 {
				t.Fatal("auth step or negotiation proof discarded")
			}
			data, err := c.FinishVerified(finish.ServerProof, finish.EncryptedSession, finish.NegotiationProof, finish.ServerContext)
			if err != nil {
				t.Fatal(err)
			}
			if token, err := data.PasswordEquivalentToken(); err != nil || token != "synthetic-pet-value=PET" {
				t.Fatal("independent session lost", err)
			}
			if cpd["o"] != nil || cpd["M1"] != nil {
				t.Fatal("mutated caller CPD")
			}
			for _, request := range fixture.Requests() {
				if bytes.Contains(request.Body, []byte(v.Password)) {
					t.Fatal("password sent over the wire")
				}
				if tc.mode == applebackemu.HTTPS && (request.ServerName != "gsa.apple.com" || request.Protocol != "HTTP/2.0") {
					t.Fatal("AuthKit TLS routing lost")
				}
			}
		})
	}
}

func TestAuthenticateAgainstBackendEmulator(t *testing.T) {
	for _, protocol := range []string{"s2k", "s2k_fo"} {
		for _, wrongPassword := range []bool{false, true} {
			t.Run(protocol+map[bool]string{false: "/success", true: "/wrong-password"}[wrongPassword], func(t *testing.T) {
				account := applebackemu.AuthAccount{AppleId: "person@example.test", Password: "synthetic-password", Adsid: "synthetic-adsid", DirectoryServicesId: "123", Idms: "synthetic-idms", Pet: "synthetic-pet=PET"}
				steps, err := applebackemu.GrandSlam(applebackemu.GrandSlamOptions{Account: account, Device: authDevice(testCpd()), Protocol: protocol})
				if err != nil {
					t.Fatal(err)
				}
				s := testService(t, steps...)
				password := account.Password
				if wrongPassword {
					password = "wrong-password"
				}
				data, err := s.Authenticate(context.Background(), account.AppleId, password, testCpd())
				if wrongPassword {
					if !errors.Is(err, ErrPasswordRejected) {
						t.Fatal("password proof not rejected", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if token, err := data.PasswordEquivalentToken(); err != nil || token != account.Pet || data.DirectoryServicesId != account.DirectoryServicesId {
					t.Fatal("verified session lost", err)
				}
			})
		}
	}
}

func TestServicePreservesInnerErrorUnderHttp200(t *testing.T) {
	for _, code := range []int64{-80_045, -22_421, -20_101} {
		for _, httpStatus := range []int{200, 500} {
			s := testService(t, serviceStep(t, "probe@example.invalid", "init", nil, applebackemu.Response{StatusCode: httpStatus, Body: plistBody(t, map[string]any{"Status": map[string]any{
				"ec": code, "hsc": 434, "em": "private-server-text", "X-Apple-I-MD-DATA": "private-sim", "X-Apple-I-MD-Cmd-Target": "native",
			}})}))
			_, err := s.Init(context.Background(), InitRequest{"probe@example.invalid", []byte{2}, testCpd()})
			var result *StatusError
			if !errors.As(err, &result) || result.Code != code || result.HttpStatus != httpStatus || result.ServerStatus != 434 || result.DeviceData != "private-sim" || result.DeviceTarget != "native" || result.Message != "private-server-text" {
				t.Fatalf("lost structured error: %v", err)
			}
			if strings.Contains(err.Error(), "private-") {
				t.Fatal("private server fields leaked")
			}
		}
	}
}

func TestServiceAccountDetailsRejectedBeforePasswordProof(t *testing.T) {
	for _, code := range []int64{-20_970, -20_101, -99_999} {
		s := testService(t, serviceStep(t, "+12025550123", "init", nil, applebackemu.Response{Body: plistBody(t, map[string]any{"Status": map[string]any{"ec": code, "hsc": 200, "em": "Your account information was entered incorrectly."}})}))
		_, err := s.Authenticate(context.Background(), "+12025550123", "synthetic-password", testCpd())
		var result *StatusError
		if !errors.As(err, &result) || result.Code != code || result.HttpStatus != 200 {
			t.Fatal("account rejection lost", err)
		}
		if errors.Is(err, ErrCredentialsRejected) != (code != -99_999) {
			t.Fatal("classification must use result code", err)
		}
	}
}

func TestServiceRejectsMissingStatusAndInvalidChallenges(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(r map[string]any) { delete(r, "Status") }, func(r map[string]any) { r["Status"] = map[string]any{} },
		func(r map[string]any) { r["Status"] = map[string]any{"ec": "0"} }, func(r map[string]any) { r["Status"] = map[string]any{"ec": uint64(1) << 63} },
		func(r map[string]any) { r["Status"] = map[string]any{"ec": 0, "hsc": -1} }, func(r map[string]any) { r["Status"] = map[string]any{"ec": 0, "au": []string{"unknown"}} },
		func(r map[string]any) { r["i"] = 1_000_001 }, func(r map[string]any) { r["i"] = "1000" }, func(r map[string]any) { r["B"] = make([]byte, 257) },
		func(r map[string]any) { r["s"] = []byte{} }, func(r map[string]any) { r["sp"] = "unknown" }, func(r map[string]any) { r["c"] = "" },
	} {
		fields := map[string]any{"Status": map[string]any{"ec": 0}, "s": []byte{1}, "B": []byte{2}, "i": 1_000, "sp": "s2k", "c": "context"}
		change(fields)
		s := testService(t, serviceStep(t, "probe@example.invalid", "init", nil, applebackemu.Response{Body: plistBody(t, fields)}))
		if _, err := s.Init(context.Background(), InitRequest{"probe@example.invalid", []byte{2}, testCpd()}); err == nil {
			t.Fatal("unsafe or incomplete challenge accepted")
		}
	}
}

func TestServiceRejectsTransportFailuresAndRedirects(t *testing.T) {
	for _, code := range []int{302, 403, 429, 503} {
		s := testService(t, serviceStep(t, "probe@example.invalid", "init", nil, applebackemu.Response{StatusCode: code, Header: map[string][]string{"Location": {"https://unrelated.invalid/credential-sink"}}, Body: []byte("not a plist")}))
		_, err := s.Init(context.Background(), InitRequest{"probe@example.invalid", []byte{2}, testCpd()})
		var httpErr *HttpError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != code || s.Http.CheckRedirect != nil {
			t.Fatalf("HTTP failure or client mutation: %v", err)
		}
	}
}

func TestServiceRejectsIncompleteDeviceBeforeSending(t *testing.T) {
	s := testService(t)
	for _, key := range []string{"X-Apple-I-MD", "X-Apple-I-MD-M", "X-Apple-I-MD-LU", "X-Apple-I-MD-RINFO", "X-Mme-Device-Id", "svct", "capp", "cbid"} {
		cpd := testCpd()
		delete(cpd, key)
		if _, err := s.Init(context.Background(), InitRequest{"probe@example.invalid", []byte{2}, cpd}); err == nil {
			t.Fatalf("missing %s accepted", key)
		}
	}
	cpd := testCpd()
	cpd["X-Mme-Device-Id"] = "id\r\nInjected: value"
	if _, err := s.Init(context.Background(), InitRequest{"probe@example.invalid", []byte{2}, cpd}); err == nil {
		t.Fatal("header injection accepted")
	}
}

func TestServiceRejectsMalformedCompleteAndOversizedBodies(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(r map[string]any) { r["M2"] = make([]byte, 31) }, func(r map[string]any) { r["spd"] = make([]byte, 15) }, func(r map[string]any) { delete(r, "spd") },
		func(r map[string]any) { r["np"] = "not-plist-data" }, func(r map[string]any) { r["np"] = []byte{} }, func(r map[string]any) { r["sc"] = "not-plist-data" },
	} {
		fields := map[string]any{"Status": map[string]any{"ec": 0}, "M2": make([]byte, 32), "spd": make([]byte, 16)}
		change(fields)
		s := testService(t, serviceStep(t, "probe@example.invalid", "complete", nil, applebackemu.Response{Body: plistBody(t, fields)}))
		if _, err := s.Complete(context.Background(), CompleteRequest{"probe@example.invalid", "context", make([]byte, 32), testCpd()}); err == nil {
			t.Fatal("malformed complete response accepted")
		}
	}
	s := testService(t, serviceStep(t, "probe@example.invalid", "init", nil, applebackemu.Response{Body: bytes.Repeat([]byte{' '}, maxServiceBody+1)}))
	if _, err := s.Init(context.Background(), InitRequest{"probe@example.invalid", []byte{2}, testCpd()}); err == nil {
		t.Fatal("oversized response accepted")
	}
	cpd := testCpd()
	cpd["large"] = strings.Repeat("x", maxServiceBody)
	if _, err := s.Init(context.Background(), InitRequest{"probe@example.invalid", []byte{2}, cpd}); err == nil {
		t.Fatal("oversized request sent")
	}
}

func TestServicePreservesPresentEmptyServerContext(t *testing.T) {
	v := vectors(t)[0]
	np := negotiationVectors(t)[1]
	s := testService(t, serviceStep(t, v.Username, "complete", nil, applebackemu.Response{Body: plistBody(t, map[string]any{
		"Status": map[string]any{"ec": 0}, "M2": unhex(t, v.ServerProof), "spd": unhex(t, np.Encrypted), "np": unhex(t, np.Proof), "sc": []byte{},
	})}))
	reply, err := s.Complete(context.Background(), CompleteRequest{v.Username, "context", make([]byte, 32), testCpd()})
	if err != nil || reply.ServerContext == nil || len(reply.ServerContext) != 0 {
		t.Fatalf("empty sc lost: %v", err)
	}
	c := prepare(t, v)
	defer c.Close()
	if _, err := c.FinishVerified(reply.ServerProof, reply.EncryptedSession, reply.NegotiationProof, reply.ServerContext); err != nil {
		t.Fatal(err)
	}
}

func TestAuthenticateWithClientComposesVerifiedExchange(t *testing.T) {
	v := vectors(t)[0]
	client, err := newClient(v.Username, v.Password, unhex(t, v.Private))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	service := testService(t, referenceExchange(t)...)
	data, err := service.authenticateWithClient(context.Background(), v.Username, client, testCpd())
	if err != nil {
		t.Fatal(err)
	}
	if token, err := data.PasswordEquivalentToken(); err != nil || token != "synthetic-pet-value=PET" {
		t.Fatalf("verified PET lost: %v", err)
	}
}
