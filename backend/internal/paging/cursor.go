// Package paging encodes keyset cursors: a page continues from the sorting value and id of its edge row.
package paging

import (
	"encoding/base64"
	"encoding/json"
	"strconv"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

const (
	maxCursorLength = 8_192
)

type Key struct {
	Id    string
	Value any
}

// Page is one step through a list: up to Limit rows after a row, or before it when Back is set.
type Page struct {
	Limit int
	After *Key
	Back  bool
}

// Token encodes only the sorting value and the row id.
func Token(key Key) string {
	data, _ := json.Marshal([2]any{key.Value, key.Id})
	return base64.RawURLEncoding.EncodeToString(data)
}

func ParseCursor(value string) (Key, error) {
	var pair []json.RawMessage
	data, err := base64.RawURLEncoding.DecodeString(value)
	if len(value) > maxCursorLength || err != nil || json.Unmarshal(data, &pair) != nil || len(pair) != 2 {
		return Key{}, fault.InvalidRequest
	}
	var key Key
	if json.Unmarshal(pair[0], &key.Value) != nil || json.Unmarshal(pair[1], &key.Id) != nil || key.Id == "" {
		return Key{}, fault.InvalidRequest
	}
	return key, nil
}

// NewPage checks a requested step. A move needs a cursor; no cursor starts at the first row.
func NewPage(limit int, cursor, move string) (Page, error) {
	if limit < 1 || limit > wire.MaxPageSize || move != "" && move != "next" && move != "prev" || move != "" && cursor == "" {
		return Page{}, fault.InvalidRequest
	}
	page := Page{Limit: limit, Back: move == "prev"}
	if cursor != "" {
		key, err := ParseCursor(cursor)
		if err != nil {
			return Page{}, err
		}
		page.After = &key
	}
	return page, nil
}

// ParsePage reads the limit, cursor and move query parameters.
func ParsePage(limit, cursor, move string, defaultLimit int) (Page, error) {
	value := defaultLimit
	if limit != "" {
		var err error
		if value, err = strconv.Atoi(limit); err != nil {
			return Page{}, fault.InvalidRequest
		}
	}
	return NewPage(value, cursor, move)
}
