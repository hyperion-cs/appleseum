package httpx

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

func serializeHello(t *testing.T, profile Profile, host string) []byte {
	t.Helper()
	client, server := net.Pipe()
	defer server.Close()
	uconn, err := profile.connection(client, host)
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	defer uconn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); _ = uconn.HandshakeContext(ctx) }()
	defer func() { client.Close(); <-done }()
	server.SetReadDeadline(time.Now().Add(3 * time.Second))
	header := make([]byte, 5)
	if _, err := io.ReadFull(server, header); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, binary.BigEndian.Uint16(header[3:]))
	if _, err := io.ReadFull(server, body); err != nil {
		t.Fatal(err)
	}
	return append(header, body...)
}

type helloExtension struct {
	id   uint16
	data []byte
}

type parsedHello struct {
	header      []byte
	random      []byte
	session     []byte
	ciphers     []byte
	compression []byte
	extensions  []helloExtension
}

// Parse wire bytes independently of uTLS so the comparison covers its serialization.
func parseHello(t *testing.T, record []byte) parsedHello {
	t.Helper()
	if len(record) < 44 || record[0] != 22 || record[5] != 1 {
		t.Fatal("invalid ClientHello record")
	}
	if int(binary.BigEndian.Uint16(record[3:5])) != len(record)-5 {
		t.Fatal("invalid record length")
	}
	body := record[9:]
	h := parsedHello{header: append(bytes.Clone(record[:3]), body[:2]...), random: bytes.Clone(body[2:34])}
	p := 34

	vector := func(width int) []byte {
		t.Helper()
		if p+width > len(body) {
			t.Fatal("short vector length")
		}
		n := int(body[p])
		if width == 2 {
			n = int(binary.BigEndian.Uint16(body[p:]))
		}
		p += width
		if p+n > len(body) {
			t.Fatal("short vector")
		}
		v := bytes.Clone(body[p : p+n])
		p += n
		return v
	}

	h.session = vector(1)
	h.ciphers = vector(2)
	h.compression = vector(1)
	ext := vector(2)
	if p != len(body) {
		t.Fatal("trailing ClientHello bytes")
	}
	for len(ext) > 0 {
		if len(ext) < 4 {
			t.Fatal("short extension header")
		}
		id, n := binary.BigEndian.Uint16(ext), int(binary.BigEndian.Uint16(ext[2:]))
		if n+4 > len(ext) {
			t.Fatal("short extension data")
		}
		h.extensions = append(h.extensions, helloExtension{id, bytes.Clone(ext[4 : 4+n])})
		ext = ext[4+n:]
	}
	return h
}

func grease(v uint16) bool { return v&0x0f0f == 0x0a0a && byte(v>>8) == byte(v) }

func normalizeWords(data []byte) {
	for i := 0; i+1 < len(data); i += 2 {
		if grease(binary.BigEndian.Uint16(data[i:])) {
			data[i], data[i+1] = 0x0a, 0x0a
		}
	}
}

func (h parsedHello) normalized(t *testing.T) parsedHello {
	t.Helper()
	h.random = make([]byte, len(h.random))
	h.session = make([]byte, len(h.session))
	normalizeWords(h.ciphers)
	for i := range h.extensions {
		ext := &h.extensions[i]
		if grease(ext.id) {
			ext.id = 0x0a0a
		}
		switch ext.id {
		case 0: // SNI varies with the requested host.
			ext.data = nil
		case 10: // supported_groups
			normalizeWords(ext.data[2:])
		case 43: // supported_versions
			normalizeWords(ext.data[1:])
		case 51: // key_share: preserve group ordering and key lengths.
			for p := 2; p < len(ext.data); {
				if p+4 > len(ext.data) {
					t.Fatal("short key share")
				}
				group, n := binary.BigEndian.Uint16(ext.data[p:]), int(binary.BigEndian.Uint16(ext.data[p+2:]))
				if p+4+n > len(ext.data) {
					t.Fatal("short key data")
				}
				if grease(group) {
					ext.data[p], ext.data[p+1] = 0x0a, 0x0a
				}
				if !grease(group) {
					clear(ext.data[p+4 : p+4+n])
				}
				p += 4 + n
			}
		}
	}
	return h
}

