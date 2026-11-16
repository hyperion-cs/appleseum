package netcompat

import (
	"io"
	"net"
	"testing"
	"time"
)

type acceptResult struct {
	conn net.Conn
	err  error
}

func startAccept(listener net.Listener) <-chan acceptResult {
	result := make(chan acceptResult, 1)
	go func() {
		conn, err := listener.Accept()
		result <- acceptResult{conn, err}
	}()
	return result
}

func receiveAccept(t *testing.T, result <-chan acceptResult) acceptResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("Accept did not return")
		return acceptResult{}
	}
}

func TestListen(t *testing.T) {
	listener, err := Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	result := startAccept(listener)
	client, err := net.DialTimeout("tcp", listener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	got := receiveAccept(t, result)
	if got.err != nil {
		t.Fatal(got.err)
	}
	defer got.conn.Close()
	got.conn.SetDeadline(time.Now().Add(5 * time.Second))
	client.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 5)
	if _, err := io.ReadFull(got.conn, data); err != nil || string(data) != "hello" {
		t.Fatalf("received %q, %v", data, err)
	}
	if got.conn.RemoteAddr().String() != client.LocalAddr().String() {
		t.Fatal("accepted connection lost its peer address")
	}
}

func TestListenRejectsInvalidNetwork(t *testing.T) {
	if _, err := Listen("invalid", "127.0.0.1:0"); err == nil {
		t.Fatal("invalid network accepted")
	}
}
