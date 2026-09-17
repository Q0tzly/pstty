// Package term provides raw-mode and window-size helpers for a controlling
// terminal, built directly on golang.org/x/sys/unix (no external PTY/term
// libraries).
package term

import (
	"golang.org/x/sys/unix"
)

// Winsize mirrors unix.Winsize with plain field names for use outside this
// package.
type Winsize struct {
	Rows uint16
	Cols uint16
}

// GetSize returns the current terminal size for fd.
func GetSize(fd int) (Winsize, error) {
	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil {
		return Winsize{}, err
	}
	return Winsize{Rows: ws.Row, Cols: ws.Col}, nil
}

// SetSize applies ws to fd (typically a PTY master).
func SetSize(fd int, ws Winsize) error {
	return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{
		Row: ws.Rows,
		Col: ws.Cols,
	})
}

// State is the saved terminal state returned by MakeRaw, to be restored with
// Restore.
type State struct {
	termios unix.Termios
}

// MakeRaw puts fd into raw mode (no echo, no line buffering, no signal
// generation from control characters) and returns the previous state so it
// can be restored later. This replicates the classic cfmakeraw(3) behavior.
func MakeRaw(fd int) (*State, error) {
	orig, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return nil, err
	}

	raw := *orig
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0

	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &raw); err != nil {
		return nil, err
	}
	return &State{termios: *orig}, nil
}

// Restore restores fd to the state captured by MakeRaw.
func Restore(fd int, s *State) error {
	return unix.IoctlSetTermios(fd, ioctlSetTermios, &s.termios)
}
