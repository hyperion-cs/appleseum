package debuglog

import (
	"errors"
	"net/url"
	"testing"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

func TestDebugSelectionUsesBoundedJournalAndMatchingResponses(t *testing.T) {
	s := &Service{cfg: Config{Enabled: true}}
	fill := func(count int, change func(i int, e *entry)) {
		entries := make([]entry, count)
		for i := range entries {
			entries[i].event = wire.DebugEvent{Id: int64(i + 1), Source: "API", Method: "GET", Target: "/fixture/match"}
			change(i, &entries[i])
		}
		s.log.segments = []segment{{entries: entries}}
	}
	fill(capacity, func(i int, e *entry) {
		if e.event.HasResponse = i%2 == 0; i == 0 {
			e.event.Source = "System"
		}
	})
	for _, sources := range [][]string{{"API"}, {"API", "Hosting"}} {
		filter, err := NewFilter(sources, "MATCH")
		if err != nil {
			t.Fatal(err)
		}
		ids, err := s.Selected(filter)
		if err != nil || len(ids) != capacity/2-1 {
			t.Fatal(len(ids), err)
		}
		for _, id := range ids {
			if id <= 1 || id%2 != 1 {
				t.Fatal("selected an unavailable response", id)
			}
		}
	}
	for _, query := range []url.Values{{"source": {"unknown"}}, {"q": {"one", "two"}}} {
		if _, _, err := ParseQuery(query); err == nil {
			t.Fatal("invalid selection query accepted", query)
		}
	}
	for _, count := range []int{wire.MaxSelectionSize, wire.MaxSelectionSize + 1} {
		fill(count, func(_ int, e *entry) { e.event.HasResponse = true })
		ids, err := s.Selected(Filter{})
		if count == wire.MaxSelectionSize && (err != nil || len(ids) != count) {
			t.Fatal("512 IDs rejected", err)
		}
		if count > wire.MaxSelectionSize && !errors.Is(err, fault.SelectionTooLarge) {
			t.Fatal("513 IDs accepted", err)
		}
	}
}
