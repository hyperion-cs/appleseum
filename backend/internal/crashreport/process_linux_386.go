package crashreport

import (
	"os"
	"os/exec"
	"syscall"
)

func launch(command *exec.Cmd, input *os.File, ish bool) (*monitor, error) {
	if !ish {
		return startCommand(command)
	}
	// os/exec probes pidfd_open, which triggers SIGSYS in older iSH kernels
	// instead of returning ENOSYS. The standard syscall launcher and wait4
	// avoid that probe; all other platforms retain the usual os/exec path.
	pid, err := syscall.ForkExec(command.Path, command.Args, &syscall.ProcAttr{
		Dir: command.Dir, Env: command.Environ(),
		Files: []uintptr{input.Fd(), os.Stdout.Fd(), os.Stderr.Fd()},
	})
	if err != nil {
		return nil, err
	}
	// EOF ends the child. Avoid PID-based kills racing with wait4 reaping and
	// PID reuse; Stop remains bounded if the emulator cannot run the child.
	m := &monitor{done: make(chan struct{})}
	go func() {
		var status syscall.WaitStatus
		for {
			if _, err := syscall.Wait4(pid, &status, 0, nil); err != syscall.EINTR {
				break
			}
		}
		close(m.done)
	}()
	return m, nil
}
