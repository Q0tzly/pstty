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

// DetachByte is the control character (Ctrl+]) that ends the local
// attachment without touching the remote session.
const DetachByte = 0x1D

// Attach connects to name's socket and bridges the local terminal to it
// until the session ends or the user detaches with Ctrl+].
func Attach(name string) error {
	sockPath, err := session.SockPath(name)
	if err != nil {
		return err
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("client: connect: %w", err)
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

	fmt.Fprintf(os.Stderr, "pst: attached to %q (detach: Ctrl+])\r\n", name)
	setTitle(name)
	defer clearTitle()

	if ws, err := term.GetSize(stdinFd); err == nil {
		proto.WriteResize(conn, ws.Rows, ws.Cols)
	}

	// There's no scrollback replay: a reattach otherwise shows nothing
	// until the user types, since whatever prompt the shell last drew was
	// discarded while nobody was attached. Feed it a Ctrl-L so it redraws
	// its current line (the same trick dtach uses).
	proto.WriteData(conn, []byte{0x0C})

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
	go func() { detachedCh <- forwardStdin(conn) }()

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
func forwardStdin(conn net.Conn) (detached bool) {
	buf := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if i := indexByte(chunk, DetachByte); i >= 0 {
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
	fmt.Fprintf(os.Stderr, "\033]0;pst:%s\007", name)
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
	sockPath, err := session.SockPath(name)
	if err != nil {
		return err
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("client: connect: %w", err)
	}
	defer conn.Close()
	_, err = conn.Write([]byte{byte(proto.HandshakeKill)})
	return err
}
