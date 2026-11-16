package main

import (
	"log"
	"os"
	"strings"
)

// Defaults preserve user overrides. madvdontneed=0 clears pages when iSH's madvise does not.
// Two Go processors retain parallelism; cooperative preemption avoids asynchronous signals.
func runtimeEnvironmentForIsh(environment []string) ([]string, bool) {
	environment = append([]string(nil), environment...)
	changed := false

	get := func(name string) string {
		for _, entry := range environment {
			if value, ok := strings.CutPrefix(entry, name+"="); ok {
				return value
			}
		}
		return ""
	}

	set := func(name, value string) {
		for i, entry := range environment {
			if strings.HasPrefix(entry, name+"=") {
				environment[i] = name + "=" + value
				changed = true
				return
			}
		}
		environment = append(environment, name+"="+value)
		changed = true
	}

	if get("GOMAXPROCS") == "" {
		set("GOMAXPROCS", "2")
	}
	debug := get("GODEBUG")
	for _, setting := range []string{"madvdontneed=0", "asyncpreemptoff=1"} {
		name, _, _ := strings.Cut(setting, "=")
		present := false
		for _, option := range strings.Split(debug, ",") {
			key, _, _ := strings.Cut(option, "=")
			present = present || key == name
		}
		if !present {
			if debug != "" {
				debug += ","
			}
			debug += setting
		}
	}
	if debug != get("GODEBUG") {
		set("GODEBUG", debug)
	}
	return environment, changed
}

func startIshBackground(locationPath string) func() {
	log.Print("iSH may request location access for background mode and local network access for connections; allow both if prompted.")
	// Keep iSH's location device open for background execution; coordinates are not read.
	file, err := os.Open(locationPath)
	if err != nil {
		log.Print("Could not enable iSH background mode; the server is still running.")
		return func() {}
	}
	return func() { file.Close() }
}
