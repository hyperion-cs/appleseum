package storekit

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

func TestHistoryUsesOnlyDeclaredYearsAndTopLevelCursor(t *testing.T) {
	s, backend := catalogBackend(t, applebackemu.InMemory, settingsStep(t, 2_019, 2_026, 2_019),
		historyStep(t, 2_026, "", `{"purchases":[],"paginationToken":"next"}`),
		historyStep(t, 2_026, "next", `{"purchases":[]}`), historyStep(t, 2_019, "", `{"purchases":[]}`),
	)
	items, err := s.ListPurchases(context.Background())
	if err != nil || len(items) != 0 || len(backend.Requests()) != 4 {
		t.Fatal("history skipped cursor, queried empty years, or repeated a declared year", err)
	}
}

func TestHistoryRejectsMissingYearListsAndBadIds(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"customDateFilter":{}}`, `{"customDateFilter":{"years":null}}`, `{"customDateFilter":{"years":[{"year":0}]}}`} {
		step := settingsStep(t)
		step.Response = catalogReply(body)
		s, _ := catalogBackend(t, applebackemu.InMemory, step)
		if items, err := s.ListPurchases(context.Background()); err == nil || items != nil {
			t.Fatal("invalid settings returned a complete catalog", err)
		}
	}
	for _, id := range []string{`""`, `"not-a-number"`, `null`, `0`, `-1`, `1.5`, `9223372036854775808`} {
		var p purchasePage
		body := `{"purchases":[{"lineItems":[{"kind":"IOSApp","adamId":` + id + `}]}]}`
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatal(err)
		}
		if err := p.appendPurchases(map[int64]bool{}, new([]Purchase)); err == nil {
			t.Fatal("invalid iOS ID silently dropped", id)
		}
	}
}

func TestHistoryParsesNumericIdsArtworkAndCalendarDates(t *testing.T) {
	var p purchasePage
	body := `{"purchases":[{"webOrderDate":"10 May 2020","lineItems":[{"kind":"IOSApp","adamId":6760873038,"nameForDisplay":"Fixture","purchaseDate":"10 May 2020 21:25","coverArt":{"url":"https://is1-ssl.mzstatic.com/art/{w}x{h}ia-{q}.{f}"}},{"kind":"MacApp","adamId":"invalid"},{"kind":"BaseSubscription"}]}]}`
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	var items []Purchase
	if err := p.appendPurchases(map[int64]bool{}, &items); err != nil || len(items) != 1 || items[0].Id != 6_760_873_038 || items[0].Date != "2020-05-10" || items[0].ArtworkUrl != "https://is1-ssl.mzstatic.com/art/120x120ia.jpg" {
		t.Fatal("lost ID/date/artwork or included a non-iOS item", items, err)
	}
	for input, want := range map[string]string{"2026-11-04T01:02:03+06:00": "2026-11-04", "4 Nov 2026": "2026-11-04", "4 Sept 2026 01:23": "2026-09-04", "invalid": "", "": ""} {
		if got := purchaseDay(input); got != want {
			t.Fatal("invented or lost calendar date", input, got)
		}
	}
}
