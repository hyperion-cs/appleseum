// Package dns dials through the system resolver, with DNS transport fallbacks for iSH.
// Go handles parsing, hosts files and address selection.
// Only the successful strategy is cached, not IP addresses.
package dns

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http/httptrace"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	DefaultTcpKeepAlive = 30 * time.Second
)

const (
	defaultLookupTimeout = 2 * time.Second
	dialTimeout          = 30 * time.Second
)

//go:embed resolvers.json
var catalog []byte

type provider struct {
	Name    string   `json:"name"`
	Servers []string `json:"servers"`
}

type strategy struct {
	name, provider, server, network string
	resolver                        *net.Resolver
}

type preference struct {
	index      int
	generation uint64
}

type dialer struct {
	strategies []strategy
	budget     time.Duration
	mu         sync.Mutex
	preferred  preference
	recovery   chan struct{}
}

var shared = newDialer(net.DefaultResolver, fallbackProviders(), defaultLookupTimeout)

func fallbackProviders() []provider {
	var c struct {
		Providers []provider `json:"providers"`
	}
	if err := json.Unmarshal(catalog, &c); err != nil {
		panic(fmt.Errorf("embedded DNS catalog: %w", err))
	}
	for _, p := range c.Providers {
		for _, server := range p.Servers {
			if _, err := netip.ParseAddrPort(server); err != nil {
				panic(fmt.Errorf("embedded DNS server: %w", err))
			}
		}
	}
	return c.Providers
}

func newDialer(system *net.Resolver, providers []provider, budget time.Duration) *dialer {
	d := &dialer{budget: budget, recovery: make(chan struct{}, 1)}
	d.strategies = append(d.strategies, strategy{name: "system", provider: "system", network: "system", resolver: system})
	d.strategies = append(d.strategies, strategy{name: "system-tcp", provider: "system", network: "tcp", resolver: wireResolver("tcp", "", system)})
	// Visit different providers before trying their secondary addresses.
	for address := 0; ; address++ {
		added := false
		for _, p := range providers {
			if address >= len(p.Servers) {
				continue
			}
			added = true
			for _, network := range []string{"udp", "tcp"} {
				server := p.Servers[address]
				d.strategies = append(d.strategies, strategy{
					name: p.Name + "/" + server + "/" + network, provider: p.Name, server: server, network: network,
					resolver: wireResolver(network, server, nil),
				})
			}
		}
		if !added {
			break
		}
	}
	return d
}

// wireResolver changes DNS transport only. In particular, returning a TCPConn
// makes the standard resolver handle DNS-over-TCP framing itself.
func wireResolver(network, server string, system *net.Resolver) *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, requested, configured string) (net.Conn, error) {
		if server != "" {
			configured = server
		}
		wire := network
		if requested == "tcp" { // Honor UDP truncation and the resolver's TCP retry.
			wire = "tcp"
		}
		if state, _ := ctx.Value(lookupKey{}).(*lookupState); state != nil {
			state.record(wire, configured)
		}
		if system != nil && system.Dial != nil {
			return system.Dial(ctx, wire, configured)
		}
		return (&net.Dialer{}).DialContext(ctx, wire, configured)
	}}
}

func (d *dialer) current() preference {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.preferred
}

func (d *dialer) remember(ctx context.Context, host string, index int, observed preference) {
	d.mu.Lock()
	previous := d.preferred.index
	changed := d.preferred == observed && previous != index
	if changed {
		d.preferred = preference{index: index, generation: observed.generation + 1}
	}
	d.mu.Unlock()
	if changed {
		s := d.strategies[index]
		emit(ctx, Event{Kind: "selected", Host: host, Strategy: s.name, Previous: d.strategies[previous].name, Provider: s.provider, Server: s.server, Network: s.network})
	}
}

// DialContext shares the selected strategy across all default Apple clients.
// Caller cancellation, service connection failures and NXDOMAIN never trigger
// a DNS fallback. An expired caller deadline is never extended.
func DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return shared.dial(ctx, network, address)
}

