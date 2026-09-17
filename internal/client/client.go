// Package client implements the pstty attach client: it bridges the local
// terminal's stdin/stdout with a session's Unix domain socket.
package client

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Q0tzly/pstty/internal/proto"
	"github.com/Q0tzly/pstty/internal/session"
	"github.com/Q0tzly/pstty/internal/term"
)

// DefaultDetachByte is the control character (Ctrl+]) that ends the
// local attachment without touching the remote session, unless
// overridden (see ParseDetachKey).
const DefaultDetachByte = 0x1D

// ParseDetachKey parses a caret-notation control character, like "^]"
// (DefaultDetachByte) or "^^", into its byte value. This lets an attach
// nested inside another one (e.g. over SSH to a second machine, where
// $PSTTY_SESSION can't be seen or checked) use a different key, since
// otherwise the outer pst client always intercepts Ctrl+] first and the
// inner one never sees it.
func ParseDetachKey(s string) (byte, error) {
	invalid := fmt.Errorf("client: invalid detach key %q: want caret notation like \"^]\"", s)
	if len(s) != 2 || s[0] != '^' {
		return 0, invalid
	}
	c := s[1]
	if c >= 'a' && c <= 'z' {
		c -= 'a' - 'A'
	}
	switch {
	case c >= '@' && c <= '_':
		return c - '@', nil
	case c == '?':
		return 0x7f, nil
	}
	return 0, invalid
}

// formatKey renders a control byte as "Ctrl+X" for messages.
func formatKey(b byte) string {
	switch {
	case b < 0x20:
		return fmt.Sprintf("Ctrl+%c", b+0x40)
	case b == 0x7f:
		return "Ctrl+?"
	default:
		return fmt.Sprintf("%#x", b)
	}
}

// dial connects to name's session socket.
func dial(name string) (net.Conn, error) {
	sockPath, err := session.SockPath(name)
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("client: connect: %w", err)
	}
	return conn, nil
}

// Attach connects to name's socket and bridges the local terminal to it
// until the session ends or the user detaches with the detach key
// (DefaultDetachByte unless detach overrides it).
func Attach(name string, detach byte) error {
	conn, err := dial(name)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.Write([]byte{byte(proto.HandshakeAttach)}); err != nil {
		return fmt.Errorf("client: handshake: %w", err)
	}

	stdinFd := int(os.Stdin.Fd())
	state, err := term.MakeRaw(stdinFd)
	if err != nil {
		return fmt.Errorf("client: set raw mode: %w", err)
	}
	defer term.Restore(stdinFd, state)

	fmt.Fprintf(os.Stderr, "pst: attached to %q (detach: %s)\r\n", name, formatKey(detach))
	setTitle(name)
	defer clearTitle()

	if ws, err := term.GetSize(stdinFd); err == nil {
		proto.WriteResize(conn, ws.Rows, ws.Cols)
	}

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			if ws, err := term.GetSize(stdinFd); err == nil {
				proto.WriteResize(conn, ws.Rows, ws.Cols)
			}
		}
	}()

	outDone := make(chan struct{})
	go func() {
		io.Copy(os.Stdout, conn)
		close(outDone)
	}()

	// forwardStdin blocks on a plain os.Stdin.Read, which can't be
	// interrupted from here. Run it in its own goroutine and race it
	// against outDone: if the remote side closes the connection (session
	// ended, or this attach got rejected) while the user hasn't typed
	// anything, we still want to exit immediately rather than wait for a
	// keystroke that stops the blocked read. Process exit cleans up the
	// leftover goroutine.
	detachedCh := make(chan bool, 1)
	go func() { detachedCh <- forwardStdin(conn, detach) }()

	select {
	case <-outDone:
		term.Restore(stdinFd, state)
		fmt.Fprintln(os.Stderr, "\r\n[session ended]")
	case detached := <-detachedCh:
		if detached {
			term.Restore(stdinFd, state)
			fmt.Fprintln(os.Stderr, "\r\n[detached]")
		} else {
			<-outDone
			term.Restore(stdinFd, state)
			fmt.Fprintln(os.Stderr, "\r\n[session ended]")
		}
	}
	return nil
}

// forwardStdin copies stdin to conn as data frames until either stdin closes
// (server-driven exit, returns false) or the user presses the detach key
// (returns true).
func forwardStdin(conn net.Conn, detach byte) (detached bool) {
	buf := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if i := indexByte(chunk, detach); i >= 0 {
				if i > 0 {
					proto.WriteData(conn, chunk[:i])
				}
				return true
			}
			if werr := proto.WriteData(conn, chunk); werr != nil {
				return false
			}
		}
		if err != nil {
			return false
		}
	}
}

// setTitle sets the terminal window/tab title to name so it stays visible
// as an at-a-glance indicator of which session is attached, since the
// shell inside the session typically renders the same prompt as the
// outer shell.
func setTitle(name string) {
	fmt.Fprintf(os.Stderr, "\033]0;pstty:%s\007", name)
}

// clearTitle drops the title override set by setTitle. The outer shell's
// own prompt hooks (if any) repaint the title on the next redraw either
// way; this just avoids leaving a stale "pst:name" title if it doesn't.
func clearTitle() {
	fmt.Fprint(os.Stderr, "\033]0;\007")
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

// Kill asks name's server to terminate the shell and shut down.
func Kill(name string) error {
	conn, err := dial(name)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte{byte(proto.HandshakeKill)})
	return err
}
