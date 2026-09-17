// Package client implements the pstty attach client: it bridges the local
// terminal's stdin/stdout with a session's Unix domain socket.
package client

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
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
// (read-write) until the session ends or the user detaches with the
// detach key (DefaultDetachByte unless detach overrides it). replay
// controls whether the server replays recent scrollback on attach.
func Attach(name string, detach byte, replay bool) error {
	hs := proto.HandshakeAttach
	if !replay {
		hs = proto.HandshakeAttachNoReplay
	}
	conn, err := dial(name)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{byte(hs)}); err != nil {
		return fmt.Errorf("client: handshake: %w", err)
	}
	return bridge(conn, name, detach, true)
}

// Watch connects to name's socket as a read-only observer: it shows the
// session's output like Attach, but never sends input and never takes
// over (or evicts) the read-write client.
func Watch(name string, detach byte) error {
	conn, err := dial(name)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{byte(proto.HandshakeWatch)}); err != nil {
		return fmt.Errorf("client: handshake: %w", err)
	}
	return bridge(conn, name, detach, false)
}

// bridge runs the local terminal side of an attach or watch: raw mode,
// the banner/title, output streaming, and waiting for detach or the
// remote end to close. forwardInput distinguishes Attach (keystrokes go
// to the PTY, resize is reported) from Watch (input is only checked for
// the detach key).
func bridge(conn net.Conn, name string, detach byte, forwardInput bool) error {
	stdinFd := int(os.Stdin.Fd())
	state, err := term.MakeRaw(stdinFd)
	if err != nil {
		return fmt.Errorf("client: set raw mode: %w", err)
	}
	defer term.Restore(stdinFd, state)

	verb := "attached to"
	if !forwardInput {
		verb = "watching"
	}
	fmt.Fprintf(os.Stderr, "pst: %s %q (detach: %s)\r\n", verb, name, formatKey(detach))
	setTitle(name)
	defer clearTitle()

	if forwardInput {
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
	}

	outDone := make(chan struct{})
	go func() {
		io.Copy(os.Stdout, conn)
		close(outDone)
	}()

	// The input goroutine blocks on a plain os.Stdin.Read, which can't
	// be interrupted from here. Run it in its own goroutine and race it
	// against outDone: if the remote side closes the connection (session
	// ended, or this attach got rejected) while the user hasn't typed
	// anything, we still want to exit immediately rather than wait for a
	// keystroke that stops the blocked read. Process exit cleans up the
	// leftover goroutine.
	detachedCh := make(chan bool, 1)
	go func() {
		if forwardInput {
			detachedCh <- forwardStdin(conn, detach)
		} else {
			detachedCh <- watchStdin(detach)
		}
	}()

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

// watchStdin reads local input and discards it, only checking for the
// detach key, since a watcher's input is never forwarded to the PTY.
func watchStdin(detach byte) (detached bool) {
	buf := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 && indexByte(buf[:n], detach) >= 0 {
			return true
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

// Exec runs cmd inside name's session and returns its output and exit
// code, without needing a real terminal: it doesn't set raw mode, skips
// scrollback replay (there's nothing old to show for a fresh command),
// and disconnects as soon as the command finishes rather than staying
// attached. Like Attach, it evicts any existing read-write client.
//
// The command is base64-encoded and piped through `bash` on the far
// side rather than typed as-is, so arbitrary quoting in cmd can't
// interfere with what's effectively a single line of raw keystrokes;
// a random marker echoed after it (with $?) marks completion and
// carries the exit code, since there's no other way to tell a shell
// prompt from command output over a raw PTY byte stream.
func Exec(name, cmd string) (output string, exitCode int, err error) {
	conn, err := dial(name)
	if err != nil {
		return "", 0, err
	}
	defer conn.Close()

	if _, err := conn.Write([]byte{byte(proto.HandshakeAttachNoReplay)}); err != nil {
		return "", 0, fmt.Errorf("client: handshake: %w", err)
	}

	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return "", 0, fmt.Errorf("client: generate marker: %w", err)
	}
	marker := "PSTDONE_" + hex.EncodeToString(nonce)

	encoded := base64.StdEncoding.EncodeToString([]byte(cmd))
	wrapped := fmt.Sprintf("echo %s | base64 -d | bash; echo %s_$?\r", encoded, marker)
	if err := proto.WriteData(conn, []byte(wrapped)); err != nil {
		return "", 0, fmt.Errorf("client: send command: %w", err)
	}

	var buf bytes.Buffer
	tmp := make([]byte, 4096)
	needle := []byte(marker + "_")
	searchFrom := 0
	for {
		n, rerr := conn.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}

		for {
			rel := bytes.Index(buf.Bytes()[searchFrom:], needle)
			if rel < 0 {
				break
			}
			idx := searchFrom + rel
			rest := buf.Bytes()[idx+len(needle):]
			end := bytes.IndexAny(rest, "\r\n")
			if end < 0 {
				break // exit-code digits haven't fully arrived yet
			}
			if code, err := strconv.Atoi(string(rest[:end])); err == nil {
				return trimEcho(buf.String()[:idx], encoded), code, nil
			}
			// This occurrence isn't followed by a real exit code (the
			// remote pty echoing our own typed command back verbatim,
			// literal "$?" and all, looks identical up to this point).
			// Keep searching past it for the real one.
			searchFrom = idx + len(needle)
		}

		if rerr != nil {
			return buf.String(), -1, fmt.Errorf("client: connection closed before command finished: %w", rerr)
		}
	}
}

// trimEcho drops everything up through the line containing echoNeedle
// (the base64 payload unique to this Exec call), since that line is
// just the remote pty echoing our own injected command back — along
// with whatever prompt redraw preceded it — not part of the command's
// actual output. If it can't be found (shouldn't happen), output is
// returned unchanged rather than guessing.
func trimEcho(output, echoNeedle string) string {
	i := strings.LastIndex(output, echoNeedle)
	if i < 0 {
		return output
	}
	if nl := strings.IndexByte(output[i:], '\n'); nl >= 0 {
		return output[i+nl+1:]
	}
	return output
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
