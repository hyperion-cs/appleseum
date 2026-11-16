package applekit

import "github.com/hyperion-cs/appleseum/applekit/internal/env"

// RunningInIsh reports the cached result of iSH host detection.
func RunningInIsh() bool { return env.IsIsh() }
