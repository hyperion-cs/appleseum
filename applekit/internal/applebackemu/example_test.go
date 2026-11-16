package applebackemu_test

import (
	"fmt"
	"io"
	"net/http"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

func ExampleNew() {
	fixture, err := applebackemu.New(applebackemu.InMemory, applebackemu.Step{
		Name: "synthetic bag", Method: http.MethodGet,
		Url:      "https://bag.itunes.apple.com/bag.xml",
		Response: applebackemu.Response{Body: []byte(`{"synthetic":true}`)},
	})
	if err != nil {
		panic(err)
	}
	defer fixture.Close()
	response, err := fixture.Client().Get("https://bag.itunes.apple.com/bag.xml")
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		panic(err)
	}
	if err := fixture.Verify(); err != nil {
		panic(err)
	}
	fmt.Println(string(body))
	// Output: {"synthetic":true}
}
