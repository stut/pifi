package utils

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// Shell runs commands. The interface is the test surface for everything that
// touches the OS — every call to systemctl, nmcli, ip, ping, etc. goes
// through here. Tests inject a recording fake; production uses
// NewBashShell.
type Shell interface {
	// Run executes command via bash -c with the given timeout. Returns the
	// process exit code, captured stdout and stderr, and a non-nil err if the
	// process failed to start, exited non-zero, or timed out.
	Run(command string, timeout time.Duration) (exitCode int, stdout, stderr string, err error)
}

// NewBashShell returns a Shell that runs commands via `bash -c`.
func NewBashShell() Shell {
	return bashShell{}
}

type bashShell struct{}

func (bashShell) Run(command string, timeout time.Duration) (int, string, string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.Command("bash", "-c", command)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Own process group so a timeout kills the whole pipeline, not just bash.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return -1, "", "", fmt.Errorf("cmd.Start [%s]: %v", command, err)
	}

	timeoutChan := time.After(timeout)
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-timeoutChan:
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return -1, "", "", fmt.Errorf("command timed out: kill [%s]: %v", command, err)
		}
		return -1, "", "", fmt.Errorf("command timed out [%s]", command)

	case err := <-done:
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return exitErr.ExitCode(), stdout.String(), stderr.String(), fmt.Errorf("cmd.Wait [%s]: %v", command, exitErr)
			}
		}
		return 0, stdout.String(), stderr.String(), err
	}
}
