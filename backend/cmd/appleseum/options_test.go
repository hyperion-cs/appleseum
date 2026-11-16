package main

import (
	"errors"
	"flag"
	"io"
	"slices"
	"testing"
)

func TestParseOptions(t *testing.T) {
	o, err := parseOptions([]string{"--http", "8080", "--https", "127.0.0.1:8443", "--settings"})
	if err != nil || o.http != "0.0.0.0:8080" || o.https != "127.0.0.1:8443" || !o.settings || o.resolver != "bonjour" {
		t.Fatalf("local options = %+v, %v", o, err)
	}
	if !slices.Equal(o.addresses(), []string{"0.0.0.0:8080", "127.0.0.1:8443"}) {
		t.Fatalf("local listeners = %v", o.addresses())
	}
	o, err = parseOptions([]string{"--public", "--shared-library", "--http", "127.0.0.1:9000"})
	if err != nil || o.https != "" || !slices.Equal(o.addresses(), []string{"127.0.0.1:9000"}) || port(o.https) != "" {
		t.Fatalf("public options = %+v, %v", o, err)
	}
	for _, args := range [][]string{
		{"--public", "--debug"},
		{"--public", "--https", "8443"},
		{"--shared-library"},
		{"--http", "localhost:80"},
		{"--https", "70000"},
		{"extra"},
		{"--localhost-resolver", "other"},
		{"--localhost-resolver", ""},
		{"--data", ""},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Fatalf("parseOptions(%q) accepted invalid options", args)
		}
	}
	if _, err := parseOptions([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help = %v", err)
	}
	o, err = parseOptions([]string{"--localhost-resolver=nip"})
	if err != nil || o.resolver != "nip" {
		t.Fatalf("nip options = %+v, %v", o, err)
	}
	usage(io.Discard)
}

func TestPublicDebugOptions(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		want  debugMode
		valid bool
	}{
		{nil, "", true},
		{[]string{"--debug=raw"}, debugRaw, true},
		{[]string{"--debug=raw", "--shared-library"}, debugRaw, true},
		{[]string{"--debug=false"}, "", true},
		{[]string{"--debug"}, "", false},
		{[]string{"--debug=web"}, "", false},
		{[]string{"--debug=true"}, "", false},
		{[]string{"--debug=raw", "--https", "8443"}, "", false},
	} {
		args := append([]string{"--public"}, tc.args...)
		o, err := parseOptions(args)
		if (err == nil) != tc.valid {
			t.Fatalf("parseOptions(%q): %v", args, err)
		}
		if tc.valid && (o.debug != tc.want || !o.public || o.https != "") {
			t.Fatalf("parseOptions(%q) = %+v", args, o)
		}
	}
}
