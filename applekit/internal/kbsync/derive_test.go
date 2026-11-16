package kbsync

import (
	"encoding/json"
	"os"
	"testing"
)

// These packets are compatibility inputs, not expected output from a new Derive call.
func TestHistoricalPacketCompatibility(t *testing.T) {
	raw, err := os.ReadFile("testdata/historical_packets.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Hardware []byte
		Dsid     uint64
		Blob     []byte
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1_036 {
		t.Fatal("incomplete terminal vectors")
	}
	for i, v := range cases {
		if _, err := openTestPacket(v.Hardware, v.Dsid, v.Blob); err != nil {
			t.Fatalf("historical packet %d: %v", i, err)
		}
		got, err := Derive(v.Hardware, v.Dsid)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := openTestPacket(v.Hardware, v.Dsid, got); err != nil {
			t.Fatalf("generated packet %d: %v", i, err)
		}
	}
}

func BenchmarkKeybag(b *testing.B) {
	hw := []byte{1, 2, 3, 4, 5, 6}
	_, _ = Derive(hw, 123_456_789)
	b.ReportAllocs()
	for b.Loop() {
		_, _ = Derive(hw, 123_456_789)
	}
}
