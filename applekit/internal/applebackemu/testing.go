package applebackemu

import "testing"

// NewTest creates a scenario and registers verification and cleanup.
func NewTest(t testing.TB, mode Mode, steps ...Step) *Server {
	t.Helper()
	server, err := New(mode, steps...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		server.Close()
		if err := server.Verify(); err != nil {
			t.Error(err)
		}
	})
	return server
}
