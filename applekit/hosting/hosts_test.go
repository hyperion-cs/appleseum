package hosting

import "testing"

func TestNormalizeHostRejectsInvalidNames(t *testing.T) {
	for _, value := range []string{
		"https://apps.example.test", "apps.example.test:443", "user@apps.example.test",
		"*.example.test", "apps.example.test/path", "apps.example.test?query=1",
		"apps.example.test,,other.example.test", ",", " ", "bad..example.test",
		"bad_name.example.test", "-bad.example.test", "bad-.example.test",
		"192.168.0.0/24", "fe80::1%eth0", "[::1]:443",
	} {
		if _, err := NormalizeHost(value); err == nil {
			t.Errorf("accepted invalid host %q", value)
		}
	}
}
