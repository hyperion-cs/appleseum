package api

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiterConcurrentRequestsAndWindowBoundary(t *testing.T) {
	var limiter Limiter
	now := time.Unix(1_700_000_000, 0)
	var accepted atomic.Int32
	var workers sync.WaitGroup
	for i := 0; i < 100; i++ {
		workers.Go(func() {
			if limiter.allow(Login, "ip:fixture", now) {
				accepted.Add(1)
			}
		})
	}
	workers.Wait()
	if int(accepted.Load()) != Login.Limit() {
		t.Fatalf("accepted %d requests", accepted.Load())
	}
	if limiter.allow(Login, "ip:fixture", now.Add(rateWindowDuration-time.Nanosecond)) {
		t.Fatal("window reset early")
	}
	if !limiter.allow(Login, "ip:fixture", now.Add(rateWindowDuration)) {
		t.Fatal("window did not reset")
	}
	if !limiter.allow(Read, "ip:fixture", now) {
		t.Fatal("classes share a counter")
	}
}

func TestLimiterCapacityPreservesKnownClientsAndReclaimsExpired(t *testing.T) {
	var limiter Limiter
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < maxRateWindows; i++ {
		if !limiter.allow(Read, strconv.Itoa(i), now) {
			t.Fatal("capacity reached early")
		}
	}
	if limiter.allow(Read, "new", now) {
		t.Fatal("capacity unbounded")
	}
	if !limiter.allow(Read, "0", now) {
		t.Fatal("new identities blocked an existing client")
	}
	if !limiter.allow(Read, "new", now.Add(rateWindowDuration)) {
		t.Fatal("expired windows not reclaimed")
	}
	if len(limiter.windows) != 1 {
		t.Fatal("expired entries retained")
	}
}

func TestInvalidRateClassIsRejected(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("unclassified route accepted")
		}
	}()
	RateClass(0).Limit()
}
