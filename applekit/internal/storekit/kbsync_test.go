package storekit

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/kbsync"
)

func TestKbSyncIdentity(t *testing.T) {
	hardware := []byte{0, 17, 34, 51, 68, 85}
	s := &Session{Account: Account{DirectoryServicesId: "123"}, hardware: hardware}

	check := func() {
		t.Helper()
		dsid, err := strconv.ParseUint(s.Account.DirectoryServicesId, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.kbsync(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		checkCachedKbSync(t, s, got)
		if s.kbsyncCache.dsid != dsid || !bytes.Equal(s.kbsyncCache.hardware, s.hardware) {
			t.Fatal("cache retained a different identity")
		}
		if again, err := s.kbsync(context.Background()); err != nil || again != got {
			t.Fatal("cache hit changed the packet", err)
		}
	}

	check()
	s.Account.DirectoryServicesId = "456"
	check()
	cachedHardware := bytes.Clone(s.kbsyncCache.hardware)
	hardware[0] = 255
	if !bytes.Equal(s.kbsyncCache.hardware, cachedHardware) {
		t.Fatal("cache hardware aliases its input")
	}
	check()
	s.Account.DirectoryServicesId = "000456"
	check()
	s.Account.DirectoryServicesId = "18446744073709551615"
	check()
	serialized := make([]byte, 24)
	binary.LittleEndian.PutUint32(serialized, 6)
	copy(serialized[4:], hardware)
	s.hardware = serialized
	check()
	for _, dsid := range []string{"", "-1", "invalid", "18446744073709551616"} {
		s.Account.DirectoryServicesId = dsid
		if got, err := s.kbsync(context.Background()); err == nil || got != "" || !strings.Contains(err.Error(), "parse dsid:") {
			t.Fatal("invalid DSID returned a cached value", err)
		}
	}
	s.Account.DirectoryServicesId = "456"
	for _, invalid := range [][]byte{nil, {1, 2, 3}, make([]byte, 24)} {
		s.hardware = invalid
		if got, err := s.kbsync(context.Background()); err == nil || got != "" || !strings.Contains(err.Error(), "generate kbsync:") {
			t.Fatal("invalid hardware returned a cached value", err)
		}
	}
	s.hardware = hardware
	check() // Errors must not poison later valid calls.
	other := &Session{Account: Account{DirectoryServicesId: "789"}, hardware: hardware}
	if got, err := other.kbsync(context.Background()); err != nil || got == s.kbsyncCache.value {
		t.Fatal("independent sessions share an account's value", err)
	}
}

func TestKbSyncConcurrent(t *testing.T) {
	s := &Session{Account: Account{DirectoryServicesId: "123"}, hardware: []byte{0, 17, 34, 51, 68, 85}}
	results := make(chan string, 80)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 10 {
				got, err := s.kbsync(context.Background())
				if err != nil {
					t.Error(err)
					continue
				}
				results <- got
			}
		})
	}
	wg.Wait()
	close(results)
	for got := range results {
		checkCachedKbSync(t, s, got)
	}
}

func TestKbSyncCacheIsNotSerialized(t *testing.T) {
	s := &Session{
		Account: Account{DirectoryServicesId: "123", PasswordToken: "synthetic", Guid: "001122334455"},
		Client:  &http.Client{Jar: newJar()}, hardware: []byte{0, 17, 34, 51, 68, 85}, amd: ams.AmdController{Bag: ams.Bag{DownloadURL: "https://buy.example.test/download"}},
	}
	before, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.kbsync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checkCachedKbSync(t, s, got)
	after, err := s.Export()
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("cache changed the serialized session", err)
	}
	restored, err := Restore(after)
	if err != nil {
		t.Fatal(err)
	}
	if restored.kbsyncCache.value != "" {
		t.Fatal("restored a serialized cache")
	}
	fresh, err := restored.kbsync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checkCachedKbSync(t, restored, fresh)
	if again, err := restored.kbsync(context.Background()); err != nil || again != fresh {
		t.Fatal("restored cache hit changed its packet", err)
	}
}

func checkCachedKbSync(t *testing.T, s *Session, value string) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) != 196 || binary.BigEndian.Uint32(raw[:4]) != 0x00040003 {
		t.Fatal("invalid cached keybag framing", err)
	}
	if value != s.kbsyncCache.value {
		t.Fatal("packet was not cached")
	}
}

func BenchmarkSessionKbSync(b *testing.B) {
	for _, mode := range []string{"uncached", "cold", "warm"} {
		b.Run(mode, func(b *testing.B) {
			hardware := []byte{0, 17, 34, 51, 68, 85}
			s := &Session{Account: Account{DirectoryServicesId: "123"}, hardware: hardware}
			if _, err := s.kbsync(context.Background()); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if mode == "uncached" {
					dsid, err := strconv.ParseUint(s.Account.DirectoryServicesId, 10, 64)
					if err != nil {
						b.Fatal(err)
					}
					raw, err := kbsync.Derive(hardware, dsid)
					if err != nil {
						b.Fatal(err)
					}
					benchmarkKbSync = base64.StdEncoding.EncodeToString(raw)
					continue
				}
				if mode == "cold" {
					s = &Session{Account: s.Account, hardware: hardware}
				}
				var err error
				benchmarkKbSync, err = s.kbsync(context.Background())
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

var benchmarkKbSync string