func TestSyntheticClientHelloProfiles(t *testing.T) {
	for _, tc := range []struct {
		profile   Profile
		reference string
	}{
		{Configurator, "configurator_hello.bin"}, {Accounts, "accounts_hello.bin"}, {NativeAuthentication, "accounts_hello.bin"},
		{AuthKit, "authkit_hello.bin"}, {AuthKitPostData, "authkit_postdata_hello.bin"},
		{AmsAccounts, "amsaccounts_hello.bin"}, {MacAppStore, "macappstore_hello.bin"},
	} {
		p := tc.profile
		t.Run(fmt.Sprint(p), func(t *testing.T) {
			t.Parallel()
			reference, err := os.ReadFile("testdata/hello/" + tc.reference)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := (&utls.Fingerprinter{}).FingerprintClientHello(reference)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := p.clientHelloSpec()
			if err != nil || spec.TLSVersMin != expected.TLSVersMin || spec.TLSVersMax != expected.TLSVersMax {
				t.Fatal("TLS negotiation limits differ from synthetic fixture", err)
			}
			first := parseHello(t, serializeHello(t, p, "first.example.test"))
			second := parseHello(t, serializeHello(t, p, "second.example.test"))
			synthetic := parseHello(t, reference)
			if bytes.Equal(first.random, second.random) || bytes.Equal(first.session, second.session) {
				t.Fatal("connection random or session ID reused")
			}
			if !bytes.Equal(first.header, synthetic.header) {
				t.Fatal("record or legacy version changed")
			}
			for i, ext := range first.extensions {
				switch ext.id {
				case 0:
					if !bytes.Contains(ext.data, []byte("first.example.test")) {
						t.Fatal("SNI was not replaced")
					}
				case 51:
					fresh := keyShares(t, ext.data)
					other := keyShares(t, second.extensions[i].data)
					saved := keyShares(t, synthetic.extensions[i].data)
					for group, key := range fresh {
						if bytes.Equal(key, other[group]) || bytes.Equal(key, saved[group]) {
							t.Fatalf("public key share %d reused", group)
						}
					}
					if bytes.Equal(fresh[4_588][1_184:], fresh[29]) {
						t.Fatal("classical and hybrid key shares reused the same X25519 key")
					}
				}
			}
			if !reflect.DeepEqual(first.normalized(t), synthetic.normalized(t)) {
				t.Errorf("serialized ClientHello differs from synthetic fixture\ngot:  %#v\nwant: %#v", first.normalized(t), synthetic.normalized(t))
			}
		})
	}
}

func TestProfileUserAgents(t *testing.T) {
	for _, tc := range []struct {
		profile Profile
		want    string
	}{
		{Configurator, DefaultConfiguratorUserAgent}, {Accounts, DefaultAccountsUserAgent},
		{NativeAuthentication, DefaultConfiguratorUserAgent}, {AuthKit, DefaultAuthKitUserAgent},
		{AuthKitPostData, DefaultAuthKitUserAgent}, {AmsAccounts, DefaultAmsAccountsUserAgent},
		{MacAppStore, "MacAppStore/3.0 MacAppStore/3.0" + DefaultMacUserAgentSuffix},
	} {
		if got := UserAgentFor(WithProfile(context.Background(), tc.profile)); got != tc.want {
			t.Errorf("profile %d: got %q", tc.profile, got)
		}
	}
	if _, err := Profile(255).clientHelloSpec(); err == nil {
		t.Fatal("unknown profile accepted")
	}
}

func keyShares(t *testing.T, data []byte) map[uint16][]byte {
	t.Helper()
	shares := make(map[uint16][]byte)
	for p := 2; p < len(data); {
		if p+4 > len(data) {
			t.Fatal("short key share")
		}
		group, n := binary.BigEndian.Uint16(data[p:]), int(binary.BigEndian.Uint16(data[p+2:]))
		if p+4+n > len(data) {
			t.Fatal("short key data")
		}
		if !grease(group) {
			shares[group] = data[p+4 : p+4+n]
		}
		p += 4 + n
	}
	return shares
}
