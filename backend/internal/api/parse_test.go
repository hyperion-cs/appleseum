package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"appleseum/backend/internal/db"
	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/paging"
	"appleseum/backend/internal/wire"
)

func TestDecodeJsonBoundary(t *testing.T) {
	type body struct {
		Value string `json:"value"`
	}
	for _, tt := range []struct {
		name, contentType, body string
		status                  int
	}{
		{"json", "application/json", `{"value":"ok"}`, 0},
		{"media type case and whitespace", "Application/JSON ; charset=utf-8", `{"value":"ok"}`, 0},
		{"wrong media type", "text/plain", `{"value":"ok"}`, http.StatusUnsupportedMediaType},
		{"broken media parameter", "application/json; charset=", `{"value":"ok"}`, http.StatusUnsupportedMediaType},
		{"unknown field", "application/json", `{"extra":1}`, http.StatusBadRequest},
		{"second value", "application/json", `{"value":"ok"} {}`, http.StatusBadRequest},
		{"oversized", "application/json", `{"value":"` + strings.Repeat("x", maxJsonBytes) + `"}`, http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("PUT", "http://localhost/api/preferences", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.contentType)
			w := httptest.NewRecorder()
			value, err := decode[body](w, r)
			if tt.status == 0 {
				if err != nil || value.Value != "ok" {
					t.Fatal(value, err)
				}
				return
			}
			(&Server{cfg: Config{Report: func(string, error) {}}}).fail(w, err)
			if w.Code != tt.status {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
}

func TestCompactCursorContainsOnlyValueAndId(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	for _, value := range []any{"2021-11-03", "Тестовое название", float64(12_345)} {
		encoded := paging.Token(paging.Key{Id: id, Value: value})
		raw, err := base64.RawURLEncoding.DecodeString(encoded)
		var pair []any
		if err != nil || json.Unmarshal(raw, &pair) != nil || len(pair) != 2 || pair[0] != value || pair[1] != id {
			t.Fatal("cursor must contain only the sorting value and ID", string(raw), err)
		}
		key, err := paging.ParseCursor(encoded)
		if err != nil || key.Id != id || key.Value != value {
			t.Fatal("cursor did not round trip", key, err)
		}
	}
	cursor := paging.Token(paging.Key{Id: id, Value: "same"})
	for _, move := range []string{"next", "prev"} {
		query, err := listQuery(url.Values{"sort": {"name"}, "direction": {"desc"}, "cursor": {cursor}, "move": {move}, "q": {"different filter"}}, wire.CatalogTable)
		if err != nil || query.Page.Back != (move == "prev") || query.Page.After.Id != id || query.Page.After.Value != "same" {
			t.Fatal("direction must come from the request", query, err)
		}
	}
	for _, raw := range []string{`{}`, `["name"]`, `["name","id",true]`, `["name",null]`} {
		if _, err := paging.ParseCursor(base64.RawURLEncoding.EncodeToString([]byte(raw))); err == nil {
			t.Fatal("malformed cursor accepted", raw)
		}
	}
	if _, err := listQuery(url.Values{"move": {"prev"}}, wire.CatalogTable); err == nil {
		t.Fatal("backward page without a boundary accepted")
	}
}

// db accepts sort keys only from its own table; every other parameter
// is checked here, before any SQL is built.
func TestListParametersRejectInjectedInput(t *testing.T) {
	cursor := paging.Token(paging.Key{Id: db.NewId(), Value: "name"})
	for _, table := range wire.TableNames {
		for _, field := range []string{"direction", "limit", "move", "delisted", "mine", "q", "unknown"} {
			for _, attack := range []string{`1 OR 1=1`, `name; DROP TABLE packages; --`, `" UNION SELECT sql FROM sqlite_schema --`, `desc/*comment*/`, "x\n"} {
				input := url.Values{"sort": {"name"}, "direction": {"asc"}, "limit": {"1"}, "cursor": {cursor}}
				input.Set(field, attack)
				if field == "q" && !strings.ContainsAny(attack, "\n") {
					continue // Any text is a valid search; it only ever binds as a parameter.
				}
				if _, err := listQuery(input, table); !errors.Is(err, fault.InvalidRequest) {
					t.Fatalf("%s accepted %s=%q: %v", table, field, attack, err)
				}
			}
		}
	}
	for _, values := range []url.Values{{"limit": {"0"}}, {"limit": {"129"}}, {"limit": {"1", "2"}}, {"cursor": {"broken"}}, {"q": {"ab"}}} {
		if _, err := listQuery(values, wire.LibraryTable); err == nil {
			t.Fatal("invalid list query accepted", values)
		}
	}
	if _, err := listQuery(url.Values{"mine": {"true"}}, wire.CatalogTable); err == nil {
		t.Fatal("mine filter accepted outside the library")
	}
}
