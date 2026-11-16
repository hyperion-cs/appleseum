package main

import (
	"flag"
	"io"
	"testing"
)

func TestDebugModes(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		want  debugMode
		valid bool
	}{
		{nil, "", true},
		{[]string{"--debug"}, debugWeb, true},
		{[]string{"--debug=web"}, debugWeb, true},
		{[]string{"--debug=raw"}, debugRaw, true},
		{[]string{"--debug=true"}, debugWeb, true},
		{[]string{"--debug=false"}, "", true},
		{[]string{"--debug=2"}, "", false},
		{[]string{"--debug=invalid"}, "", false},
	} {
		flags := flag.NewFlagSet("test", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		var mode debugMode
		flags.Var(&mode, "debug", "")
		err := flags.Parse(tc.args)
		if (err == nil) != tc.valid || tc.valid && mode != tc.want {
			t.Fatalf("%v: mode=%q err=%v", tc.args, mode, err)
		}
	}
}
