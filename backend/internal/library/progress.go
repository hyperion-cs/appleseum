package library

import (
	"context"
	"time"

	"github.com/hyperion-cs/appleseum/applekit"

	"appleseum/backend/internal/db"
	"appleseum/backend/internal/wire"
)

const (
	// Weight of the newest sample in the smoothed transfer speed.
	speedSmoothing = 0.35
)

// Progress returns a reporter for one transfer attempt; it keeps the speed
// estimate and logs only the first failed write.
func (s *Service) Progress(id string) func(applekit.DownloadProgress) {
	last := time.Now()
	var lastBytes, lastTotal int64
	var speed float64
	logged := false
	return func(p applekit.DownloadProgress) {
		status := wire.Downloading
		switch p.Stage {
		case "preparing":
			p.Received, p.Total = lastBytes, lastTotal
			status, speed = wire.Preparing, 0
		case "downloading":
			now := time.Now()
			if elapsed := now.Sub(last).Seconds(); elapsed > 0 && p.Received >= lastBytes {
				instant := float64(p.Received-lastBytes) / elapsed
				if speed == 0 {
					speed = instant
				} else {
					speed = speedSmoothing*instant + (1-speedSmoothing)*speed
				}
			}
			last, lastBytes, lastTotal = now, p.Received, p.Total
		}
		err := s.db.SaveProgress(context.Background(), id, db.Progress{Status: status, Received: p.Received, Total: p.Total, Speed: speed})
		s.StorageFailure(err)
		if err != nil && !logged {
			s.report("download_progress", err)
			logged = true
		}
	}
}
