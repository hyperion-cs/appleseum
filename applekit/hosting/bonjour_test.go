package hosting

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	bonjourTestTimeout = 200 * time.Millisecond
)

type bonjourFixture struct {
	name string
	ipv4 [4]byte
	ipv6 [16]byte
}

func serveBonjour(t *testing.T, fixture bonjourFixture) string {
	t.Helper()
	connection, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	go func() {
		packet := make([]byte, bonjourPacketBytes)
		for {
			n, peer, err := connection.ReadFromUDP(packet)
			if err != nil {
				return
			}
			var query dnsmessage.Message
			if query.Unpack(packet[:n]) != nil || len(query.Questions) != 1 {
				continue
			}
			q := query.Questions[0]
			response := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true}, Questions: query.Questions}
			header := dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: dnsmessage.ClassINET}
			var body dnsmessage.ResourceBody
			switch q.Type {
			case dnsmessage.TypePTR:
				body = &dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(fixture.name)}
			case dnsmessage.TypeA:
				if fixture.ipv4 != [4]byte{} {
					body = &dnsmessage.AResource{A: fixture.ipv4}
				}
			case dnsmessage.TypeAAAA:
				if fixture.ipv6 != [16]byte{} {
					body = &dnsmessage.AAAAResource{AAAA: fixture.ipv6}
				}
			}
			if body != nil {
				response.Answers = []dnsmessage.Resource{{Header: header, Body: body}}
			}
			encoded, err := response.Pack()
			if err == nil {
				connection.WriteToUDP(encoded, peer)
			}
		}
	}()
	return connection.LocalAddr().String()
}

func TestBonjourLookup(t *testing.T) {
	for _, test := range []struct {
		name    string
		fixture bonjourFixture
		want    string
	}{
		{name: "lowercase IPv4", fixture: bonjourFixture{name: "Phone-Example.LOCAL.", ipv4: [4]byte{127, 0, 0, 1}}, want: "phone-example.local"},
		{name: "IPv6 only", fixture: bonjourFixture{name: "tablet.local.", ipv6: [16]byte{15: 1}}, want: "tablet.local"},
		{name: "foreign IP", fixture: bonjourFixture{name: "phone.local.", ipv4: [4]byte{203, 0, 113, 5}}},
		{name: "external name", fixture: bonjourFixture{name: "phone.example."}},
		{name: "empty addresses", fixture: bonjourFixture{name: "phone.local."}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), bonjourTestTimeout)
			defer cancel()
			name, err := lookupBonjour(ctx, serveBonjour(t, test.fixture))
			if name != test.want || (err == nil) != (test.want != "") {
				t.Fatalf("lookup = %q, %v; want %q", name, err, test.want)
			}
		})
	}
}

func TestBonjourCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	name, err := BonjourName(ctx)
	if name != "" || err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("canceled lookup = %q, %v", name, err)
	}
}

func TestBonjourSilentResponderTimesOut(t *testing.T) {
	silent, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	start := time.Now()
	name, err := lookupBonjour(context.Background(), silent.LocalAddr().String())
	if elapsed := time.Since(start); name != "" || err == nil || elapsed > bonjourTimeout+time.Second {
		t.Fatalf("silent responder: %q, %v after %s", name, err, elapsed)
	}
}
