package plistutil

import (
	"bytes"
	"strings"
	"testing"

	"howett.net/plist"
)

func TestApplePlistEnvelopes(t *testing.T) {
	value := map[string]any{"message": "binary text containing <plist is still data"}
	for _, format := range []int{plist.XMLFormat, plist.BinaryFormat} {
		body, err := plist.Marshal(value, format)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if _, err = Unmarshal(body, &decoded); err != nil || decoded["message"] != value["message"] {
			t.Fatal("plist envelope changed", format, err)
		}
	}
	for _, body := range []string{`</plist>bad<plist>`, `garbage<plist>`, ``, `</plist>   <plist><dict></dict>`} {
		var decoded map[string]any
		if _, err := Unmarshal([]byte(body), &decoded); err == nil {
			t.Fatal("incomplete envelope accepted", body)
		}
	}
}

func TestBinaryPlistWhitespaceTrailerIsData(t *testing.T) {
	checked := 0
	for size := range 512 {
		expected := strings.Repeat("x", size) + "<plist"
		body, err := plist.Marshal(map[string]string{"message": expected}, plist.BinaryFormat)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.ContainsRune([]byte(" \t\n\v\f\r"), rune(body[len(body)-1])) {
			continue
		}
		var got map[string]string
		if _, err := Unmarshal(body, &got); err != nil || got["message"] != expected {
			t.Fatal("binary trailer was trimmed", size, err)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("missing binary whitespace-trailer cases")
	}
}

func TestInt64AcceptsOnlyRepresentableIntegers(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  int64
		ok    bool
	}{
		{int64(-5), -5, true}, {uint64(1 << 63), 0, false}, {uint64(1<<63 - 1), 1<<63 - 1, true}, {"1", 0, false}, {nil, 0, false},
	} {
		if got, ok := Int64(tc.value); got != tc.want || ok != tc.ok {
			t.Fatal("plist integer conversion changed", tc.value, got, ok)
		}
	}
}
