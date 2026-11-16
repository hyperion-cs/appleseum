package integration_test

import (
	"net/http"
	"testing"

	"appleseum/backend/internal/wire"
)

func TestLoginRateCannotResetByChangingBrowser(t *testing.T) {
	const attempts = 11

	_, ts, _ := publicApp(t)
	for attempt := 1; attempt <= attempts; attempt++ {
		browser := newBrowser(t, ts)
		status := http.StatusOK
		if attempt == attempts {
			status = http.StatusTooManyRequests
		}
		login := "first@example.test"
		if attempt%2 == 0 {
			login = " FIRST@example.test "
		}
		browser.request(http.MethodPost, "/api/login", wire.LoginRequest{Email: login, Password: "fixture"}, status, nil)
	}
}
