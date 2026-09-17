//go:build linux

package pty

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// open implements Open() on Linux using the /dev/ptmx multiplexer, per
// pty(7): open the multiplexer, unlock the slave, read back its number and
// open /dev/pts/<n>.
func open() (*PTY, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("pty: open /dev/ptmx: %w", err)
	}

	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		master.Close()
		return nil, fmt.Errorf("pty: unlock: %w", err)
	}

	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("pty: get pty number: %w", err)
	}

	slavePath := fmt.Sprintf("/dev/pts/%d", n)
	slave, err := os.OpenFile(slavePath, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("pty: open %s: %w", slavePath, err)
	}

	return &PTY{Master: master, Slave: slave}, nil
}
