package wire

import (
	"strings"
	"testing"
)

func TestSearchLengthCountsUnicodeCharacters(t *testing.T) {
	for _, item := range []struct {
		query string
		valid bool
	}{
		{"", true}, {"a", false}, {"ab", false}, {"abc", true},
		{"я", false}, {"ёж", false}, {"ёж🙂", true},
		{"🙂", false}, {"🙂🙂", false}, {"🙂🙂🙂", true},
		{"abc\n", false}, {string([]byte{0xff}), false},
	} {
		if Search(item.query) != item.valid {
			t.Errorf("query %q: want valid=%t", item.query, item.valid)
		}
		if (Selection{Filter: &Filter{Query: item.query}}.Validate() == nil) != item.valid {
			t.Errorf("bulk selection bypasses search length: %q", item.query)
		}
	}
}

func TestSelectionTakesIdsOrFilterButNotBoth(t *testing.T) {
	id := "0190a6c4-3c1e-7f00-8000-000000000001"
	for _, item := range []struct {
		selection Selection
		valid     bool
	}{
		{Selection{Ids: []string{id}}, true},
		{Selection{Filter: &Filter{}}, true},
		{Selection{}, false},
		{Selection{Filter: &Filter{}, Ids: []string{id}}, false},
		{Selection{Ids: []string{id, id}}, false},
		{Selection{Ids: []string{strings.ToUpper(id)}}, false},
		{Selection{Ids: []string{"x"}}, false},
		{Selection{Ids: make([]string, MaxSelectionSize+1)}, false},
		{Selection{Filter: &Filter{Delisted: true}}, false},
	} {
		if (item.selection.For(LibraryTable) == nil) != item.valid {
			t.Errorf("%+v: want valid=%t", item.selection, item.valid)
		}
	}
}

func TestNumbersArePositiveDecimals(t *testing.T) {
	for value, valid := range map[string]bool{"1": true, "820000000": true, "0": false, "00": false, "-1": false, "1e3": false, "": false, strings.Repeat("9", 33): false} {
		if Number(value) != valid {
			t.Errorf("%q: want %t", value, valid)
		}
	}
}

func TestFiltersBelongToTheirList(t *testing.T) {
	if (Selection{Filter: &Filter{Delisted: true}}).For(CatalogTable) != nil || (Selection{Filter: &Filter{Mine: true}}).For(LibraryTable) != nil {
		t.Fatal("a list rejected its own filter")
	}
	if (Selection{Filter: &Filter{Mine: true}}).For(CatalogTable) == nil || (Selection{Filter: &Filter{Mine: true}}).For(SharesTable) == nil {
		t.Fatal("a list accepted another list's filter")
	}
	if (Selection{Ids: []string{"12"}}).For(CatalogTable) != nil || (Selection{Ids: []string{"12"}}).For(LibraryTable) == nil {
		t.Fatal("ids not checked by their list")
	}
}
