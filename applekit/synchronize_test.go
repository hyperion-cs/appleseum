package applekit

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"net/url"
	"testing"
	"time"

	"howett.net/plist"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
	"github.com/hyperion-cs/appleseum/applekit/internal/grandslam"
	"github.com/hyperion-cs/appleseum/applekit/internal/storekit"
	"github.com/hyperion-cs/appleseum/applekit/internal/testfixture"
)

func TestMidServiceSyncPreservesIdentityRoutingAndResponseCookies(t *testing.T) {
	for _, accepted := range []bool{true, false} {
		t.Run(map[bool]string{true: "accepted", false: "rejected"}[accepted], func(t *testing.T) {
			d := testfixture.Load(t)
			raw := d.Idms.Record
			state := &installation{Uuid: d.Uuid, LocalUser: d.LocalUser, Idms: append([]byte("IDM1"), raw...), Routing: 7}
			at := time.Unix(1_793_613_402, 0).UTC()
			sim := make([]byte, 32)
			binary.BigEndian.PutUint32(sim, 4)
			binary.BigEndian.PutUint32(sim[8:], 1)
			challenge := &grandslam.StatusError{Status: grandslam.Status{Code: -22_421, ServerStatus: 434, DeviceTarget: "native", DeviceData: base64.StdEncoding.EncodeToString(sim)}}
			lookupResponse := sdkReply(t, map[string]any{"urls": map[string]any{"midSyncMachine": "https://gsa.apple.com/grandslam/MidService/syncMachine"}})
			lookupResponse.Header.Set("Set-Cookie", "sync-stage=lookup; Path=/; Secure")
			lookup := midStep(t, state, "https://gsa.apple.com/grandslam/GsService2/lookup", nil, lookupResponse)
			lookup.Header.Set("X-Apple-Client-App-Name", "Apple Configurator")
			code, status := 0, 200
			if !accepted {
				code, status = -1, 403
			}
			syncResponse := sdkReply(t, map[string]any{"Response": map[string]any{"Status": map[string]any{"ec": code}, "X-Apple-I-MD-RINFO": 99}})
			syncResponse.StatusCode = status
			syncResponse.Header.Set("Set-Cookie", "sync-stage=complete; Path=/; Secure")
			syncStep := applebackemu.Step{Method: "POST", Url: "https://gsa.apple.com/grandslam/MidService/syncMachine", Response: syncResponse, Header: lookup.Header.Clone()}
			syncStep.Header.Set("Cookie", "sync-stage=lookup")
			syncStep.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			backend := applebackemu.NewTest(t, applebackemu.InMemory, lookup, syncStep)
			observer := &sdkActorObserver{next: backend.Client().Transport}
			client := storekit.NewSessionHttpClientWithTransport(observer)
			err := synchronizeIdms(context.Background(), client, state, AuthenticatorOptions{Language: "en-RU"}, challenge, at)
			if (err == nil) != accepted || len(backend.Requests()) != 2 {
				t.Fatal("incorrect sync result", err)
			}
			observer.checkAuthKit(t, 2)
			var wire struct {
				Request struct {
					Srm     string `plist:"srm"`
					Mid     string `plist:"X-Apple-I-MD-M"`
					Routing uint64 `plist:"X-Apple-I-MD-RINFO"`
				}
			}
			if _, err := plist.Unmarshal(backend.Requests()[1].Body, &wire); err != nil {
				t.Fatal(err)
			}
			srm, err := base64.StdEncoding.DecodeString(wire.Request.Srm)
			if err != nil || len(srm) != 121 || binary.BigEndian.Uint32(srm[80:]) != uint32(at.Unix()) || wire.Request.Routing != 7 {
				t.Fatal("invalid Go Sync request")
			}
			mid, err := base64.StdEncoding.DecodeString(wire.Request.Mid)
			if err != nil || !bytes.Equal(mid, raw[36:96]) {
				t.Fatal("sync changed MID")
			}

			if (accepted && state.Routing != 99) || (!accepted && state.Routing != 7) {
				t.Fatal("incorrect routing commit")
			}
			u, _ := url.Parse("https://gsa.apple.com/grandslam/GsService2")
			cookies := client.Jar.Cookies(u)
			if len(cookies) != 1 || cookies[0].Value != "complete" {
				t.Fatal("sync response cookie was discarded")
			}
		})
	}
}
