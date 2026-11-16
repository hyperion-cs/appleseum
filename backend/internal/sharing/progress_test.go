package sharing

import (
	"encoding/json"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"appleseum/backend/internal/wire"
)

func TestProgressWeightsEveryPackageBySize(t *testing.T) {
	p := new(Progress)
	p.Manifest("share", "trace01", []Package{{Id: "small", Size: 100}, {Id: "large", Size: 900}})(true)
	// The untouched large package must already contribute to the denominator.
	// Retrying the tail of the small one cannot increase its coverage.
	for range 2 {
		transfer := p.File("share", "trace01", "small", 100)
		transfer.Sent(80, 100)
		transfer.Finish(true)
	}
	if got := p.Snapshot("share", "trace01"); got.Percent != 2 || got.Stage != wire.InstallTransferring {
		t.Fatalf("tail and retry: %+v", got)
	}
	transfer := p.File("share", "trace01", "small", 100)
	transfer.Sent(0, 90)
	transfer.Finish(true)
	if got := p.Snapshot("share", "trace01"); got.Percent != 10 || got.Stage != wire.InstallTransferring {
		t.Fatalf("small file: %+v", got)
	}
	transfer = p.File("share", "trace01", "large", 900)
	transfer.Sent(0, 450)
	if got := p.Snapshot("share", "trace01"); got.Percent != 55 || got.Stage != wire.InstallTransferring {
		t.Fatalf("weighted progress: %+v", got)
	}
	transfer.Sent(450, 900)
	if got := p.Snapshot("share", "trace01"); got.Percent != 99 || got.Stage != wire.InstallTransferring {
		t.Fatalf("response still active: %+v", got)
	}
	transfer.Finish(true)
	if got := p.Snapshot("share", "trace01"); got.Stage != wire.InstallComplete || got.Percent != 100 {
		t.Fatalf("completion: %+v", got)
	}
	if p.Snapshot("share", "trace02").Stage != wire.InstallWaiting || p.Snapshot("other", "trace01").Stage != wire.InstallWaiting {
		t.Fatal("attempts are not isolated")
	}
	transfer = p.File("share", "trace01", "small", 100)
	if p.Snapshot("share", "trace01").Stage == wire.InstallComplete {
		t.Fatal("retry still appears complete")
	}
	transfer.Finish(true)
}

func TestProgressStages(t *testing.T) {
	p := new(Progress)
	if got := p.Snapshot("share", "trace01"); got.Stage != wire.InstallWaiting {
		t.Fatalf("unknown attempt: %+v", got)
	}
	finish := p.Manifest("share", "trace01", []Package{{Id: "one", Size: 100}})
	if got := p.Snapshot("share", "trace01"); got.Stage != wire.InstallManifest {
		t.Fatalf("manifest in flight: %+v", got)
	}
	finish(true)
	if got := p.Snapshot("share", "trace01"); got.Stage != wire.InstallWaitingIpa || got.Percent != 0 {
		t.Fatalf("manifest sent: %+v", got)
	}
}

func TestProgressSnapshotDoesNotGrowWithPackageCount(t *testing.T) {
	snapshot := func(count int) []byte {
		p := new(Progress)
		packages := make([]Package, count)
		for i := range packages {
			packages[i] = Package{Id: string(rune('a' + i)), Size: 100}
		}
		p.Manifest("share", "trace01", packages)(true)
		raw, err := json.Marshal(p.Snapshot("share", "trace01"))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if string(snapshot(1)) != string(snapshot(100)) {
		t.Fatal("snapshot grew with the package list")
	}
}

func TestProgressExpiryAndRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := new(Progress)
		p.Manifest("share", "trace01", []Package{{Id: "one", Size: 100}})(true)
		transfer := p.File("share", "trace01", "one", 100)
		transfer.Sent(0, 43)
		transfer.Finish(false)
		time.Sleep(interruptionDelay)
		synctest.Wait()
		if got := p.Snapshot("share", "trace01"); got.Stage != wire.InstallInterrupted || got.Percent != 43 {
			t.Fatalf("interruption: %+v", got)
		}
		transfer = p.File("share", "trace01", "one", 100)
		if p.Snapshot("share", "trace01").Stage != wire.InstallTransferring {
			t.Fatal("retry still interrupted")
		}
		time.Sleep(2 * progressLifetime)
		synctest.Wait()
		if p.Snapshot("share", "trace01").Stage != wire.InstallTransferring {
			t.Fatal("active transfer expired")
		}
		transfer.Sent(43, 100)
		transfer.Finish(true)
		time.Sleep(progressLifetime - time.Second)
		synctest.Wait()
		if p.Snapshot("share", "trace01").Stage != wire.InstallComplete {
			t.Fatal("completion disappeared early")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if got := p.Snapshot("share", "trace01"); got.Stage != wire.InstallWaiting {
			t.Fatalf("idle record retained: %+v", got)
		}
		p.Manifest("share", "trace02", nil)(false)
		if got := p.Snapshot("share", "trace02"); got.Stage != wire.InstallFailed {
			t.Fatalf("failed manifest: %+v", got)
		}
		time.Sleep(progressLifetime)
		synctest.Wait()
		if p.Snapshot("share", "trace02").Stage != wire.InstallWaiting {
			t.Fatal("failed manifest retained")
		}
	})
}

