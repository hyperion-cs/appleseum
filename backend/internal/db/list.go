package db

import (
	"context"
	"slices"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/paging"
	"appleseum/backend/internal/wire"
)

const (
	maxCursorTextLength = 4_096
)

// Query selects one page of a list, sorted by one of the list's keys.
type Query struct {
	Sort     string
	Desc     bool
	Page     paging.Page
	Search   string
	Delisted bool
	Mine     bool
}

type sortKey struct {
	expr    string
	numeric bool
}

// list is a keyset-paged SELECT: rows come from source, ordered by a sort
// expression and then by the source's unique id column.
type list struct {
	columns string
	args    []any
	source  string
	where   string
	filter  []any
	sorts   map[string]sortKey
}

func (l list) key(q Query) (sortKey, error) {
	key, ok := l.sorts[q.Sort]
	if !ok {
		return key, fault.InvalidRequest
	}
	if after := q.Page.After; after != nil {
		_, number := after.Value.(float64)
		text, isText := after.Value.(string)
		if !wire.Uuid(after.Id) || key.numeric != number || !key.numeric && (!isText || !wire.Text(text, maxCursorTextLength)) {
			return key, fault.InvalidRequest
		}
	}
	return key, nil
}

func (l list) pageSql(key sortKey, desc bool, page paging.Page) (string, []any) {
	order, op := "ASC", ">"
	if desc != page.Back {
		order, op = "DESC", "<"
	}
	where, args := l.where, slices.Concat(l.args, l.filter)
	if page.After != nil {
		where += " AND (" + key.expr + ",id)" + op + "(?,?)"
		args = append(args, page.After.Value, page.After.Id)
	}
	return "SELECT " + l.columns + "," + key.expr + ",id FROM " + l.source + " " + where +
		" ORDER BY " + key.expr + " " + order + ",id " + order + " LIMIT ?", append(args, page.Limit+1)
}

// readPage reads a page and its neighbors' existence. fields returns the
// item's scan targets for l.columns and where its cursor goes.
func readPage[T any](ctx context.Context, q querier, l list, query Query, fields func(*T) ([]any, *string)) (wire.Page[T], error) {
	key, err := l.key(query)
	if err != nil {
		return wire.Page[T]{}, err
	}
	page := query.Page
	// A page whose rows are gone falls back to the nearest edge: the last page
	// after stepping past the end, the first one after stepping before the start.
	for attempt := 0; attempt < 2; attempt++ {
		sql, args := l.pageSql(key, query.Desc, page)
		items, err := collect(ctx, q, func(row scanner) (T, error) {
			var item T
			var cursor paging.Key
			targets, token := fields(&item)
			err := row.Scan(append(targets, &cursor.Value, &cursor.Id)...)
			*token = paging.Token(cursor)
			return item, err
		}, sql, args...)
		if err != nil {
			return wire.Page[T]{}, err
		}
		if len(items) == 0 && page.After != nil {
			page = paging.Page{Limit: page.Limit, Back: !page.Back}
			continue
		}
		result := wire.Page[T]{Items: items, Next: len(items) > page.Limit, Previous: page.After != nil && len(items) > 0}
		if result.Next {
			result.Items = items[:page.Limit]
		}
		if page.Back {
			slices.Reverse(result.Items)
			result.Next, result.Previous = result.Previous, result.Next
		}
		err = q.QueryRowContext(ctx, "SELECT count(*) FROM "+l.source+" "+l.where, l.filter...).Scan(&result.Total)
		return result, err
	}
	return wire.Page[T]{Items: []T{}}, nil
}
