package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const (
	shutdownTimeout = 15 * time.Second
)

// Handle interrupts throughout cleanup; iSH cannot rely on Go's default signal exit.
// The watchdog bounds cleanup while the runtime and emulator remain responsive.
func shutdownSignals(timeout time.Duration, exit func(int)) (context.Context, func(), func()) {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(context.Background())
	requested, started, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var requestOnce, finishOnce sync.Once
	go func() {
		var timer *time.Timer
		var deadline <-chan time.Time
		defer func() {
			if timer != nil {
				timer.Stop()
			}
		}()

		start := func() {
			if timer == nil {
				timer = time.NewTimer(timeout)
				deadline = timer.C
				close(started)
				cancel()
			}
		}

		request := requested
		for {
			select {
			case sig := <-signals:
				if timer != nil {
					code := 130
					if sig == syscall.SIGTERM {
						code = 143
					}
					exit(code) // Do not wait for logging or any cleanup locks.
					return
				}
				start()
			case <-request:
				request = nil
				start()
			case <-deadline:
				fmt.Fprintln(os.Stdout, "Shutdown timed out; exiting.")
				exit(1)
				return
			case <-finished:
				return
			}
		}
	}()

	begin := func() {
		requestOnce.Do(func() { close(requested) })
		<-started // Arm the watchdog before entering potentially blocking cleanup.
	}

	finish := func() {
		finishOnce.Do(func() {
			signal.Stop(signals)
			close(finished)
			cancel()
		})
	}

	return ctx, begin, finish
}
