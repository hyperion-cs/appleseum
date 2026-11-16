package storekit

import (
	"net/http"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

func catalogContext() applebackemu.StoreContext {
	return applebackemu.StoreContext{Dsid: "123", Storefront: "143517-2,13"}
}

func catalogReply(body string) applebackemu.Response {
	return applebackemu.Response{Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(body)}
}

func catalogBackend(t *testing.T, mode applebackemu.Mode, steps ...applebackemu.Step) (*Session, *applebackemu.Server) {
	t.Helper()
	backend, err := applebackemu.New(mode, steps...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		backend.Close()
		if err := backend.Verify(); err != nil {
			t.Error(err)
		}
	})
	return &Session{
		Account: Account{Pod: "42", DirectoryServicesId: "123", StoreFront: "143517-2,34", Guid: "001122AABBCC"},
		Client:  NewSessionHttpClientWithTransport(backend.Client().Transport),
	}, backend
}

func settingsStep(t *testing.T, years ...int) applebackemu.Step {
	t.Helper()
	step, err := applebackemu.PurchaseSettings(catalogContext(), years)
	if err != nil {
		t.Fatal(err)
	}
	return step
}

func historyStep(t *testing.T, year int, cursor, body string) applebackemu.Step {
	t.Helper()
	step, err := applebackemu.PurchaseHistoryPage(catalogContext(), year, cursor, catalogReply(body))
	if err != nil {
		t.Fatal(err)
	}
	return step
}

func lookupStep(t *testing.T, country string, ids []int64, body string) applebackemu.Step {
	t.Helper()
	step, err := applebackemu.StoreLookup(country, ids, catalogReply(body))
	if err != nil {
		t.Fatal(err)
	}
	return step
}

func summaryStep(t *testing.T, body string) applebackemu.Step {
	t.Helper()
	step, err := applebackemu.AccountSummary(catalogContext(), "42", catalogReply(body))
	if err != nil {
		t.Fatal(err)
	}
	return step
}
