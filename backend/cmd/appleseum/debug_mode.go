package main

import "fmt"

const (
	debugWeb debugMode = "web"
	debugRaw debugMode = "raw"
)

type debugMode string

func (d *debugMode) String() string { return string(*d) }

func (d *debugMode) IsBoolFlag() bool { return true }

func (d *debugMode) Set(value string) error {
	switch value {
	case "true", "web":
		*d = debugWeb
	case "raw":
		*d = debugRaw
	case "false":
		*d = ""
	default:
		return fmt.Errorf("debug mode must be web or raw")
	}
	return nil
}
