package db

import "strings"

const (
	catalogSearch searchSource = "catalog"
	packageSearch searchSource = "packages"
	shareSearch   searchSource = "shares"
)

type searchSource string

func fold(value string) string { return strings.ToLower(value) }

// The native trigram tokenizer has no matches below three Unicode characters.
func searchIds(source searchSource, query string) (string, []any) {
	query = fold(query)
	// CROSS JOIN keeps FTS hits ahead of metadata lookups by primary key.
	return `SELECT d.entity_id FROM search_names
 CROSS JOIN search_documents d ON d.id=search_names.rowid WHERE d.source=? AND search_names MATCH ?`, []any{source, `"` + strings.ReplaceAll(query, `"`, `""`) + `"`}
}

func searchFilter(source searchSource, where string, args []any, query string) (string, []any) {
	if query == "" {
		return where, args
	}
	ids, values := searchIds(source, query)
	return where + " AND id IN (" + ids + ")", append(args, values...)
}
