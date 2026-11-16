package kbsync

import (
	"encoding/json"
	"os"
	"testing"
)

func TestScalarEncodingMatchesNativeWriter(t *testing.T) {
	raw, err := os.ReadFile("testdata/scalars.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Value, Noise, Word uint32 }
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, v := range cases {
		if got := encodeScalar(v.Value, v.Noise); got != v.Word {
			t.Errorf("value %d, noise %08x: got %08x, want %08x", v.Value, v.Noise, got, v.Word)
		}
	}
}
