//go:build darwin

package pty

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// open implements Open() on macOS/BSD using the /dev/ptmx cloning device and
// the TIOCPTYGRANT/TIOCPTYUNLK/TIOCPTYGNAME ioctls (the same sequence
// libc's grantpt/unlockpt/ptsname perform under the hood).
func open() (*PTY, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("pty: open /dev/ptmx: %w", err)
	}

	if err := unix.IoctlSetInt(int(master.Fd()), unix.TIOCPTYGRANT, 0); err != nil {
		master.Close()
		return nil, fmt.Errorf("pty: grant: %w", err)
	}
	if err := unix.IoctlSetInt(int(master.Fd()), unix.TIOCPTYUNLK, 0); err != nil {
		master.Close()
		return nil, fmt.Errorf("pty: unlock: %w", err)
	}

	var buf [128]byte
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, master.Fd(), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&buf[0])))
	if errno != 0 {
		master.Close()
		return nil, fmt.Errorf("pty: get slave name: %w", errno)
	}
	end := strings.IndexByte(string(buf[:]), 0)
	if end < 0 {
		end = len(buf)
	}
	name := string(buf[:end])

	slave, err := os.OpenFile(name, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("pty: open %s: %w", name, err)
	}

	return &PTY{Master: master, Slave: slave}, nil
}
