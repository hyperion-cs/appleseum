package httpx

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/dns"
	"golang.org/x/net/dns/dnsmessage"
)

func TestDefaultDnsFallbackPreservesTlsProfilesAndPools(t *testing.T) {
	if os.Getenv("APPLESEUM_DNS_HTTPX_TEST") != "1" {
		// Isolate the system resolver override from other tests and clients.
		args := []string{os.Args[0], "-test.run=^TestDefaultDNSFallbackPreservesTLSProfilesAndPools$"}
		if emulator := os.Getenv("APPLESEUM_TEST_EXEC"); emulator != "" {
			args = append([]string{emulator}, args...)
		}
		command := exec.Command(args[0], args[1:]...)
		command.Env = append(os.Environ(), "APPLESEUM_DNS_HTTPX_TEST=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("DNS/TLS integration: %v\n%s", err, output)
		}
		return
	}
	var udp, tcp atomic.Int32
	net.DefaultResolver.PreferGo = true

	net.DefaultResolver.Dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		if network == "udp" {
			udp.Add(1)
			return nil, &net.OpError{Op: "dial", Net: network, Err: os.ErrDeadlineExceeded}
		}
		tcp.Add(1)
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			var prefix [2]byte
			if _, err := io.ReadFull(server, prefix[:]); err != nil {
				return
			}
			data := make([]byte, binary.BigEndian.Uint16(prefix[:]))
			if _, err := io.ReadFull(server, data); err != nil {
				return
			}
			var request dnsmessage.Message
			if err := request.Unpack(data); err != nil {
				t.Error(err)
				return
			}
			response := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, RecursionAvailable: true}, Questions: request.Questions}
			for _, q := range request.Questions {
				if q.Type == dnsmessage.TypeA {
					response.Answers = append(response.Answers, dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: dnsmessage.ClassINET}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}})
				}
			}
			data, err := response.Pack()
			if err != nil {
				t.Error(err)
				return
			}
			server.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(data))), data...))
		}()
		return client, nil
	}

	server, connections := localServer(t, true, func(w http.ResponseWriter, r *http.Request) {
		if r.TLS.ServerName != "example.com" || r.ProtoMajor != 2 {
			t.Errorf("SNI=%q protocol=%s", r.TLS.ServerName, r.Proto)
		}
		io.WriteString(w, "ok")
	})
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	url := "https://example.com:" + port
	var failures, selected atomic.Int32
	ctx := dns.WithObserver(context.Background(), func(event dns.Event) {
		if event.Kind == "failure" {
			failures.Add(1)
		}
		if event.Kind == "selected" && event.Strategy == "system-tcp" {
			selected.Add(1)
		}
	})
	client := localClient(t, server)
	for range 2 {
		poolRequest(t, client, ctx, url, Accounts)
	}
	if udp.Load() == 0 || tcp.Load() == 0 || connections.Load() != 1 || failures.Load() != 1 || selected.Load() != 1 {
		t.Fatalf("udp=%d tcp=%d connections=%d failures=%d selected=%d", udp.Load(), tcp.Load(), connections.Load(), failures.Load(), selected.Load())
	}
	udpBefore, tcpBefore := udp.Load(), tcp.Load()
	// A separate profile has its own pool but reuses the remembered DNS strategy.
	poolRequest(t, client, ctx, url, Configurator)
	if udp.Load() != udpBefore || tcp.Load() <= tcpBefore || connections.Load() != 2 || failures.Load() != 1 {
		t.Fatalf("strategy or pool isolation lost: udp=%d tcp=%d connections=%d failures=%d", udp.Load(), tcp.Load(), connections.Load(), failures.Load())
	}
}