func TestConcurrentProgress(t *testing.T) {
	p := new(Progress)
	p.Manifest("share", "trace01", []Package{{Id: "one", Size: 100}})(true)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			transfer := p.File("share", "trace01", "one", 100)
			transfer.Sent(int64(i*5), int64(i*5+5))
			p.Snapshot("share", "trace01")
			transfer.Finish(true)
		})
	}
	wg.Wait()
	if got := p.Snapshot("share", "trace01"); got.Stage != wire.InstallComplete || got.Percent != 100 {
		t.Fatalf("concurrent coverage: %+v", got)
	}
}

func TestCanceledParallelRequestDoesNotUndoCompletion(t *testing.T) {
	p := new(Progress)
	p.Manifest("share", "trace01", []Package{{Id: "one", Size: 100}})(true)
	canceled := p.File("share", "trace01", "one", 100)
	full := p.File("share", "trace01", "one", 100)
	full.Sent(0, 100)
	full.Finish(true)
	canceled.Finish(false)
	if p.Snapshot("share", "trace01").Stage != wire.InstallComplete {
		t.Fatal("a canceled parallel request undid full delivery")
	}
}

// iOS skips apps it already has, so their IPAs are never requested.
func TestProgressSettlesPackagesTheDeviceSkips(t *testing.T) {
	complete := func(skipped int) wire.InstallationProgress {
		return wire.InstallationProgress{Stage: wire.InstallComplete, Percent: 100, Skipped: skipped}
	}
	t.Run("one installed app", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p := new(Progress)
			p.Manifest("share", "trace01", []Package{{Id: "new", Size: 900}, {Id: "installed", Size: 100}})(true)
			transfer := p.File("share", "trace01", "new", 900)
			transfer.Sent(0, 900)
			transfer.Finish(true)
			time.Sleep(settleDelay - time.Second)
			synctest.Wait()
			if got := p.Snapshot("share", "trace01"); got.Stage != wire.InstallTransferring || got.Percent != 90 {
				t.Fatalf("settled too early: %+v", got)
			}
			time.Sleep(time.Second)
			synctest.Wait()
			if got := p.Snapshot("share", "trace01"); got != complete(1) {
				t.Fatalf("skipped app kept the attempt open: %+v", got)
			}
		})
	})
	t.Run("every app installed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p := new(Progress)
			p.Manifest("share", "trace01", []Package{{Id: "one", Size: 100}, {Id: "two", Size: 100}})(true)
			if got := p.Snapshot("share", "trace01"); got.Stage != wire.InstallWaitingIpa {
				t.Fatalf("manifest sent: %+v", got)
			}
			time.Sleep(settleDelay)
			synctest.Wait()
			if got := p.Snapshot("share", "trace01"); got != complete(2) {
				t.Fatalf("nothing requested: %+v", got)
			}
		})
	})
	t.Run("late request restarts the quiet period", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p := new(Progress)
			p.Manifest("share", "trace01", []Package{{Id: "first", Size: 100}, {Id: "second", Size: 100}, {Id: "installed", Size: 100}})(true)
			time.Sleep(settleDelay - time.Second)
			transfer := p.File("share", "trace01", "first", 100)
			transfer.Sent(0, 100)
			transfer.Finish(true)
			time.Sleep(settleDelay - time.Second)
			synctest.Wait()
			if got := p.Snapshot("share", "trace01"); got.Stage != wire.InstallTransferring {
				t.Fatalf("settled while the device was still downloading: %+v", got)
			}
			transfer = p.File("share", "trace01", "second", 100)
			transfer.Sent(0, 100)
			transfer.Finish(true)
			time.Sleep(settleDelay)
			synctest.Wait()
			if got := p.Snapshot("share", "trace01"); got != complete(1) {
				t.Fatalf("after the last download: %+v", got)
			}
		})
	})
	t.Run("an unfinished file is not skipped", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p := new(Progress)
			p.Manifest("share", "trace01", []Package{{Id: "broken", Size: 100}, {Id: "installed", Size: 100}})(true)
			transfer := p.File("share", "trace01", "broken", 100)
			transfer.Sent(0, 40)
			transfer.Finish(false)
			time.Sleep(2 * settleDelay)
			synctest.Wait()
			if got := p.Snapshot("share", "trace01"); got.Stage != wire.InstallInterrupted || got.Percent != 20 || got.Skipped != 0 {
				t.Fatalf("interrupted delivery: %+v", got)
			}
		})
	})
}
