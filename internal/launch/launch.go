// Package launch starts a detached pstty server process for a session that
// isn't running yet.
package launch

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/Q0tzly/pstty/internal/session"
)

// serverArg is the hidden subcommand main.go dispatches to run a server in
// the foreground of a freshly forked, detached process.
const serverArg = "__serve"

// IsServerCommand reports whether args (os.Args[1:]) request running as a
// server, and returns the session name if so.
func IsServerCommand(args []string) (name string, ok bool) {
	if len(args) == 2 && args[0] == serverArg {
		return args[1], true
	}
	return "", false
}

// EnsureRunning makes sure a server for name is listening, starting one in
// the background if it isn't.
func EnsureRunning(name string) error {
	sockPath, err := session.SockPath(name)
	if err != nil {
		return err
	}

	if session.Alive(sockPath) {
		return nil
	}
	os.Remove(sockPath)

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("launch: find executable: %w", err)
	}

	cmd := exec.Command(exe, serverArg, name)
	cmd.Stdin = nil
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("launch: open %s: %w", os.DevNull, err)
	}
	defer devnull.Close()
	cmd.Stdin = devnull
	cmd.Stdout = devnull
	cmd.Stderr = devnull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch: start server: %w", err)
	}
	// The server outlives us; don't wait on it, but do reap it if it
	// happens to exit before detaching further (avoids a zombie).
	go cmd.Wait()

	const (
		timeout = 3 * time.Second
		step    = 20 * time.Millisecond
	)
	for waited := time.Duration(0); waited < timeout; waited += step {
		if session.Alive(sockPath) {
			return nil
		}
		time.Sleep(step)
	}
	return fmt.Errorf("launch: session %q did not come up within %s", name, timeout)
}
