package kbsync

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/testfixture"
)

func TestHardwareFormsAndOutputOwnership(t *testing.T) {
	cases := testfixture.Keybags(t)
	if len(cases) != 12 {
		t.Fatal("incomplete hardware cases")
	}
	for _, v := range cases {
		packed := make([]byte, 24)
		binary.LittleEndian.PutUint32(packed, 6)
		copy(packed[4:], v.Hardware)
		for _, hw := range [][]byte{v.Hardware, packed} {
			before := bytes.Clone(hw)
			got, err := Derive(hw, v.Dsid)
			if err != nil || !bytes.Equal(hw, before) {
				t.Fatal("input mutated or hardware rejected", err)
			}
			if _, err := openTestPacket(v.Hardware, v.Dsid, got); err != nil {
				t.Fatal(err)
			}
			if _, err := openTestPacket(v.Hardware, v.Dsid+1, got); err == nil {
				t.Fatal("keybag lost account binding")
			}
			otherHw := bytes.Clone(v.Hardware)
			otherHw[0] ^= 1
			if _, err := openTestPacket(otherHw, v.Dsid, got); err == nil {
				t.Fatal("keybag lost hardware binding")
			}
			got[0] ^= 1
			again, err := Derive(hw, v.Dsid)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := openTestPacket(v.Hardware, v.Dsid, again); err != nil {
				t.Fatal("mutable result alias or invalid new packet", err)
			}
		}
	}
}

func TestInvalidHardware(t *testing.T) {
	for _, hw := range [][]byte{nil, make([]byte, 5), make([]byte, 7), make([]byte, 24), make([]byte, 25)} {
		if _, err := Derive(hw, 123); err == nil {
			t.Fatal("invalid hardware accepted")
		}
	}
}
