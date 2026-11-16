package debuglog

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/hyperion-cs/appleseum/applekit"
)

type debugConnKey struct{}

type debugListener struct {
	net.Listener
	server *Service
}

func (l debugListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &debugConn{Conn: conn, server: l.server}, nil
}

type debugConn struct {
	net.Conn
	mu       sync.Mutex
	interim  bytes.Buffer
	capture  *applekit.ExchangeCapture
	response bool
	finish   func()
	server   *Service
	tlsError sync.Once
}

func (c *debugConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil && c.server != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
		// The wire wrapper hides *tls.Conn from net/http's handshake logger.
		if conn, ok := c.Conn.(*tls.Conn); ok && !conn.ConnectionState().HandshakeComplete {
			c.tlsError.Do(func() { c.server.SystemError("http_server", errors.New("tls_handshake_failed")) })
		}
	}
	return n, err
}

func (c *debugConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.mu.Lock()
	if c.capture != nil {
		if c.response {
			c.capture.Write(p[:n])
		} else {
			c.interim.Write(p[:n])
		}
	}
	c.mu.Unlock()
	return n, err
}

func (c *debugConn) begin(capture *applekit.ExchangeCapture) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.capture = capture
	c.response = false
	c.interim.Reset()
}

func (c *debugConn) responseStart() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.capture.Write([]byte("\r\n\r\n----- RESPONSE -----\r\n"))
	c.capture.Write(c.interim.Bytes())
	c.interim.Reset()
	c.response = true
}

func (c *debugConn) done() {
	c.mu.Lock()
	finish := c.finish
	c.finish = nil
	capture := c.capture
	c.capture = nil
	c.mu.Unlock()
	if capture != nil {
		capture.Close()
	}
	if finish != nil {
		finish()
	}
}

// DebugListener observes plaintext HTTP writes after net/http adds its final
// headers and framing. Wrap TLS listeners after tls.NewListener, not beneath TLS.
func (s *Service) Listener(server *http.Server, listener net.Listener) net.Listener {
	if s == nil || !s.cfg.Enabled {
		return listener
	}
	previousContext, previousState := server.ConnContext, server.ConnState

	server.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		if previousContext != nil {
			ctx = previousContext(ctx, c)
		}
		if conn, ok := c.(*debugConn); ok {
			ctx = context.WithValue(ctx, debugConnKey{}, conn)
		}
		return ctx
	}

	server.ConnState = func(c net.Conn, state http.ConnState) {
		if conn, ok := c.(*debugConn); ok && (state == http.StateIdle || state == http.StateClosed) {
			conn.done()
		}
		if previousState != nil {
			previousState(c, state)
		}
	}

	return debugListener{Listener: listener, server: s}
}

// tlsState exposes the connection's TLS state, which the wrapper hides from net/http.
func (c *debugConn) tlsState(r *http.Request) {
	if conn, ok := c.Conn.(*tls.Conn); ok {
		state := conn.ConnectionState()
		r.TLS = &state
	}
}
