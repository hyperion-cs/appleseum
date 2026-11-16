package dns

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type dnsFixture struct {
	address            string
	udp, tcp           atomic.Int32
	dropUdp, failTcp   atomic.Bool
	negative, truncate atomic.Bool
}

func serveDns(t *testing.T) *dnsFixture {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	udpAddress, err := net.ResolveUDPAddr("udp4", address)
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenUDP("udp4", udpAddress)
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	f := &dnsFixture{address: address}

	response := func(data []byte, tcp bool) []byte {
		var request dnsmessage.Message
		if err := request.Unpack(data); err != nil {
			t.Errorf("DNS request: %v", err)
			return nil
		}
		message := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, RecursionAvailable: true}, Questions: request.Questions}
		switch {
		case f.negative.Load():
			message.RCode = dnsmessage.RCodeNameError
		case f.truncate.Load() && !tcp:
			message.Truncated = true
		default:
			for _, q := range request.Questions {
				if q.Type == dnsmessage.TypeA {
					message.Answers = append(message.Answers, dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}})
				}
			}
		}
		data, err := message.Pack()
		if err != nil {
			t.Errorf("DNS response: %v", err)
		}
		return data
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := make([]byte, 4_096)
		for {
			n, peer, err := udp.ReadFromUDP(buf)
			if err != nil {
				return
			}
			f.udp.Add(1)
			if !f.dropUdp.Load() {
				udp.WriteToUDP(response(buf[:n], false), peer)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				c.SetDeadline(time.Now().Add(time.Second))
				prefix := make([]byte, 2)
				if _, err := io.ReadFull(c, prefix); err != nil {
					return
				}
				data := make([]byte, binary.BigEndian.Uint16(prefix))
				if _, err := io.ReadFull(c, data); err != nil {
					return
				}
				f.tcp.Add(1)
				if f.failTcp.Load() {
					return
				}
				data = response(data, true)
				prefix = binary.BigEndian.AppendUint16(prefix[:0], uint16(len(data)))
				c.Write(append(prefix, data...))
			}()
		}
	}()
	t.Cleanup(func() { listener.Close(); udp.Close(); wg.Wait() })
	return f
}

func (f *dnsFixture) resolver() *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, f.address)
	}}
}

func serveTarget(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return port
}

type events struct {
	mu    sync.Mutex
	items []Event
}

func (e *events) context(ctx context.Context) context.Context {
	return WithObserver(ctx, func(event Event) { e.mu.Lock(); e.items = append(e.items, event); e.mu.Unlock() })
}

func (e *events) snapshot() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Event(nil), e.items...)
}

