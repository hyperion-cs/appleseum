package ams

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"howett.net/plist"
)

func BenchmarkAuthenticateNative(b *testing.B) {
	for _, retry := range []bool{false, true} {
		name := "Success"
		if retry {
			name = "AMDRetries"
		}
		b.Run(name, func(b *testing.B) {
			credentials, device, options := validNativeInput()
			body, err := plist.Marshal(map[string]any{"status": uint64(0), "passwordToken": "fixture", "dsPersonId": credentials.DirectoryServicesId}, plist.XMLFormat)
			if err != nil {
				b.Fatal(err)
			}
			jar, _ := cookiejar.New(nil)
			requests := 0
			client := &http.Client{Jar: jar, Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				requests++
				headers := make(http.Header)
				if retry {
					headers.Set("X-Apple-AMD-Action", "RP")
				}
				return &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(bytes.NewReader(body))}, nil
			})}
			if retry {
				device.Amd, device.AmdMachineId = "", ""
				options.Amd = &AmdController{Provider: &fakeAmdProvider{}, Bag: Bag{}}
			}
			signer := &nativeSigner{}
			expectedRequests := 1
			if retry {
				expectedRequests = 6
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				requests = 0
				_, err := AuthenticateNative(context.Background(), client, "https://auth.itunes.apple.com/auth/v1/native", signer, credentials, device, options)
				if requests != expectedRequests {
					b.Fatalf("requests=%d, want %d", requests, expectedRequests)
				}
				if !retry && err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