func (d *dialer) dial(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	host, _, err := net.SplitHostPort(address)
	_, literalErr := netip.ParseAddr(host)
	if err != nil || literalErr == nil || (network != "tcp" && network != "tcp4" && network != "tcp6") {
		return (&net.Dialer{KeepAlive: DefaultTcpKeepAlive}).DialContext(ctx, network, address)
	}
	observed := d.current()
	tried := make(map[int]bool)
	var releaseOnce sync.Once

	release := func() { releaseOnce.Do(func() { <-d.recovery }) }

	try := func(index int, state preference, recovering bool) (net.Conn, error) {
		tried[index] = true
		return d.attempt(ctx, network, address, host, index, func() {
			d.remember(ctx, host, index, state)
			if recovering {
				release() // DNS is ready; do not hold the gate during a service connection.
			}
		})
	}

	conn, err := try(observed.index, observed, false)
	if !retryable(err) || ctx.Err() != nil {
		return conn, err
	}
	// Only one request explores fallbacks at a time. Waiting callers recheck the
	// selected strategy, while ordinary successful lookups remain concurrent.
	select {
	case d.recovery <- struct{}{}:
		defer release()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	state := d.current()
	order := []int{}
	if state != observed {
		order = append(order, state.index)
	}
	for index := range d.strategies {
		if !tried[index] && !slices.Contains(order, index) {
			order = append(order, index)
		}
	}
	previous := observed.index
	for _, index := range order {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s := d.strategies[index]
		emit(ctx, Event{Kind: "fallback", Host: host, Strategy: s.name, Previous: d.strategies[previous].name,
			Provider: s.provider, Server: s.server, Network: s.network, VpnPossible: previous == 0 && index == 1})
		conn, err = try(index, d.current(), true)
		if !retryable(err) || ctx.Err() != nil {
			return conn, err
		}
		previous = index
	}
	return nil, err
}

func retryable(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && !dnsErr.IsNotFound
}

var errLookupTimeout = errors.New("DNS attempt timed out")

type lookupKey struct{}
type lookupState struct {
	mu       sync.Mutex
	servers  []string
	networks []string
	timer    *time.Timer
	finished bool
}

func (s *lookupState) record(network, server string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(s.servers, server) {
		s.servers = append(s.servers, server)
	}
	if !slices.Contains(s.networks, network) {
		s.networks = append(s.networks, network)
	}
}

func (s *lookupState) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished = true
	if s.timer != nil {
		s.timer.Stop()
	}
}

func (d *dialer) attempt(ctx context.Context, network, address, host string, index int, ready func()) (net.Conn, error) {
	s := d.strategies[index]
	state := new(lookupState)
	attemptCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	defer state.stop()

	normalize := func(err error) error {
		if errors.Is(context.Cause(attemptCtx), errLookupTimeout) {
			return &net.DNSError{Name: host, Server: s.server, Err: errLookupTimeout.Error(), IsTimeout: true, IsTemporary: true, UnwrapErr: context.DeadlineExceeded}
		}
		return err
	}

	start := time.Now()
	attemptCtx = context.WithValue(attemptCtx, lookupKey{}, state)
	attemptCtx = httptrace.WithClientTrace(attemptCtx, &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) {
			state.mu.Lock()
			state.timer = time.AfterFunc(d.budget, func() {
				state.mu.Lock()
				defer state.mu.Unlock()
				if !state.finished {
					cancel(errLookupTimeout)
				}
			})
			state.mu.Unlock()
		},
		DNSDone: func(info httptrace.DNSDoneInfo) {
			state.stop()
			err := normalize(info.Err)
			if err != nil {
				state.mu.Lock()
				server, wire := strings.Join(state.servers, ","), strings.Join(state.networks, ",")
				state.mu.Unlock()
				if server == "" {
					var dnsErr *net.DNSError
					if errors.As(err, &dnsErr) {
						server = dnsErr.Server
					}
				}
				if wire == "" {
					wire = s.network
				}
				emit(ctx, Event{Kind: "failure", Host: host, Strategy: s.name, Provider: s.provider, Server: server, Network: wire, Duration: time.Since(start), Err: err})
			}
			if ctx.Err() == nil && (err == nil || !retryable(err)) {
				ready()
			}
		},
	})
	conn, err := (&net.Dialer{Resolver: s.resolver, KeepAlive: DefaultTcpKeepAlive}).DialContext(attemptCtx, network, address)
	if err != nil && errors.Is(context.Cause(attemptCtx), errLookupTimeout) {
		return nil, &net.OpError{Op: "dial", Net: network, Err: normalize(err)}
	}
	return conn, err
}
