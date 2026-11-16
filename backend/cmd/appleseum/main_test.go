package main

import (
	"testing"
)

func TestListenAddress(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"8080", "0.0.0.0:8080"},
		{"8000", "0.0.0.0:8000"},
		{"8443", "0.0.0.0:8443"},
		{"65535", "0.0.0.0:65535"},
		{"0.0.0.0:8080", "0.0.0.0:8080"},
		{"127.0.0.1:8000", "127.0.0.1:8000"},
		{":8443", ":8443"},
	} {
		t.Run(tt.input, func(t *testing.T) {
			got, err := listenAddress(tt.input)
			if err != nil || got != tt.want {
				t.Fatalf("listenAddress(%q) = %q, %v; want %q", tt.input, got, err, tt.want)
			}
		})
	}
	for _, input := range []string{"", "65536", "-1", "8080x", "localhost", "127.0.0.1:8000:8443"} {
		t.Run(input, func(t *testing.T) {
			if _, err := listenAddress(input); err == nil {
				t.Fatalf("listenAddress(%q) accepted an invalid address", input)
			}
		})
	}
}
