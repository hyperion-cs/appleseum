//go:build !linux || !386

package crashreport

import (
	"os"
	"os/exec"
)

func launch(command *exec.Cmd, _ *os.File, _ bool) (*monitor, error) {
	return startCommand(command)
}
