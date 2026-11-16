//go:build !linux || !386

package main

func prepareIshRuntime(bool) error { return nil }
