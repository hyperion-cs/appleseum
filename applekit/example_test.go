package applekit_test

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/hyperion-cs/appleseum/applekit"
)

func Example() {
	const appleId, password = "me@example.com", "secret"

	ctx := context.Background()

	c, err := applekit.SignIn(ctx, appleId, password)
	if errors.Is(err, applekit.ErrTwoFactorRequired) {
		// Prompt the user for the code sent to their trusted devices, then:
		c, err = applekit.SignInWithCode(ctx, appleId, password, "123456")
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("signed in as", c.Account().Email)

	apps, err := c.DelistedApps(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, a := range apps {
		fmt.Printf("%d  %s\n", a.Id, a.Name)
	}

	if len(apps) > 0 {
		path, err := c.Download(ctx, apps[0].Id, "", "out/app.ipa", nil)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("saved", path)
	}
}
