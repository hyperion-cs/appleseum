package applebackemu

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"howett.net/plist"
)

func TestGrandSlamRequestValidation(t *testing.T) {
	for _, operation := range []string{"init", "complete"} {
		t.Run(operation, func(t *testing.T) {
			for _, mutation := range []string{"valid", "identity", "CPD", "proof", "extra-field", "envelope"} {
				t.Run(mutation, func(t *testing.T) {
					device := AuthDevice{ClientInfo: "Synthetic client", Language: "en", ClientData: map[string]any{"at": uint64(2), "bootstrap": true}}
					step, err := GrandSlamRequest(device, "person@example.test", operation, nil, Response{})
					if err != nil {
						t.Fatal(err)
					}
					body := map[string]any{"u": "person@example.test", "o": operation, "cpd": map[string]any{"at": uint64(2), "bootstrap": true}}
					if operation == "init" {
						body["A2k"], body["ps"] = []byte{2}, []string{"s2k", "s2k_fo"}
					} else {
						body["M1"], body["c"] = make([]byte, 32), "synthetic-context"
					}
					envelope := map[string]any{"Header": map[string]any{"Version": "1.0.1"}, "Request": body}
					switch mutation {
					case "identity":
						body["u"] = "other@example.test"
					case "CPD":
						body["cpd"] = map[string]any{"at": "2", "bootstrap": true}
					case "proof":
						if operation == "init" {
							body["A2k"] = "not-data"
						} else {
							body["M1"] = make([]byte, 31)
						}
					case "extra-field":
						body["password"] = "must-not-be-sent"
					case "envelope":
						envelope["Header"] = map[string]any{"Version": "2"}
					}
					raw, err := plist.Marshal(envelope, plist.XMLFormat)
					if err != nil {
						t.Fatal(err)
					}
					s := newFixture(t, InMemory, step)
					r, _ := http.NewRequest("POST", grandSlamUrl, bytes.NewReader(raw))
					r.Header = step.Header.Clone()
					response, err := s.Client().Do(r)
					if response != nil {
						response.Body.Close()
					}
					if mutation == "valid" {
						if err != nil || s.Verify() != nil {
							t.Fatal("valid request rejected", err)
						}
					} else if err == nil || s.Verify() == nil {
						t.Fatal("invalid request accepted")
					}
				})
			}
		})
	}
}

func TestTrustedDeviceVerificationRequiresDeliveryCookie(t *testing.T) {
	steps, err := TrustedDevice(TrustedDeviceOptions{Device: AuthDevice{}, Adsid: "synthetic-adsid", Idms: "synthetic-idms", Code: "123456"})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise just the cookie-bound confirmation step with a fresh, empty jar.
	s := newFixture(t, InMemory, steps[1])
	r, _ := http.NewRequest("GET", steps[1].Url, nil)
	r.Header = steps[1].Header.Clone()
	_, err = s.Client().Do(r)
	if err == nil || !strings.Contains(err.Error(), "synthetic-factor cookie") || s.Verify() == nil {
		t.Fatal("confirmation accepted without delivery cookie", err)
	}
}

func TestRespondFailureDoesNotAdvanceOrMutateJournal(t *testing.T) {
	cause := errors.New("synthetic responder failure")
	calls := 0
	s := newFixture(t, InMemory, Step{Method: "POST", Url: "https://gsa.apple.com/test", Respond: func(request Request) (Response, error) {
		calls++
		request.Body[0] = '!'
		request.Header.Set("X-Fixture", "changed")
		if calls == 1 {
			return Response{}, cause
		}
		return Response{Body: []byte("done")}, nil
	}})
	for range 2 {
		r, _ := http.NewRequest("POST", "https://gsa.apple.com/test", strings.NewReader("original"))
		r.Header.Set("X-Fixture", "original")
		response, _ := s.Client().Do(r)
		if response != nil {
			response.Body.Close()
		}
	}
	if err := s.Verify(); !errors.Is(err, cause) || strings.Contains(err.Error(), "consumed") {
		t.Fatal("responder failure was lost or advanced the scenario", err)
	}
	for _, request := range s.Requests() {
		if string(request.Body) != "original" || request.Header.Get("X-Fixture") != "original" {
			t.Fatal("callback mutated journal")
		}
	}
}

func TestServerNegotiationAgainstIndependentVector(t *testing.T) {
	// Use the independent OpenSSL-generated fixture.
	raw, err := os.ReadFile("../grandslam/testdata/negotiation.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Vectors []struct{ Name, Key, Protocol, Encrypted, Proof string }
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	for _, vector := range file.Vectors {
		if vector.Name != "absent-sc" {
			continue
		}

		decode := func(value string) []byte {
			result, err := hex.DecodeString(value)
			if err != nil {
				t.Fatal(err)
			}
			return result
		}

		if got := serverNegotiationProof(decode(vector.Key), vector.Protocol, decode(vector.Encrypted)); !bytes.Equal(got, decode(vector.Proof)) {
			t.Fatal("server negotiation proof differs from independent oracle")
		}
		return
	}
	t.Fatal("missing independent oracle")
}
