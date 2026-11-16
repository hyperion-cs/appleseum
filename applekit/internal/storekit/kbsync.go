package storekit

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"sync"

	"github.com/hyperion-cs/appleseum/applekit/internal/kbsync"
)

type cachedKbSync struct {
	mu       sync.Mutex
	hardware []byte
	dsid     uint64
	value    string
}

func (s *Session) kbsync(_ context.Context) (string, error) {
	dsid, err := strconv.ParseUint(s.Account.DirectoryServicesId, 10, 64)
	if err != nil {
		return "", fmt.Errorf("parse dsid: %w", err)
	}
	c := &s.kbsyncCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value != "" && c.dsid == dsid && bytes.Equal(c.hardware, s.hardware) {
		return c.value, nil
	}
	raw, err := kbsync.Derive(s.hardware, dsid)
	if err != nil {
		return "", fmt.Errorf("generate kbsync: %w", err)
	}
	c.hardware = bytes.Clone(s.hardware)
	c.dsid = dsid
	c.value = base64.StdEncoding.EncodeToString(raw)
	return c.value, nil
}
