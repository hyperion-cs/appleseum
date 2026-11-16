package applebackemu

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestPurchaseHistoryPageValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"valid", `{"paginationToken":"next","searchCriteria":{"contentTypeGroups":["APP"],"customYear":2026,"dateRangeType":"CUSTOM"}}`, true},
		{"wrong-year-type", `{"paginationToken":"next","searchCriteria":{"contentTypeGroups":["APP"],"customYear":"2026","dateRangeType":"CUSTOM"}}`, false},
		{"wrong-cursor", `{"paginationToken":"other","searchCriteria":{"contentTypeGroups":["APP"],"customYear":2026,"dateRangeType":"CUSTOM"}}`, false},
		{"nested-cursor", `{"searchCriteria":{"paginationToken":"next","contentTypeGroups":["APP"],"customYear":2026,"dateRangeType":"CUSTOM"}}`, false},
		{"wrong-filter", `{"paginationToken":"next","searchCriteria":{"contentTypeGroups":["ALL"],"customYear":2026,"dateRangeType":"CUSTOM"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step, err := PurchaseHistoryPage(StoreContext{}, 2_026, "next", Response{})
			if err != nil {
				t.Fatal(err)
			}
			assertJsonRequest(t, step, tc.body, tc.valid)
		})
	}
}

func TestFinanceJsonRetainsLargeNumbersAndRejectsExtraDocuments(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"valid", `{"flag":true,"value":9007199254740993}`, true},
		{"rounded-number", `{"value":9007199254740992,"flag":true}`, false},
		{"wrong-type", `{"value":9007199254740993,"flag":"true"}`, false},
		{"extra-document", `{"value":9007199254740993,"flag":true} {}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step, err := FinanceRequest(StoreContext{}, "https://ph2.itunes.apple.com/test", map[string]any{"value": json.Number("9007199254740993"), "flag": true}, Response{})
			if err != nil {
				t.Fatal(err)
			}
			assertJsonRequest(t, step, tc.body, tc.valid)
		})
	}
}

func TestAccountSummaryRejectsMutation(t *testing.T) {
	step, err := AccountSummary(StoreContext{}, "42", Response{})
	if err != nil {
		t.Fatal(err)
	}
	assertJsonRequest(t, step, `{"query":"mutation changeAccount { account { appleId } }"}`, false)
}

func assertJsonRequest(t *testing.T, step Step, body string, valid bool) {
	t.Helper()
	s := newFixture(t, InMemory, step)
	r, err := http.NewRequest(step.Method, step.Url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header = step.Header.Clone()
	response, err := s.Client().Do(r)
	if response != nil {
		response.Body.Close()
	}
	if valid {
		if err != nil || s.Verify() != nil {
			t.Fatal("valid JSON request rejected", err)
		}
	} else if err == nil || s.Verify() == nil {
		t.Fatal("invalid JSON request accepted")
	}
}
