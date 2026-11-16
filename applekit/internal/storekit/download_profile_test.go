package storekit

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/applebackemu"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

func TestDownloadKeepsConfiguratorProfileWithAccountsParent(t *testing.T) {
	account := downloadAccount()
	dispatch := dispatchStep(t, account, fixtureDispatchUrl, 1, "synthetic-kbsync", "", downloadReply(t, map[string]any{}))
	asset := applebackemu.DownloadAsset(fixtureAssetUrl, applebackemu.Response{Body: []byte("synthetic archive bytes")})
	s, backend := downloadBackend(t, applebackemu.InMemory, account, fixtureDispatchUrl, dispatch, asset)
	profiles := observeDownloadProfiles(s)
	ctx := httpx.WithProfile(context.Background(), httpx.Accounts)
	if _, _, err := s.volumeStoreDownload(ctx, 1, "synthetic-kbsync", "", false); err != nil {
		t.Fatal(err)
	}
	if err := s.fetchToFile(ctx, fixtureAssetUrl, filepath.Join(t.TempDir(), "archive"), "", nil); err != nil {
		t.Fatal(err)
	}
	if len(backend.Requests()) != 2 {
		t.Fatal("download profile test did not cover both exchanges")
	}
	profiles.check(t, 2)
}
