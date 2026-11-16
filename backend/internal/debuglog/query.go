package debuglog

import (
	"cmp"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/paging"
	"appleseum/backend/internal/wire"
)

const (
	DefaultPageLimit = 10
)

const (
	maxSearchLength = 200
)

// Sources are the origins an event can be filtered by.
var Sources = []string{"Apple", "API", "Auth", "Hosting", "System"}

// Filter selects events by origin and by text.
type Filter struct {
	sources []string
	search  string
}

func NewFilter(sources []string, search string) (Filter, error) {
	// A single empty source is the interface's "all sources" choice.
	if len(sources) == 1 && sources[0] == "" {
		sources = nil
	}
	for _, source := range sources {
		if !slices.Contains(Sources, source) {
			return Filter{}, fault.InvalidRequest
		}
	}
	if !wire.Text(search, maxSearchLength) {
		return Filter{}, fault.InvalidRequest
	}
	return Filter{sources: sources, search: strings.ToLower(search)}, nil
}

// from reports whether an event counts as coming from a source: share
// requests are hosting activity, and sign-in events belong to the system.
func from(event wire.DebugEvent, source string) bool {
	switch {
	case event.Source == source:
		return true
	case source == "Hosting":
		return event.Source == "API" && (event.Target == "/api/shares" || strings.HasPrefix(event.Target, "/api/shares/"))
	case source == "System":
		return event.Source == "Auth"
	}
	return false
}

func (f Filter) Match(event wire.DebugEvent) bool {
	return (len(f.sources) == 0 || slices.ContainsFunc(f.sources, func(source string) bool { return from(event, source) })) &&
		strings.Contains(strings.ToLower(event.Method+" "+event.Target+" "+event.Source+" "+event.Detail+" "+strconv.Itoa(event.Status)), f.search)
}

// Page selects one page of the matching events, newest first, and counts all of them.
func Page(events []wire.DebugEvent, filter Filter, page paging.Page) (items []wire.DebugEvent, total int, next, previous bool, err error) {
	var boundary int64
	if page.After != nil {
		boundary, _ = strconv.ParseInt(page.After.Id, 10, 64)
		if number, numeric := page.After.Value.(float64); boundary <= 0 || !numeric || number != float64(boundary) {
			return nil, 0, false, false, fault.InvalidRequest
		}
	}
	matching := slices.DeleteFunc(slices.Clone(events), func(event wire.DebugEvent) bool { return !filter.Match(event) })
	slices.SortFunc(matching, func(a, b wire.DebugEvent) int { return cmp.Compare(b.Id, a.Id) })
	start, end := 0, len(matching)
	if boundary != 0 {
		// Ids descend: rows after the boundary are older, rows before it newer.
		older := slices.IndexFunc(matching, func(event wire.DebugEvent) bool { return event.Id < boundary })
		if older < 0 {
			older = len(matching)
		}
		if page.Back {
			end = slices.IndexFunc(matching, func(event wire.DebugEvent) bool { return event.Id <= boundary })
			if end < 0 {
				end = len(matching)
			}
			start = max(0, end-page.Limit)
		} else {
			start = older
		}
	}
	if !page.Back {
		end = min(end, start+page.Limit)
	}
	// A cursor the journal has already evicted shows the current first page.
	if start == end && len(matching) > 0 {
		start, end = 0, min(page.Limit, len(matching))
	}
	items = slices.Clone(matching[start:end])
	for i := range items {
		items[i].Cursor = paging.Token(paging.Key{Id: strconv.FormatInt(items[i].Id, 10), Value: items[i].Id})
	}
	return items, len(matching), end < len(matching), start > 0, nil
}

// ParseQuery reads a journal request: repeated source, q, and the paging parameters.
func ParseQuery(query url.Values) (Filter, paging.Page, error) {
	for key, values := range query {
		if !slices.Contains([]string{"limit", "source", "q", "cursor", "move"}, key) || len(values) == 0 || key != "source" && len(values) != 1 {
			return Filter{}, paging.Page{}, fault.InvalidRequest
		}
	}
	filter, err := NewFilter(query["source"], query.Get("q"))
	if err != nil {
		return Filter{}, paging.Page{}, err
	}
	page, err := paging.ParsePage(query.Get("limit"), query.Get("cursor"), query.Get("move"), DefaultPageLimit)
	return filter, page, err
}
