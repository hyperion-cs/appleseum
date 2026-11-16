package applebackemu

import (
	"fmt"
	"net/http"
	"reflect"

	"howett.net/plist"
)

// PlistRequest checks an empty GET or plist POST.
// Value types must match; XML key order is ignored.
func PlistRequest(endpoint string, input map[string]any, header http.Header, response Response) (Step, error) {
	step := Step{Name: "plist exchange", Method: "GET", Url: endpoint, Header: header.Clone(), Body: []byte{}, Response: response}
	if input != nil {
		expected, err := normalizedPlist(input)
		if err != nil {
			return Step{}, err
		}
		step.Method, step.Body = "POST", nil

		step.Check = func(request Request) error {
			var actual map[string]any
			if _, err := plist.Unmarshal(request.Body, &actual); err != nil || !reflect.DeepEqual(actual, expected) {
				return fmt.Errorf("plist request mismatch")
			}
			return nil
		}
	}
	return step, nil
}
