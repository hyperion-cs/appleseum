package debuglog

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"

	"github.com/hyperion-cs/appleseum/applekit/hosting"

	"appleseum/backend/internal/paging"
)

func TestUnifiedJournalPersistsInternalErrorsWithoutConsole(t *testing.T) {
	var console bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&console)
	defer log.SetOutput(previous)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Enabled: true, Directory: filepath.Join(dir, "logs")}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.log.directory != filepath.Join(dir, "logs") {
		t.Fatal("journal must default to the data directory", s.log.directory)
	}
	s.SystemError("ipa_save", &os.PathError{Op: "write", Path: "private-path", Err: syscall.ENOSPC})
	s.SystemError("canceled", context.Canceled)
	s.HttpErrorLog().Print("http: panic serving private-peer: private-panic-value")
	s.HttpErrorLog().Print("http: TLS handshake error from private-peer: bad certificate")
	s.Close()
	s, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	filter, err := NewFilter([]string{"System"}, "")
	if err != nil {
		t.Fatal(err)
	}
	events, total, _, _, err := Page(s.Events(), filter, paging.Page{Limit: 10})
	if err != nil || total != 3 {
		t.Fatal("internal errors not persisted", total, err)
	}
	for _, event := range events {
		if event.Source != "System" || event.HasResponse || event.Method != "" || strings.Contains(event.Detail, "private-") {
			t.Fatal("internal error misclassified or private values leaked", event)
		}
	}
	if console.Len() != 0 {
		t.Fatal("nonfatal errors printed to console", console.String())
	}
}

func TestUnifiedJournalFailureUsesConsoleWithoutRecursion(t *testing.T) {
	var console bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&console)
	defer log.SetOutput(previous)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Enabled: true, Directory: filepath.Join(dir, "logs")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.log.csvFile.Close()
	s.SystemError("first", errors.New("local failure"))
	s.SystemError("second", errors.New("local failure"))
	if strings.Count(console.String(), "Journal unavailable:") != 1 {
		t.Fatal("missing critical diagnostic or console flood", console.String())
	}
	if len(s.Events()) != 2 {
		t.Fatal("recursive logging or lost in-memory events")
	}
}

func TestUnifiedJournalDisabledStaysQuiet(t *testing.T) {
	s := &Service{}
	s.SystemError("local", errors.New("failure"))
	s.HttpErrorLog().Print("http: panic serving private-peer")
	if len(s.Events()) != 0 {
		t.Fatal("logging enabled without --debug")
	}
}

func TestUnifiedJournalReportsCaptureWriteFailure(t *testing.T) {
	var console bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&console)
	defer log.SetOutput(previous)
	s := &Service{}
	s.log.directory = t.TempDir()
	capture := s.createExchangeCapture()
	if capture == nil {
		t.Fatal("capture not created")
	}
	if _, err := capture.WriteCloser.(*captureFile).ReadFrom(iotest.ErrReader(io.ErrUnexpectedEOF)); !errors.Is(err, io.ErrUnexpectedEOF) || console.Len() != 0 {
		t.Fatal("request read failure mistaken for a broken journal", err, console.String())
	}
	capture.Close()
	if _, err := io.Copy(capture.WriteCloser.(*captureFile), io.LimitReader(strings.NewReader("body"), 4)); err == nil {
		t.Fatal("closed capture accepted a write")
	}
	if strings.Count(console.String(), "Journal unavailable:") != 1 {
		t.Fatal("capture write failure not reported")
	}
}

func TestUnifiedJournalRecordsWrappedTlsFailure(t *testing.T) {
	var console bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&console)
	defer log.SetOutput(previous)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Enabled: true, Directory: filepath.Join(dir, "logs")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	config, err := hosting.Tls([]string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	server.Config.ErrorLog = s.HttpErrorLog()
	server.Listener = s.Listener(server.Config, tls.NewListener(server.Listener, config))
	server.Start()
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("GET / HTTP/1.0\r\n\r\n"))
	io.Copy(io.Discard, conn)
	events := s.Events()
	if len(events) != 1 || events[0].Source != "System" || events[0].Detail != "tls_handshake_failed" {
		t.Fatal("TLS wrapper hid handshake failure from journal")
	}
	if console.Len() != 0 {
		t.Fatal("nonfatal TLS error printed to console")
	}
}
