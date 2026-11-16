package storekit

import (
	"context"
	"fmt"
	"strconv"
)

// ListVersions returns entitled external version Ids, oldest first, and the latest ID.
func (s *Session) ListVersions(ctx context.Context, appId int64) (ids []string, latest string, err error) {
	kb, err := s.kbsync(ctx)
	if err != nil {
		return nil, "", err
	}
	item, err := s.resolveDownload(ctx, appId, kb, "", false)
	if err != nil {
		return nil, "", err
	}
	return versionIdentifiers(item)
}

func versionIdentifiers(item map[string]any) (ids []string, latest string, err error) {
	meta, _ := item["metadata"].(map[string]any)
	if meta == nil {
		return nil, "", fmt.Errorf("version response has no metadata")
	}
	if arr, ok := meta["softwareVersionExternalIdentifiers"].([]any); ok {
		for _, v := range arr {
			if s := numToString(v); s != "" {
				ids = append(ids, s)
			}
		}
	}
	latest = numToString(meta["softwareVersionExternalIdentifier"])
	if latest == "" && len(ids) > 0 {
		latest = ids[len(ids)-1]
	}
	if latest == "" {
		return nil, "", fmt.Errorf("no version identifiers in metadata")
	}
	return ids, latest, nil
}

func numToString(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case int64:
		return strconv.FormatInt(n, 10)
	case uint64:
		return strconv.FormatUint(n, 10)
	case int:
		return strconv.Itoa(n)
	case float64:
		return strconv.FormatInt(int64(n), 10)
	default:
		return ""
	}
}
