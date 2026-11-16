package httpx

import (
	"context"
	"strings"
	"testing"
)

func TestPlatformMetadataIsLocalToItsRequestContext(t *testing.T) {
	p := Platform{Model: "MacBookAir9,1", Version: "15.7", Build: "24G222"}
	ctx := WithProfile(WithPlatform(context.Background(), p), Accounts)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if agent := UserAgentFor(ctx); !strings.Contains(agent, "OS X 15.7; 24G222") || !strings.HasPrefix(agent, "com.apple.accountsd/113") {
		t.Fatal(agent)
	}
	if UserAgentFor(context.Background()) != DefaultConfiguratorUserAgent || Accounts.UserAgent() != DefaultAccountsUserAgent {
		t.Fatal("global client identity changed")
	}
	if p.UserAgent(AuthKit) != DefaultAuthKitUserAgent {
		t.Fatal("AuthKit protocol agent changed")
	}
	for _, invalid := range []Platform{{Model: "MacBookAir9,1\r\nX: 1"}, {Version: "15.7; injected"}, {Build: "24G222>"}} {
		if invalid.Validate() == nil {
			t.Fatal("invalid header metadata accepted")
		}
	}
}
