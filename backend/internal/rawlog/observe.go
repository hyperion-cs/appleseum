// Package rawlog provides opt-in process and network diagnostics.
// Request diagnostics omit bodies, headers and query strings.
package rawlog

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"
)

func Enabled() bool { return current.Load() != nil }

func Log(format string, args ...any) {
	if r := current.Load(); r != nil {
		r.log(format, args...)
	}
}

// Error reports classifications without leaking URLs, credentials or bodies.
func Error(err error) string {
	if err == nil {
		return "none"
	}
	var u *url.Error
	if errors.As(err, &u) {
		return Error(u.Err)
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return fmt.Sprintf("dns name=%q server=%q timeout=%t temporary=%t not_found=%t cause=%s", dns.Name, dns.Server, dns.IsTimeout, dns.IsTemporary, dns.IsNotFound, Error(dns.Unwrap()))
	}
	var operation *net.OpError
	if errors.As(err, &operation) {
		return fmt.Sprintf("operation=%s network=%s cause=%s", operation.Op, operation.Net, Error(operation.Err))
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	var call *os.SyscallError
	if errors.As(err, &call) {
		return fmt.Sprintf("syscall=%s errno=%v", call.Syscall, call.Err)
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return fmt.Sprintf("errno=%d (%s)", errno, errno)
	}
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		return fmt.Sprintf("%T code=%d", err, coded.Code())
	}
	var n net.Error
	if errors.As(err, &n) {
		return fmt.Sprintf("%T timeout=%t", err, n.Timeout())
	}
	return fmt.Sprintf("%T", err)
}

func Begin(name string) func() {
	r := current.Load()
	if r == nil {
		return func() {}
	}
	id := r.sequence.Add(1)
	op := operation{name, time.Now()}
	r.pending.Store(id, op)
	r.log("begin #%d %s", id, name)
	return func() { r.pending.Delete(id); r.log("end #%d %s duration=%s", id, name, time.Since(op.start)) }
}

func Call(name string, f func() error) error {
	end := Begin(name)
	defer end()
	err := f()
	Log("result %s error=%s", name, Error(err))
	return err
}

func (r *recorder) start() {
	r.log("raw logging started pid=%d runtime=%s target=%s/%s cpus=%d gomaxprocs=%d", os.Getpid(), runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.GOMAXPROCS(0))
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, v := range info.Settings {
			if strings.HasPrefix(v.Key, "vcs.") || v.Key == "GO386" {
				r.log("build %s=%s", v.Key, v.Value)
			}
		}
	}
	r.log("forced runtime stops disabled; GODEBUG=%q", os.Getenv("GODEBUG"))
	go r.heartbeat()
	if runtime.GOOS == "linux" {
		Watch(r.ctx, func() {
			end := Begin("snapshot file descriptors")
			defer end()
			entries, err := os.ReadDir("/proc/self/fd")
			Log("file descriptors count=%d error=%s", len(entries), Error(err))
		})
	}
	go func() {
		for _, path := range []string{"/proc/ish/version", "/proc/version", "/proc/mounts", "/etc/resolv.conf"} {
			if runtime.GOOS != "linux" || r.ctx.Err() != nil {
				return
			}
			end := Begin("environment " + path)
			f, err := os.Open(path)
			if err == nil {
				data, readErr := io.ReadAll(io.LimitReader(f, 2_048))
				f.Close()
				r.log("environment %s value=%q error=%s", path, data, Error(readErr))
			} else {
				r.log("environment %s error=%s", path, Error(err))
			}
			end()
		}
	}()
}

func Watch(ctx context.Context, sample func()) {
	r := current.Load()
	if r == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-r.ctx.Done():
				return
			case <-ticker.C:
				sample()
			}
		}
	}()
}

func (r *recorder) heartbeat() {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	last := time.Now()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-tick.C:
		}
		now := time.Now()
		counts := make(map[http.ConnState]int)
		r.connections.Range(func(_, value any) bool { counts[value.(http.ConnState)]++; return true })
		var ops []string
		r.pending.Range(func(key, value any) bool {
			op := value.(operation)
			ops = append(ops, fmt.Sprintf("#%d %s age=%s", key, op.name, now.Sub(op.start).Round(time.Millisecond)))
			return true
		})
		sort.Strings(ops)
		Log("heartbeat gap=%s goroutines=%d accepted=%d closed=%d new=%d active=%d idle=%d pending=%d", now.Sub(last), runtime.NumGoroutine(), r.accepted.Load(), r.closed.Load(), counts[http.StateNew], counts[http.StateActive], counts[http.StateIdle], len(ops))
		for i, op := range ops {
			if i == 30 {
				Log("remaining pending operations omitted=%d", len(ops)-i)
				break
			}
			Log("pending %s", op)
		}
		last = now
	}
}

