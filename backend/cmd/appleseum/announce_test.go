package main

import (
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
)

func TestAnnounceBonjour(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "Bonjour first", true: "unavailable remains usable"}[unavailable], func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			original := os.Stdout
			os.Stdout = writer
			defer func() { os.Stdout = original }()
			addresses := hostInventory{console: []net.IP{net.IPv4(127, 0, 0, 1)}, bonjour: "phone-example.local"}
			if unavailable {
				addresses.bonjour, addresses.bonjourErr = "", errors.New("no Bonjour name resolving to loopback")
			}
			announce(options{http: "0.0.0.0:8080", https: "0.0.0.0:8443"}, nil, addresses)
			writer.Close()
			body, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			output := string(body)
			if unavailable {
				if !strings.Contains(output, "Same-device installation unavailable.") || !strings.Contains(output, "--localhost-resolver=nip") || !strings.Contains(output, "http://127.0.0.1:8080") {
					t.Fatal(output)
				}
			} else if !strings.Contains(output, "Available at:\n  - http://phone-example.local:8080\n  - http://127.0.0.1:8080") {
				t.Fatal(output)
			}
		})
	}
}
