package storekit

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
)

func TestAssemblyFailurePreservesDestination(t *testing.T) {
	for _, mode := range []string{"canceled", "CRC"} {
		t.Run(mode, func(t *testing.T) {
			const original = "previous complete IPA"

			src, _ := rawSource(t, mode == "CRC")
			dir := t.TempDir()
			dst := filepath.Join(dir, "out.ipa")
			if err := os.WriteFile(dst, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			if err := assembleIpa(ctx, src, dst, nil); err == nil {
				t.Fatal("failed assembly accepted")
			}
			got, err := os.ReadFile(dst)
			if err != nil || string(got) != original {
				t.Fatal("failed assembly damaged existing IPA", err)
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 1 {
				t.Fatal("unfinished assembly retained temporary files", err)
			}
		})
	}
}

func TestMetadataDoesNotMutateResponse(t *testing.T) {
	accountInfo := map[string]any{"AppleID": "original value", "DSPersonID": uint64(123)}
	meta := map[string]any{"itemName": "Example", "appleId": "original value", "com.apple.iTunesStore.downloadInfo": accountInfo}
	item := map[string]any{"metadata": meta}
	want := map[string]any{"itemName": "Example", "appleId": "original value", "com.apple.iTunesStore.downloadInfo": map[string]any{"AppleID": "original value", "DSPersonID": uint64(123)}}
	for range 2 {
		if _, err := buildMetadata(item); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(meta, want) {
			t.Fatal("metadata mutated shared download response")
		}
	}
}

func writeTestZip(t testing.TB, names, contents []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.ipa")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for i, name := range names {
		dst, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(dst, contents[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAssemblySelectsTopLevelBundle(t *testing.T) {
	info := `<plist><dict><key>CFBundleExecutable</key><string>Main</string></dict></plist>`
	manifest := `<plist><dict><key>SinfPaths</key><array><string>SC_Info/Main.sinf</string></array></dict></plist>`
	src := writeTestZip(t, []string{"Payload/Main.app/Extras/Nested.app/Info.plist", "Payload/Main.app/Extras/Nested.app/SC_Info/Manifest.plist", "Payload/Main.app/SC_Info/Manifest.plist", "Payload/Main.app/Info.plist"}, []string{info, strings.ReplaceAll(manifest, "Main.sinf", "Wrong.sinf"), manifest, info})
	dst := filepath.Join(t.TempDir(), "out.ipa")
	if err := assembleIpa(context.Background(), src, dst, map[string]any{"sinfs": []any{map[string]any{"sinf": []byte("license")}}}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	count := 0
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".sinf") {
			count++
			if f.Name != "Payload/Main.app/SC_Info/Main.sinf" {
				t.Fatal("license injected into another bundle", f.Name)
			}
		}
	}
	if count != 1 {
		t.Fatal("missing main bundle license")
	}
}

func TestCatalogResponsesDecodeCompressionAndRejectUnknownDocuments(t *testing.T) {
	const body = `{"purchases":[],"results":[]}`
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	if _, err := io.WriteString(w, body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	response := catalogReply("")
	response.Header.Set("Content-Encoding", "gzip")
	response.Body = compressed.Bytes()
	finance, err := applebackemu.FinanceRequest(catalogContext(), "https://ph2.itunes.apple.com/purchaseHistory", map[string]any{"dateRangeType": "", "customYear": 0, "contentTypeGroups": nil}, response)
	if err != nil {
		t.Fatal(err)
	}
	lookup := lookupStep(t, "kz", []int64{1}, "")
	lookup.Response = response
	session, _ := catalogBackend(t, applebackemu.InMemory, finance, lookup)
	client := session.Client
	var got purchasePage
	err = session.financeJson(context.Background(), "https://ph2.itunes.apple.com/purchaseHistory", purchaseCriteria{}, &got, "purchases")
	if err != nil || got.Purchases == nil {
		t.Error("ph2 response did not decode gzip", err)
	}
	if _, err = lookupStoreApps(context.Background(), client, "kz", []int64{1}, nil); err != nil {
		t.Error("lookup response did not decode gzip", err)
	}
	for _, invalid := range []string{`{}`, `null`, `{"error":"unavailable"}`, `{"data":{}}`, `{"data":{"attributes":null}}`} {
		var purchases []Purchase
		var page purchasePage
		_ = json.Unmarshal([]byte(invalid), &page)
		if err := page.appendPurchases(map[int64]bool{}, &purchases); err == nil {
			t.Error("unknown purchase document accepted", invalid)
		}
		invalidSession, _ := catalogBackend(t, applebackemu.InMemory, lookupStep(t, "kz", []int64{1}, invalid))
		client = invalidSession.Client
		if _, err := lookupStoreApps(context.Background(), client, "kz", []int64{1}, nil); err == nil {
			t.Error("unknown lookup document marked apps delisted", invalid)
		}
	}
}

func TestPurchasePaginationLimitDoesNotReturnPartialHistory(t *testing.T) {
	steps := []applebackemu.Step{settingsStep(t, 2_026)}
	cursor := ""
	for page := 1; page <= 1_000; page++ {
		next := fmt.Sprintf("next-%d", page)
		steps = append(steps, historyStep(t, 2_026, cursor, fmt.Sprintf(`{"purchases":[],"paginationToken":%q}`, next)))
		cursor = next
	}
	session, backend := catalogBackend(t, applebackemu.InMemory, steps...)
	if purchases, err := session.ListPurchases(context.Background()); err == nil || purchases != nil {
		t.Fatal("unfinished purchase history returned as complete")
	}
	if len(backend.Requests()) != 1_001 {
		t.Fatal("purchase request limit changed", len(backend.Requests()))
	}
}

func TestCookieDefaultPathSurvivesRestore(t *testing.T) {
	for _, path := range []string{"/a//b/../login", "/a/./login", "///login"} {
		t.Run(path, func(t *testing.T) {
			origin, _ := url.Parse("https://buy.example.test" + path)
			jar := newJar()
			jar.SetCookies(origin, []*http.Cookie{{Name: "token", Value: "saved"}})
			session := &Session{Account: Account{DirectoryServicesId: "123", PasswordToken: "token", Guid: "001122334455"}, Client: &http.Client{Jar: jar}, amd: ams.AmdController{Bag: ams.Bag{DownloadURL: "https://buy.example.test/download"}}}
			data, err := session.Export()
			if err != nil {
				t.Fatal(err)
			}
			restored, err := Restore(data)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{origin.Path, "/a/other", "/outside", "//asset"} {
				probe := *origin
				probe.Path = path
				if !reflect.DeepEqual(jar.Cookies(&probe), restored.Client.Jar.Cookies(&probe)) {
					t.Error("cookie path scope changed after restore", path)
				}
			}
		})
	}
}

func BenchmarkAssemblyManyEntries(b *testing.B) {
	names := []string{"Payload/Main.app/Info.plist"}
	data := []string{`<plist><dict><key>CFBundleExecutable</key><string>Main</string></dict></plist>`}
	for i := range 64 {
		names = append(names, fmt.Sprintf("Payload/Main.app/asset-%d", i))
		data = append(data, strings.Repeat("synthetic archive payload", 170))
	}
	src := writeTestZip(b, names, data)
	dst := filepath.Join(b.TempDir(), "output.ipa")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := assembleIpa(context.Background(), src, dst, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRejectedCookieCannotReplaceOrDeletePersistedCookie(t *testing.T) {
	for _, domain := range []string{"example.test", "com"} {
		for _, maxAge := range []int{0, -1} {
			t.Run(fmt.Sprintf("%s/%d", domain, maxAge), func(t *testing.T) {
				origin, _ := url.Parse("https://" + domain + "/login")
				bad, _ := url.Parse("https://unrelated.example.com/login")
				jar := newJar()
				jar.SetCookies(origin, []*http.Cookie{{Name: "token", Value: "original", Domain: domain, Path: "/"}})
				jar.SetCookies(bad, []*http.Cookie{{Name: "token", Value: "rejected", Domain: domain, Path: "/", MaxAge: maxAge}})
				session := &Session{Account: Account{DirectoryServicesId: "123", PasswordToken: "token", Guid: "001122334455"}, Client: &http.Client{Jar: jar}, amd: ams.AmdController{Bag: ams.Bag{DownloadURL: "https://buy.example.test/download"}}}
				data, err := session.Export()
				if err != nil {
					t.Fatal(err)
				}
				restored, err := Restore(data)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(jar.Cookies(origin), restored.Client.Jar.Cookies(origin)) {
					t.Fatal("rejected cookie changed saved authentication")
				}
			})
		}
	}
}

func TestDownloadKeepsExistingEndpointQuery(t *testing.T) {
	const endpoint = "https://buy.itunes.apple.com/download?route=fixture"

	account := downloadAccount()
	step := dispatchStep(t, account, endpoint, 1, "fixture", "", downloadReply(t, map[string]any{}))
	session, _ := downloadBackend(t, applebackemu.InMemory, account, endpoint, step)
	if _, _, err := session.volumeStoreDownload(context.Background(), 1, "fixture", "", false); err != nil {
		t.Fatal(err)
	}
}

func TestAssemblyReplacesExistingMetadataAndLicense(t *testing.T) {
	const info = `<plist><dict><key>CFBundleExecutable</key><string>Main</string></dict></plist>`
	src := writeTestZip(t, []string{"Payload/Main.app/Info.plist", "iTunesMetadata.plist", "Payload/Main.app/SC_Info/Main.sinf"}, []string{info, "obsolete metadata", "obsolete license"})
	dst := filepath.Join(t.TempDir(), "out.ipa")
	item := map[string]any{"sinfs": []any{map[string]any{"sinf": []byte("current license")}}}
	if err := assembleIpa(context.Background(), src, dst, item); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	metadata, license := 0, 0
	for _, f := range zr.File {
		switch f.Name {
		case "iTunesMetadata.plist":
			metadata++
		case "Payload/Main.app/SC_Info/Main.sinf":
			license++
			body, err := readZip(f)
			if err != nil || string(body) != "current license" {
				t.Fatal("obsolete license retained", err)
			}
		}
	}
	if metadata != 1 || license != 1 {
		t.Fatal("assembly left duplicate metadata/license entries", metadata, license)
	}
}

func TestCookieHostSpellingDoesNotReorderUpdates(t *testing.T) {
	origin, _ := url.Parse("https://BUY.example.test./login")
	canonical, _ := url.Parse("https://buy.example.test/login")
	jar := newJar()
	jar.SetCookies(origin, []*http.Cookie{{Name: "token", Value: "first", Path: "/"}})
	jar.SetCookies(canonical, []*http.Cookie{{Name: "token", Value: "second", Path: "/"}})
	jar.SetCookies(origin, []*http.Cookie{{Name: "token", Value: "latest", Path: "/"}})
	session := &Session{Account: Account{DirectoryServicesId: "123", PasswordToken: "token", Guid: "001122334455"}, Client: &http.Client{Jar: jar}, amd: ams.AmdController{Bag: ams.Bag{DownloadURL: "https://buy.example.test/download"}}}
	data, err := session.Export()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(jar.Cookies(canonical), restored.Client.Jar.Cookies(canonical)) {
		t.Fatal("restored an older cookie value after host spelling changed")
	}
}

func TestAssemblyRejectsMissingOrUnreadableBundleMetadata(t *testing.T) {
	for _, tc := range []struct{ name, path, info string }{
		{"missing main bundle", "Nested.app/Info.plist", `<plist><dict/></plist>`},
		{"broken info", "Payload/Main.app/Info.plist", `<plist><dict>`},
		{"missing license destination", "Payload/Main.app/Info.plist", `<plist><dict/></plist>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := writeTestZip(t, []string{tc.path}, []string{tc.info})
			dst := filepath.Join(t.TempDir(), "out.ipa")
			item := map[string]any{"sinfs": []any{map[string]any{"sinf": []byte("license")}}}
			if err := assembleIpa(context.Background(), src, dst, item); err == nil {
				t.Fatal("uninstallable archive accepted")
			}
			if _, err := os.Stat(dst); !os.IsNotExist(err) {
				t.Fatal("failed assembly published an output file", err)
			}
		})
	}
}

func TestPurchasePageOverlapDoesNotSkipLaterApps(t *testing.T) {
	session, backend := catalogBackend(t, applebackemu.InMemory, settingsStep(t, 2_026, 2_025),
		historyStep(t, 2_026, "", `{"purchases":[{"orderId":"overlapping","lineItems":[{"kind":"IOSApp","adamId":"1"}]}]}`),
		historyStep(t, 2_025, "", `{"purchases":[{"orderId":"overlapping","lineItems":[{"kind":"IOSApp","adamId":"1"}]}],"paginationToken":"next"}`),
		historyStep(t, 2_025, "next", `{"purchases":[{"orderId":"overlapping","lineItems":[{"kind":"IOSApp","adamId":"2"}]}]}`),
	)
	items, err := session.ListPurchases(context.Background())
	if err != nil || len(backend.Requests()) != 4 || len(items) != 2 || items[1].Id != 2 {
		t.Fatal("deduplication hid a later purchase page", len(items), err)
	}
}

func TestRepeatedPurchasePageFailsInsteadOfReturningPartialHistory(t *testing.T) {
	const repeated = `{"purchases":[{"orderId":"repeat","lineItems":[{"kind":"IOSApp","adamId":"1"}]}],"paginationToken":"repeat"}`
	session, backend := catalogBackend(t, applebackemu.InMemory, settingsStep(t, 2_026), historyStep(t, 2_026, "", repeated), historyStep(t, 2_026, "repeat", repeated))
	if purchases, err := session.ListPurchases(context.Background()); err == nil || purchases != nil || len(backend.Requests()) != 3 {
		t.Fatal("repeated cursor accepted or request loop continued", len(backend.Requests()), err)
	}
}
