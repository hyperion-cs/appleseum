package account

import (
	"testing"
)

func TestNormalizeLogin(t *testing.T) {
	for input, want := range map[string]string{
		"70000000000":        "70000000000",
		"+70000000000":       "70000000000",
		"+7 (000) 000-00-00": "70000000000",
		"  +7\u00a0000\u202f000\u201100\u201100  ": "70000000000",
		"8 (000) 000-00-00":                        "70000000000",
		"7.000.000.00.00":                          "70000000000",
		" Mixed+tag@Example.invalid ":              "mixed+tag@example.invalid",
		" ROOT@APPLESEUM ":                         "root@appleseum",
		"+1 (202) 555-0123":                        "12025550123",
		"+852 0000 0000":                           "85200000000",
		"0000000000":                               "0000000000",
		"8000000000":                               "8000000000",
		"8 000 000-00-00 ext 123":                  "8 000 000-00-00 ext 123",
		"phone 123":                                "phone 123",
		"++70000000000":                            "++70000000000",
		"7+0000000000":                             "7+0000000000",
		"  ":                                       "",
	} {
		if got := NormalizeLogin(input); got != want {
			t.Errorf("NormalizeLogin(%q) = %q; want %q", input, got, want)
		}
	}
}
