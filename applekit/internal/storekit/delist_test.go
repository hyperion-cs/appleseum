package storekit

import (
	"context"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

func TestLookupMetadataUsesOneBatch(t *testing.T) {
	session, _ := catalogBackend(t, applebackemu.InMemory, lookupStep(t, "us", []int64{1, 2, 3, 4, 5, 6, 7, 8}, `{"results":[{"trackId":1,"fileSizeBytes":"12345678","artworkUrl100":"https://is1-ssl.mzstatic.com/icon.jpg"},{"trackId":2,"fileSizeBytes":500},{"trackId":3},{"trackId":4,"fileSizeBytes":"-10"},{"trackId":5,"fileSizeBytes":"invalid"},{"trackId":6,"fileSizeBytes":"9223372036854775808"},{"trackId":7,"fileSizeBytes":"0"},{"trackId":99,"fileSizeBytes":"100"}]}`))
	client := session.Client
	apps, err := lookupStoreApps(context.Background(), client, "us", []int64{1, 2, 3, 4, 5, 6, 7, 8}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 7 || *apps[1].Size != 12_345_678 || *apps[2].Size != 500 || apps[1].ArtworkUrl == "" {
		t.Fatal("metadata was not retained")
	}
	for _, id := range []int64{3, 4, 5, 6, 7} {
		if apps[id].Size != nil {
			t.Fatal("invalid size was displayed")
		}
	}
	if _, ok := apps[8]; ok {
		t.Fatal("delisted app appeared in lookup")
	}
	if _, ok := apps[99]; ok {
		t.Fatal("unrequested app appeared in lookup")
	}
}

func TestUnknownCountryDoesNotFallBackToAnotherStore(t *testing.T) {
	session, _ := catalogBackend(t, applebackemu.InMemory)
	client := session.Client
	if _, err := lookupStoreApps(context.Background(), client, "", []int64{1}, nil); err == nil {
		t.Fatal("unknown country silently queried another store")
	}
}

func TestLookupBatchesCatalogRequests(t *testing.T) {
	ids := make([]int64, 205)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	session, backend := catalogBackend(t, applebackemu.InMemory,
		lookupStep(t, "kz", ids[:100], `{"results":[{"trackId":1,"fileSizeBytes":"10"}]}`),
		lookupStep(t, "kz", ids[100:200], `{"results":[{"trackId":101,"fileSizeBytes":"20"}]}`),
		lookupStep(t, "kz", ids[200:], `{"results":[{"trackId":205,"fileSizeBytes":"30"}]}`),
	)
	var progress []float64
	apps, err := lookupStoreApps(context.Background(), session.Client, "kz", ids, func(value float64) { progress = append(progress, value) })
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.Requests()) != 3 || len(apps) != 3 || len(progress) != 3 || progress[0] <= 0 || progress[0] >= progress[1] || progress[1] >= progress[2] || progress[2] != 100 {
		t.Fatal("lookup batches or progress were lost", progress)
	}
	for _, id := range []int64{1, 101, 205} {
		if apps[id].Size == nil {
			t.Fatal("lookup batch metadata was lost", id)
		}
	}
}