func connect(t *testing.T, d *dialer, ctx context.Context, host, port string) {
	t.Helper()
	c, err := d.dial(ctx, "tcp4", net.JoinHostPort(host, port))
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestHealthySystemDnsUsesNoFallbackAndDoesNotCacheAddresses(t *testing.T) {
	f, port := serveDns(t), serveTarget(t)
	d := newDialer(f.resolver(), nil, time.Second)
	e := new(events)
	for range 2 {
		connect(t, d, e.context(context.Background()), "healthy.test.", port)
	}
	if f.udp.Load() != 2 || f.tcp.Load() != 0 || d.current().index != 0 || len(e.snapshot()) != 0 {
		t.Fatalf("udp=%d tcp=%d preferred=%v events=%v", f.udp.Load(), f.tcp.Load(), d.current(), e.snapshot())
	}
}

func TestUdpTimeoutSelectsSystemTcpAndRemembersIt(t *testing.T) {
	f, port := serveDns(t), serveTarget(t)
	f.dropUdp.Store(true)
	d := newDialer(f.resolver(), nil, 60*time.Millisecond)
	e := new(events)
	connect(t, d, e.context(context.Background()), "first.test.", port)
	udp := f.udp.Load()
	connect(t, d, e.context(context.Background()), "second.test.", port)
	if d.current().index != 1 || f.udp.Load() != udp || f.tcp.Load() != 2 {
		t.Fatalf("preferred=%v udp=%d -> %d tcp=%d", d.current(), udp, f.udp.Load(), f.tcp.Load())
	}
	var failure, hint, selected bool
	for _, event := range e.snapshot() {
		failure = failure || (event.Kind == "failure" && event.Strategy == "system" && event.Err != nil && event.Duration > 0)
		hint = hint || (event.Kind == "fallback" && event.VpnPossible)
		selected = selected || (event.Kind == "selected" && event.Strategy == "system-tcp")
	}
	if !failure || !hint || !selected {
		t.Fatalf("missing diagnostics: %+v", e.snapshot())
	}
	// A change of network conditions can invalidate the remembered transport.
	f.dropUdp.Store(false)
	f.failTcp.Store(true)
	connect(t, d, context.Background(), "recovered.test.", port)
	if d.current().index != 0 {
		t.Fatal("did not return to healthy system DNS")
	}
}

func TestPublicFallbackUsesStandardResolverFraming(t *testing.T) {
	for _, wire := range []string{"udp", "tcp", "truncated-udp"} {
		t.Run(wire, func(t *testing.T) {
			system, fallback, port := serveDns(t), serveDns(t), serveTarget(t)
			system.dropUdp.Store(true)
			system.failTcp.Store(true)
			fallback.dropUdp.Store(wire == "tcp")
			fallback.truncate.Store(wire == "truncated-udp")
			d := newDialer(system.resolver(), []provider{{Name: "fixture", Servers: []string{fallback.address}}}, 60*time.Millisecond)
			e := new(events)
			connect(t, d, e.context(context.Background()), "fallback.test.", port)
			index := 2
			if wire == "tcp" {
				index = 3
			}
			if d.current().index != index {
				t.Fatalf("selected %v, want %d", d.current(), index)
			}
			if fallback.udp.Load() == 0 || (wire != "udp" && fallback.tcp.Load() == 0) {
				t.Fatalf("udp=%d tcp=%d", fallback.udp.Load(), fallback.tcp.Load())
			}
			var selected bool
			for _, event := range e.snapshot() {
				selected = selected || (event.Kind == "selected" && event.Provider == "fixture" && event.Server == fallback.address)
			}
			if !selected {
				t.Fatalf("selected server not reported: %+v", e.snapshot())
			}
		})
	}
}

func TestCancellationDoesNotTryFallbackOrChangePreference(t *testing.T) {
	f := serveDns(t)
	f.dropUdp.Store(true)
	d := newDialer(f.resolver(), nil, time.Second)
	e := new(events)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := d.dial(e.context(ctx), "tcp4", "cancelled.test.:443")
	if err == nil || ctx.Err() == nil || f.tcp.Load() != 0 || d.current().index != 0 {
		t.Fatalf("err=%v ctx=%v tcp=%d preferred=%v", err, ctx.Err(), f.tcp.Load(), d.current())
	}
	for _, event := range e.snapshot() {
		if event.Kind != "failure" {
			t.Fatalf("unexpected fallback or selection: %+v", event)
		}
	}
}

func TestWaitingForRecoveryHonorsDeadline(t *testing.T) {
	f := serveDns(t)
	f.dropUdp.Store(true)
	d := newDialer(f.resolver(), nil, 10*time.Millisecond)
	d.recovery <- struct{}{}
	defer func() { <-d.recovery }()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err := d.dial(ctx, "tcp4", "waiting.test.:443")
	if !errors.Is(err, context.DeadlineExceeded) || f.tcp.Load() != 0 {
		t.Fatalf("err=%v tcp=%d", err, f.tcp.Load())
	}
}

func TestServiceFailureAfterRecoveryStillRemembersSuccessfulDns(t *testing.T) {
	f := serveDns(t)
	f.dropUdp.Store(true)
	d := newDialer(f.resolver(), nil, 60*time.Millisecond)
	_, err := d.dial(context.Background(), "tcp4", "service.test.:0")
	if err == nil || retryable(err) || d.current().index != 1 {
		t.Fatalf("service error=%v preferred=%v", err, d.current())
	}
	udp := f.udp.Load()
	connect(t, d, context.Background(), "working.test.", serveTarget(t))
	if f.udp.Load() != udp {
		t.Fatal("connection failure discarded a successful DNS strategy")
	}
}

func TestNxdomainAndServiceFailureDoNotTriggerDnsFallback(t *testing.T) {
	for _, negative := range []bool{false, true} {
		t.Run(strconv.FormatBool(negative), func(t *testing.T) {
			f := serveDns(t)
			f.negative.Store(negative)
			d := newDialer(f.resolver(), nil, time.Second)
			e := new(events)
			_, err := d.dial(e.context(context.Background()), "tcp4", "absent.test.:0")
			if err == nil || f.tcp.Load() != 0 || d.current().index != 0 {
				t.Fatalf("err=%v tcp=%d preferred=%v", err, f.tcp.Load(), d.current())
			}
			for _, event := range e.snapshot() {
				if event.Kind != "failure" {
					t.Fatalf("unexpected DNS fallback: %+v", event)
				}
			}
		})
	}
}

func TestConcurrentRecoverySharesStrategyAndLeavesGateUsable(t *testing.T) {
	f, port := serveDns(t), serveTarget(t)
	f.dropUdp.Store(true)
	d := newDialer(f.resolver(), nil, 100*time.Millisecond)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, err := d.dial(ctx, "tcp4", net.JoinHostPort("concurrent"+strconv.Itoa(i)+".test.", port))
			if err != nil {
				t.Error(err)
				return
			}
			c.Close()
		})
	}
	wg.Wait()
	if d.current().index != 1 || f.tcp.Load() != 20 || len(d.recovery) != 0 {
		t.Fatalf("preferred=%v tcp=%d gate=%d", d.current(), f.tcp.Load(), len(d.recovery))
	}
	udp := f.udp.Load()
	connect(t, d, context.Background(), "after.test.", port)
	if f.udp.Load() != udp {
		t.Fatal("successful strategy was not shared")
	}
}

func TestEmbeddedCatalogContainsOnlyNumericPlainDns(t *testing.T) {
	providers := fallbackProviders()
	if len(providers) == 0 {
		t.Fatal("no fallback providers")
	}
	for _, p := range providers {
		for _, excluded := range []string{"Yandex", "NSDI", "MSK-IX", "Comss", "SkyDNS", "AdGuard", "Gcore"} {
			if strings.EqualFold(p.Name, excluded) {
				t.Fatalf("excluded provider %s", p.Name)
			}
		}
		for _, server := range p.Servers {
			host, port, err := net.SplitHostPort(server)
			if err != nil || port != "53" || net.ParseIP(host) == nil {
				t.Fatalf("non-plain DNS endpoint %q", server)
			}
		}
	}
}
