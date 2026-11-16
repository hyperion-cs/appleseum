package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	startupTimeout = 30 * time.Second
)

func startupFailureMessage(ish bool) string {
	message := "Could not start the server within 30 seconds."
	if ish {
		message += " Check that airplane mode is off and iSH can access the local network."
	}
	return message
}

// A context cannot interrupt every emulated syscall. Terminate on timeout while
// the golang scheduler is responsive, rather than waiting for startup cleanup.
func startupDeadline(timeout time.Duration, expire func()) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	var once sync.Once
	stop := context.AfterFunc(ctx, func() {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			once.Do(expire)
		}
	})
	return ctx, func() {
		once.Do(func() {})
		stop()
		cancel()
	}
}

func waitForStartup(ctx context.Context, configured string, bound net.Addr, serverErrors <-chan error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	host, _, err := net.SplitHostPort(configured)
	if err != nil {
		return err
	}
	_, port, err := net.SplitHostPort(bound.String())
	if err != nil {
		return err
	}
	if host == "" {
		host = "127.0.0.1"
	} else if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		if ip.To4() == nil {
			host = "::1"
		} else {
			host = "127.0.0.1"
		}
	}
	// Contact only our bound listener, without environment proxy settings.
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/api/health", nil)
	if err != nil {
		return err
	}
	result := make(chan error, 1)
	go func() {
		response, err := (&http.Client{Transport: transport}).Do(request)
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if err == nil && response.StatusCode != http.StatusOK {
				err = errors.New("local server did not become ready")
			}
		}
		result <- err
	}()
	select {
	case err := <-serverErrors:
		return err
	case err := <-result:
		if err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
