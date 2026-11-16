package applekit

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/adi"
	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
	"github.com/hyperion-cs/appleseum/applekit/internal/storekit"
	"github.com/hyperion-cs/appleseum/applekit/internal/testfixture"
)

func TestMidServiceProvisioningUsesVerifiedGoContextAndAuthKit(t *testing.T) {
	d := testfixture.Load(t)

	read := func(name string) []byte {
		switch name {
		case "host-uuid.bin":
			return d.Uuid[:]
		case "spim.bin":
			return d.Idms.Spim
		case "ptm.bin":
			return d.Idms.Ptm
		case "tk.bin":
			return d.Idms.TrustKey
		}
		t.Fatalf("unknown fixture %s", name)
		return nil
	}

	oldIdms := d.Idms.Record
	state := &installation{Version: 1, LocalUser: d.LocalUser, Idms: append([]byte("IDM1"), oldIdms...), Amd: []byte("preserve-existing-amd"), Storefront: "preserve-storefront"}
	copy(state.Uuid[:], read("host-uuid.bin"))
	at := time.Unix(d.Time, 0).UTC()
	backend := applebackemu.NewTest(t, applebackemu.InMemory,
		midStep(t, state, "https://gsa.apple.com/grandslam/GsService2/lookup", nil, sdkReply(t, map[string]any{"urls": map[string]any{
			"midStartProvisioning": "https://gsa.apple.com/grandslam/MidService/startMachineProvisioning", "midFinishProvisioning": "https://gsa.apple.com/grandslam/MidService/finishMachineProvisioning",
		}})),
		midStep(t, state, "https://gsa.apple.com/grandslam/MidService/startMachineProvisioning", map[string]any{"Header": map[string]any{}, "Request": map[string]any{}}, sdkReply(t, map[string]any{"Response": map[string]any{"spim": base64.StdEncoding.EncodeToString(read("spim.bin")), "Status": map[string]any{"ec": 0}}})),
		midStep(t, state, "https://gsa.apple.com/grandslam/MidService/finishMachineProvisioning", map[string]any{"Header": map[string]any{}, "Request": map[string]any{"cpim": base64.StdEncoding.EncodeToString(d.Idms.Cpim)}}, sdkReply(t, map[string]any{"Response": map[string]any{
			"ptm": base64.StdEncoding.EncodeToString(read("ptm.bin")), "tk": base64.StdEncoding.EncodeToString(read("tk.bin")), "X-Apple-I-MD-RINFO": uint64(17_106_176), "Status": map[string]any{"ec": 0},
		}})),
	)
	observer := &sdkActorObserver{next: backend.Client().Transport}
	client := storekit.NewSessionHttpClientWithTransport(observer)
	options := AuthenticatorOptions{Language: "en-us"}
	if err := provisionIdms(context.Background(), client, state, options, at); err != nil {
		t.Fatal(err)
	}
	restored, err := adi.ParseIdms(state.Idms)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := restored.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved[4:], d.Idms.Record) || state.Routing != 17_106_176 {
		t.Fatal("IdMS response lost verified record or server routing")
	}
	if state.IdmsGeneration != idmsProvisioningGeneration || string(state.Amd) != "preserve-existing-amd" || state.Storefront != "preserve-storefront" || state.LocalUser != d.LocalUser || !bytes.Equal(state.Uuid[:], read("host-uuid.bin")) {
		t.Fatal("reprovisioning changed installation ownership")
	}
	if err := provisionIdms(context.Background(), client, state, options, at.Add(time.Hour)); err != nil || len(backend.Requests()) != 3 {
		t.Fatal("provisioned installation repeated network", err)
	}
	observer.checkAuthKit(t, 3)
}

func TestMidServiceRejectsForeignUrlBeforePosting(t *testing.T) {
	state := &installation{Uuid: [16]byte{1}}
	response := sdkReply(t, map[string]any{"urls": map[string]any{"midStartProvisioning": "https://foreign.invalid/start"}})
	backend := applebackemu.NewTest(t, applebackemu.InMemory, midStep(t, state, "https://gsa.apple.com/grandslam/GsService2/lookup", nil, response))
	client := storekit.NewSessionHttpClientWithTransport(backend.Client().Transport)
	err := provisionIdms(context.Background(), client, state, AuthenticatorOptions{}, time.Now())
	if err == nil || errors.Is(err, context.Canceled) || len(backend.Requests()) != 1 {
		t.Fatal("foreign provisioning accepted")
	}
}

func TestMidServiceFailedMigrationPreservesExistingState(t *testing.T) {
	state := &installation{Uuid: [16]byte{1}, Idms: []byte("old-record"), Routing: 42, Amd: []byte("old-amd")}
	backend := applebackemu.NewTest(t, applebackemu.InMemory, midStep(t, state, "https://gsa.apple.com/grandslam/GsService2/lookup", nil, applebackemu.Response{StatusCode: 403}))
	client := storekit.NewSessionHttpClientWithTransport(backend.Client().Transport)
	if err := provisionIdms(context.Background(), client, state, AuthenticatorOptions{}, time.Now()); err == nil {
		t.Fatal("failed provisioning accepted")
	}
	if string(state.Idms) != "old-record" || state.Routing != 42 || string(state.Amd) != "old-amd" || state.IdmsGeneration != 0 {
		t.Fatal("failed migration changed existing state")
	}
}
