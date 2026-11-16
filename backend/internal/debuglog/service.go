package debuglog

import (
	"io/fs"
	"os"
	"slices"
	"time"

	"appleseum/backend/internal/wire"
)

type Config struct {
	Enabled   bool
	Directory string
	Web       fs.FS
}

type Service struct {
	cfg Config
	log journal
}

func New(cfg Config) (*Service, error) {
	s := &Service{cfg: cfg, log: journal{started: time.Now(), directory: cfg.Directory}}
	if !cfg.Enabled {
		return s, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	s.log.executable, s.log.arguments = executable, slices.Clone(os.Args[1:])
	if err := s.log.load(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Service) Enabled() bool { return s != nil && s.cfg.Enabled }

func (s *Service) Close() {
	if s == nil {
		return
	}
	s.log.mu.Lock()
	defer s.log.mu.Unlock()
	s.log.closeCsv()
}

// Event records an event without a captured exchange.
func (s *Service) Event(e wire.DebugEvent) { s.add(e, "") }

func (s *Service) add(e wire.DebugEvent, file string) {
	if !s.Enabled() {
		return
	}
	s.log.mu.Lock()
	defer s.log.mu.Unlock()
	if err := s.log.add(entry{e, file}); err != nil {
		s.logUnavailable(err)
	}
}
