package plistutil

import (
	"bytes"
	"errors"
	"math"

	"howett.net/plist"
)

// Unmarshal strips an XML prolog/DTD and leaves binary bodies untouched.
func Unmarshal(body []byte, value any) (int, error) {
	if !bytes.HasPrefix(body, []byte("bplist")) {
		body = bytes.TrimSpace(body)
		if start := bytes.Index(body, []byte("<plist")); start >= 0 {
			if end := bytes.LastIndex(body, []byte("</plist>")); end >= start {
				body = body[start : end+len("</plist>")]
			} else {
				return 0, errors.New("incomplete Apple plist")
			}
		}
	}
	if len(body) == 0 {
		return 0, errors.New("empty Apple plist")
	}
	return plist.Unmarshal(body, value)
}

func Int64(value any) (int64, bool) {
	switch n := value.(type) {
	case int64:
		return n, true
	case uint64:
		if n <= math.MaxInt64 {
			return int64(n), true
		}
	}
	return 0, false
}
