package integration_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/account"
	server "appleseum/backend/internal/app"
	"appleseum/backend/internal/wire"
)

type heldDownload struct {
	*fakeApple
	started      chan int64
	release      chan struct{}
	active, peak atomic.Int32
	failure      error
}

func (f *heldDownload) PrepareDownload(_ context.Context, id int64, version string) (applekit.Download, error) {
	return func(ctx context.Context, path string, progress func(applekit.DownloadProgress), _ applekit.DownloadOptions) (string, error) {
		active := f.active.Add(1)
		defer f.active.Add(-1)
		for peak := f.peak.Load(); active > peak; peak = f.peak.Load() {
			if f.peak.CompareAndSwap(peak, active) {
				break
			}
		}
		f.started <- id
		if f.failure != nil {
			return "", f.failure
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-f.release:
		}
		return f.fakeApple.Download(ctx, id, version, path, progress)
	}, nil
}

func TestLocalDownloadsRunThreeInParallelAndRefill(t *testing.T) {
	fake := &heldDownload{fakeApple: &fakeApple{owner: "parallel"}, started: make(chan int64, 20), release: make(chan struct{})}
	h := rememberedHarness(t, server.Config{DataDir: t.TempDir(), Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) { return fake, nil }}})
	defer stopRemembered(h)
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "parallel@example.test", Password: "fixture"}, 200, nil)
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1", "2"}}}, 202, nil)
	waitStarts(t, fake.started, 2)
	// Adding work while two old transfers are blocked must immediately use the third slot.
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"3", "4"}}}, 202, nil)
	waitStarts(t, fake.started, 1)
	select {
	case <-fake.started:
		t.Fatal("fourth transfer started before a slot was free")
	case <-time.After(30 * time.Millisecond):
	}
	var jobs wire.LibraryPage
	h.request(t, "GET", "/api/library", nil, 200, &jobs)
	for _, job := range jobs.Items {
		if job.Status == wire.Downloading {
			h.request(t, "POST", "/api/downloads/"+job.Id+"/cancel", nil, 202, nil)
			break
		}
	}
	waitStarts(t, fake.started, 1)
	close(fake.release)
	deadline := time.Now().Add(3 * time.Second)
	for {
		h.request(t, "GET", "/api/library", nil, 200, &jobs)
		active := false
		for _, job := range jobs.Items {
			if job.Status == wire.Failed {
				t.Fatal(job)
			}
			active = active || job.Status == wire.Downloading || job.Status == wire.Queued || job.Status == wire.Preparing || job.Status == wire.Canceling
		}
		if !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("transfers did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if fake.peak.Load() != 3 {
		t.Fatal("expected three parallel transfers", fake.peak.Load())
	}
}

func waitStarts(t *testing.T, started <-chan int64, n int) {
	t.Helper()
	for range n {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("parallel download did not start")
		}
	}
}

func TestPublicSessionsShareThreeDownloadsPerAccount(t *testing.T) {
	fake := &heldDownload{fakeApple: &fakeApple{owner: "same"}, started: make(chan int64, 20), release: make(chan struct{})}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	app, err := newTestServer(server.Config{DataDir: dir, Public: true, Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) { return fake, nil }}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(app.Handler())
	defer func() { ts.Close(); app.Close() }()
	one, two := newBrowser(t, ts), newBrowser(t, ts)
	one.login("same@example.test", "fixture")
	two.login("same@example.test", "fixture")
	one.refreshCatalog()
	one.request("POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1", "2"}}}, 202, nil)
	waitStarts(t, fake.started, 2)
	two.request("POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"3", "4"}}}, 202, nil)
	waitStarts(t, fake.started, 1)
	select {
	case <-fake.started:
		t.Fatal("browser sessions bypassed account limit")
	case <-time.After(30 * time.Millisecond):
	}
	close(fake.release)
	deadline := time.Now().Add(3 * time.Second)
	for one.state().DownloadsActive {
		if time.Now().After(deadline) {
			t.Fatal("public transfers did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if fake.peak.Load() != 3 {
		t.Fatal(fake.peak.Load())
	}
}

func TestDownloadNoSpaceIsReportedPrecisely(t *testing.T) {
	fake := &heldDownload{fakeApple: &fakeApple{owner: "no-space"}, started: make(chan int64, 20), failure: syscall.ENOSPC}
	h := rememberedHarness(t, server.Config{DataDir: t.TempDir(), Apple: &testApple{signIn: func(context.Context, string, string, string) (account.Session, error) { return fake, nil }}})
	defer stopRemembered(h)
	h.request(t, "POST", "/api/login", wire.LoginRequest{Email: "no-space@example.test", Password: "fixture"}, 200, nil)
	h.refresh(t)
	h.request(t, "POST", "/api/downloads", wire.DownloadRequest{Selection: wire.Selection{Ids: []string{"1"}}}, 202, nil)
	deadline := time.Now().Add(3 * time.Second)
	for {
		var jobs wire.LibraryPage
		h.request(t, "GET", "/api/library", nil, 200, &jobs)
		if len(jobs.Items) == 1 && jobs.Items[0].Status == wire.Failed {
			if jobs.Items[0].Error != "storage_full" {
				t.Fatal(jobs.Items[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disk failure not reported")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
