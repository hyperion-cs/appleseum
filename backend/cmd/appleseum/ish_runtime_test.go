package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestIshRuntimeDefaultsPreserveEnvironmentAndExplicitSettings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		before  []string
		gomax   string
		debug   string
		changed bool
	}{
		{"defaults", []string{"PATH=/bin", "TOKEN=private"}, "2", "madvdontneed=0,asyncpreemptoff=1", true},
		{"retain diagnostics", []string{"GODEBUG=gctrace=1", "GOMAXPROCS=1"}, "1", "gctrace=1,madvdontneed=0,asyncpreemptoff=1", true},
		{"complete", []string{"GOMAXPROCS=2", "GODEBUG=asyncpreemptoff=1,madvdontneed=0"}, "2", "asyncpreemptoff=1,madvdontneed=0", false},
		{"explicit overrides", []string{"GOMAXPROCS=6", "GODEBUG=madvdontneed=1,asyncpreemptoff=0,gctrace=1"}, "6", "madvdontneed=1,asyncpreemptoff=0,gctrace=1", false},
		{"empty means default", []string{"GOMAXPROCS=", "GODEBUG="}, "2", "madvdontneed=0,asyncpreemptoff=1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]string(nil), tc.before...)
			after, changed := runtimeEnvironmentForIsh(tc.before)
			if changed != tc.changed || !reflect.DeepEqual(tc.before, before) {
				t.Fatalf("changed=%t original modified=%t", changed, !reflect.DeepEqual(tc.before, before))
			}
			for _, want := range []string{"GOMAXPROCS=" + tc.gomax, "GODEBUG=" + tc.debug} {
				found := false
				for _, entry := range after {
					found = found || entry == want
				}
				if !found {
					t.Fatalf("missing %q in %v", want, after)
				}
			}
			for _, entry := range before {
				if !strings.HasPrefix(entry, "GOMAXPROCS=") && !strings.HasPrefix(entry, "GODEBUG=") {
					found := false
					for _, got := range after {
						found = found || got == entry
					}
					if !found {
						t.Fatal("lost unrelated environment variable")
					}
				}
			}
			again, needsRestart := runtimeEnvironmentForIsh(after)
			if needsRestart || !reflect.DeepEqual(after, again) {
				t.Fatal("compatibility settings would cause repeated restarts")
			}
		})
	}
}
