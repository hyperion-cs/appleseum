package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/wire"
)

func TestErrorsKeepTheirCodeWhenWrapped(t *testing.T) {
	var reported []string
	a := &Server{cfg: Config{Report: func(operation string, err error) { reported = append(reported, operation) }}}
	for _, c := range []struct {
		err    error
		code   fault.Code
		status int
	}{
		{fmt.Errorf("selection: %w", fault.SelectionTooLarge), fault.SelectionTooLarge, 400},
		{fmt.Errorf("lookup: %w", fault.NotFound), fault.NotFound, 404},
		{errMediaType, fault.InvalidRequest, 415},
		{fault.Busy, fault.Busy, 409},
		// A text that merely looks like a code is a local failure.
		{errors.New(string(fault.SelectionTooLarge)), fault.StorageFailed, 500},
	} {
		out := httptest.NewRecorder()
		a.fail(out, c.err)
		var result wire.Error
		if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil || out.Code != c.status || result.Code != c.code {
			t.Fatalf("%v: %d %s", c.err, out.Code, out.Body.String())
		}
	}
	if len(reported) != 1 {
		t.Fatal("only errors without a code are local failures to report", reported)
	}
}
