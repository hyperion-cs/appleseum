package applebackemu

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"howett.net/plist"
)

func TestDownloadProductValidation(t *testing.T) {
	for _, mutation := range []string{"valid", "query", "ID type", "version", "authorization type", "keybag", "serial", "extra field"} {
		t.Run(mutation, func(t *testing.T) {
			step, err := DownloadProduct(DownloadOptions{
				Endpoint: "https://buy.itunes.apple.com/download?route=fixture", Guid: "001122AABBCC", Dsid: "123", Token: "synthetic-token", Storefront: "143517-2,34",
				AppId: 123, Version: "7", Keybag: "synthetic-keybag", Serial: "AQIDBAU=", Authorized: true,
			}, Response{})
			if err != nil {
				t.Fatal(err)
			}
			body := map[string]any{"creditDisplay": "", "guid": "001122AABBCC", "kbsync": "synthetic-keybag", "salableAdamId": "123", "serialNumber": "AQIDBAU=", "externalVersionId": "7", "hasBeenAuthedForBuy": "true"}
			endpoint := "https://buy.itunes.apple.com/download?guid=001122AABBCC&route=fixture"
			switch mutation {
			case "query":
				endpoint = "https://buy.itunes.apple.com/download?guid=001122AABBCC"
			case "ID type":
				body["salableAdamId"] = 123
			case "version":
				body["externalVersionId"] = "6"
			case "authorization type":
				body["hasBeenAuthedForBuy"] = true
			case "keybag":
				body["kbsync"] = "other-keybag"
			case "serial":
				body["serialNumber"] = "invalid"
			case "extra field":
				body["unexpected"] = true
			}
			raw, err := plist.Marshal(body, plist.XMLFormat)
			if err != nil {
				t.Fatal(err)
			}
			s := newFixture(t, InMemory, step)
			r, _ := http.NewRequest("POST", endpoint, bytes.NewReader(raw))
			r.Header = step.Header.Clone()
			response, err := s.Client().Do(r)
			if response != nil {
				response.Body.Close()
			}
			if mutation == "valid" {
				if err != nil || s.Verify() != nil {
					t.Fatal("valid dispatch rejected", err)
				}
			} else if err == nil || s.Verify() == nil {
				t.Fatal("invalid dispatch accepted")
			}
		})
	}
}

type trackedStream struct {
	io.Reader
	closed *atomic.Int32
}

func (s *trackedStream) Close() error { s.closed.Add(1); return nil }

func TestResponseStreamsHaveFreshBodiesAndLengths(t *testing.T) {
	eachMode(t, func(t *testing.T, mode Mode) {
		for _, kind := range []string{"known", "unknown", "empty"} {
			t.Run(kind, func(t *testing.T) {
				data := "synthetic archive"
				if kind == "empty" {
					data = ""
				}
				var opened, closed atomic.Int32
				response := Response{OpenBody: func(context.Context) io.ReadCloser {
					opened.Add(1)
					return &trackedStream{strings.NewReader(data), &closed}
				}}
				length := int64(len(data))
				if kind != "unknown" {
					response.ContentLength = &length
				} else {
					length = -1
				}
				step := Step{Method: "GET", Url: "https://download.itunes.apple.com/asset", Response: response}
				s := newFixture(t, mode, step, step)
				for range 2 {
					reply, err := s.Client().Get(step.Url)
					if err != nil {
						t.Fatal(err)
					}
					got, err := io.ReadAll(reply.Body)
					reply.Body.Close()
					if err != nil || string(got) != data || reply.ContentLength != length {
						t.Fatal("stream body or length differs", reply.ContentLength, err)
					}
				}
				s.Close()
				if opened.Load() != 2 || closed.Load() != 2 || s.Verify() != nil {
					t.Fatal("response streams were shared or not closed")
				}
			})
		}
	})
}

type interruptedStream struct{ io.Reader }

func (s interruptedStream) Read(p []byte) (int, error) {
	n, err := s.Reader.Read(p)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func TestResponseStreamErrorsDoNotCompleteTheTransfer(t *testing.T) {
	eachMode(t, func(t *testing.T, mode Mode) {
		const full = "prefix-and-rest"
		length := int64(len(full))
		var closed atomic.Int32
		s := newFixture(t, mode, Step{Method: "GET", Url: "https://download.itunes.apple.com/asset", Response: Response{
			ContentLength: &length, OpenBody: func(context.Context) io.ReadCloser {
				return &trackedStream{interruptedStream{strings.NewReader("prefix")}, &closed}
			},
		}})
		reply, err := s.Client().Get("https://download.itunes.apple.com/asset")
		if err != nil {
			t.Fatal("stream failed before response headers", err)
		}
		got, err := io.ReadAll(reply.Body)
		reply.Body.Close()
		if err == nil || len(got) >= len(full) || !bytes.HasPrefix([]byte(full), got) {
			t.Fatal("broken stream was accepted", string(got), err)
		}
		if mode == InMemory && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal("stream error was lost", err)
		}
		s.Close()
		if closed.Load() != 1 || s.Verify() != nil {
			t.Fatal("broken stream was not closed or request was not consumed")
		}
	})
}

func TestInvalidResponseStreamFailsTheScenario(t *testing.T) {
	eachMode(t, func(t *testing.T, mode Mode) {
		s := newFixture(t, mode, Step{Method: "GET", Url: "https://download.itunes.apple.com/asset", Response: Response{OpenBody: func(context.Context) io.ReadCloser { return nil }}})
		reply, err := s.Client().Get("https://download.itunes.apple.com/asset")
		if reply != nil {
			reply.Body.Close()
		}
		if err == nil && reply.StatusCode != 500 {
			t.Fatal("invalid stream was accepted")
		}
		if err := s.Verify(); err == nil || !strings.Contains(err.Error(), "invalid response stream") {
			t.Fatal("ignored stream configuration failure was lost", err)
		}
	})
}