// Unlike ReadMemStats, this metric does not explicitly stop the world.
func HeapBytes() int64 {
	samples := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(samples)
	if samples[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return int64(samples[0].Value.Uint64())
}

func ConnState(c net.Conn, state http.ConnState) {
	r := current.Load()
	if r == nil {
		return
	}
	switch state {
	case http.StateNew:
		r.accepted.Add(1)
	case http.StateClosed, http.StateHijacked:
		r.closed.Add(1)
		r.connections.Delete(c)
		return
	}
	r.connections.Store(c, state)
}

func route(r *http.Request) string {
	switch r.URL.Path {
	case "/api/login", "/api/state", "/api/health", "/api/profile", "/api/debug":
		return r.URL.Path
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return "/api/<other>"
	}
	return "<frontend-or-file>"
}

func Handler(next http.Handler) http.Handler {
	if !Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		end := Begin("HTTP " + r.Method + " " + route(r))
		defer end()
		next.ServeHTTP(w, r)
	})
}

type traceKey struct{}

// Trace preserves the request and transport, including the Apple TLS profile.
func Trace(ctx context.Context) context.Context {
	if !Enabled() {
		return ctx
	}
	if ctx.Value(traceKey{}) != nil {
		return ctx
	}
	ctx = context.WithValue(ctx, traceKey{}, true)
	ctx = applekit.WithDnsObserver(ctx, observeDns)
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		DNSStart: func(i httptrace.DNSStartInfo) { Log("apple DNS begin host=%s", i.Host) },
		DNSDone: func(i httptrace.DNSDoneInfo) {
			Log("apple DNS end addresses=%d ips=%v error=%s", len(i.Addrs), i.Addrs[:min(len(i.Addrs), 32)], Error(i.Err))
		},
		ConnectStart: func(network, address string) { Log("apple connect begin network=%s address=%s", network, address) },
		ConnectDone: func(network, address string, err error) {
			Log("apple connect end network=%s address=%s error=%s", network, address, Error(err))
		},
		TLSHandshakeStart: func() { Log("apple TLS begin") },
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			Log("apple TLS end error=%s version=%s cipher=%s protocol=%s", Error(err), tls.VersionName(state.Version), tls.CipherSuiteName(state.CipherSuite), state.NegotiatedProtocol)
		},
		GotConn:              func(i httptrace.GotConnInfo) { Log("apple connection acquired reused=%t idle=%t", i.Reused, i.WasIdle) },
		WroteRequest:         func(i httptrace.WroteRequestInfo) { Log("apple request written error=%s", Error(i.Err)) },
		GotFirstResponseByte: func() { Log("apple first response byte") },
	})
}

func observeDns(event applekit.DnsEvent) {
	Log("apple DNS %s host=%q strategy=%q previous=%q provider=%q server=%q network=%s duration=%s error=%s",
		event.Kind, event.Host, event.Strategy, event.Previous, event.Provider, event.Server, event.Network, event.Duration, Error(event.Err))
	if event.VpnPossible {
		Log("DNS may be affected by an enabled VPN; trying the system DNS over TCP")
	}
}

// Each probe has one worker, so even an uninterruptible emulated syscall cannot
// accumulate probe goroutines. The heartbeat runs independently.
func Ready(configured string, bound net.Addr) {
	r := current.Load()
	if r == nil {
		return
	}
	if os.Getenv("APPLESEUM_RAW_PROBES") == "0" {
		Log("active local probes disabled by environment")
		return
	}
	Log("active local TCP/HTTP probes enabled: interval=15s deadline=4s")
	host, _, err := net.SplitHostPort(configured)
	if err != nil {
		Log("probe configuration error=%s", Error(err))
		return
	}
	_, port, _ := net.SplitHostPort(bound.String())
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	address := net.JoinHostPort(host, port)
	for _, path := range []string{"tcp", "/api/health", "/api/state"} {
		go func() {
			transport := &http.Transport{DisableKeepAlives: true, Proxy: nil}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 4 * time.Second}
			for {
				select {
				case <-r.ctx.Done():
					return
				case <-time.After(15 * time.Second):
				}
				end := Begin("probe " + path)
				ctx, cancel := context.WithTimeout(r.ctx, 4*time.Second)
				if path == "tcp" {
					c, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
					if c != nil {
						c.Close()
					}
					Log("probe tcp error=%s", Error(err))
				} else {
					r, _ := http.NewRequestWithContext(ctx, "GET", "http://"+address+path, nil)
					response, err := client.Do(r)
					status := 0
					if response != nil {
						status = response.StatusCode
						_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 8_192))
						response.Body.Close()
						if err == nil {
							err = readErr
						}
					}
					Log("probe %s status=%d error=%s", path, status, Error(err))
				}
				cancel()
				end()
			}
		}()
	}
}
