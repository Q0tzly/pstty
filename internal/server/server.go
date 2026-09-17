// Package server implements the pstty session server: it owns a PTY and the
// shell running inside it, and serves that session to clients over a Unix
// domain socket.
package server

import (
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/Q0tzly/pstty/internal/proto"
	"github.com/Q0tzly/pstty/internal/pty"
	"github.com/Q0tzly/pstty/internal/session"
	"github.com/Q0tzly/pstty/internal/term"
)

// Run creates the session's PTY and shell, listens on its Unix socket, and
// serves clients until the shell exits or it is killed. It does not return
// until the session ends.
func Run(name string) error {
	sockPath, err := session.SockPath(name)
	if err != nil {
		return err
	}

	// Clean up a stale socket file left behind by a server that died
	// without a clean shutdown.
	if !session.Alive(sockPath) {
		os.Remove(sockPath)
	}

	p, err := pty.Open()
	if err != nil {
		return err
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	cmd := exec.Command(shell, "-l")
	cmd.Stdin = p.Slave
	cmd.Stdout = p.Slave
	cmd.Stderr = p.Slave
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
	}
	cmd.Env = append(os.Environ(), "PSTTY_SESSION="+name)

	if err := cmd.Start(); err != nil {
		p.Close()
		return err
	}
	log.Printf("pst: started session %q (shell=%s, pid=%d)", name, shell, cmd.Process.Pid)
	// The slave fd's controlling-terminal duty now belongs to the child;
	// the server only needs the master end.
	p.Slave.Close()

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		cmd.Process.Kill()
		p.Master.Close()
		return err
	}

	shellDone := make(chan struct{})
	go func() {
		cmd.Wait()
		close(shellDone)
	}()

	s := &sessionServer{master: p.Master, killCh: make(chan struct{}), watchers: make(map[net.Conn]bool)}

	// A single long-lived reader drains the PTY master for the whole
	// life of the session (not per client): this keeps the shell from
	// blocking on a full tty output buffer while nobody is attached, and
	// avoids two goroutines racing to read the master across a
	// detach/reattach. Output produced while no client is attached is
	// kept in a bounded scrollback buffer and replayed on the next
	// attach, rather than forwarded live.
	masterDone := make(chan struct{})
	go func() {
		s.pumpMaster()
		close(masterDone)
	}()

	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		s.acceptLoop(ln)
	}()

	select {
	case <-shellDone:
		log.Printf("pst: shell exited, tearing down session %q", name)
	case <-masterDone:
		log.Printf("pst: pty closed, tearing down session %q", name)
	case <-s.killCh:
		log.Printf("pst: kill requested, tearing down session %q", name)
	}

	ln.Close()
	s.mu.Lock()
	if s.active != nil {
		s.active.Close()
	}
	for w := range s.watchers {
		w.Close()
	}
	s.mu.Unlock()
	<-acceptDone
	cmd.Process.Kill()
	p.Master.Close()
	os.Remove(sockPath)
	return nil
}

// scrollbackCap bounds how much recent output is kept to replay to the
// next client, so a session left detached for a long time with a noisy
// process running doesn't grow this without limit.
const scrollbackCap = 64 * 1024

type sessionServer struct {
	master *os.File
	killCh chan struct{}

	mu         sync.Mutex
	active     net.Conn          // current attached (read-write) client, if any
	watchers   map[net.Conn]bool // read-only clients watching the output
	scrollback []byte            // last scrollbackCap bytes of master output
}

func (s *sessionServer) pumpMaster() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.master.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.appendScrollback(buf[:n])
			active := s.active
			watchers := make([]net.Conn, 0, len(s.watchers))
			for w := range s.watchers {
				watchers = append(watchers, w)
			}
			s.mu.Unlock()

			if active != nil {
				if _, werr := active.Write(buf[:n]); werr != nil {
					s.mu.Lock()
					if s.active == active {
						s.active = nil
					}
					s.mu.Unlock()
					active.Close()
				}
			}
			for _, w := range watchers {
				if _, werr := w.Write(buf[:n]); werr != nil {
					s.mu.Lock()
					delete(s.watchers, w)
					s.mu.Unlock()
					w.Close()
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// appendScrollback appends p to the scrollback buffer, trimming from the
// front if it grows past scrollbackCap. Caller must hold s.mu.
func (s *sessionServer) appendScrollback(p []byte) {
	s.scrollback = append(s.scrollback, p...)
	if over := len(s.scrollback) - scrollbackCap; over > 0 {
		n := copy(s.scrollback, s.scrollback[over:])
		s.scrollback = s.scrollback[:n]
	}
}

func (s *sessionServer) acceptLoop(ln net.Listener) {
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handleConn(conn)
		}()
	}
}

func (s *sessionServer) handleConn(conn net.Conn) {
	defer conn.Close()

	var hs [1]byte
	if _, err := io.ReadFull(conn, hs[:]); err != nil {
		return
	}

	switch proto.Handshake(hs[0]) {
	case proto.HandshakeKill:
		select {
		case <-s.killCh:
		default:
			close(s.killCh)
		}
	case proto.HandshakeAttach:
		s.serveAttach(conn, true)
	case proto.HandshakeAttachNoReplay:
		s.serveAttach(conn, false)
	case proto.HandshakeWatch:
		s.serveWatch(conn)
	case proto.HandshakeStatus:
		s.mu.Lock()
		attached := s.active != nil
		s.mu.Unlock()
		var b byte
		if attached {
			b = 1
		}
		conn.Write([]byte{b})
	default:
		// Unknown handshake: drop the connection.
	}
}

// liveMarker separates replayed scrollback from what's actually
// happening now, so it isn't mistaken for current state (a stale
// "Password:" prompt sitting in scrollback, say).
const liveMarker = "\r\n\x1b[2m--- live ---\x1b[0m\r\n"

func (s *sessionServer) serveAttach(conn net.Conn, replay bool) {
	s.mu.Lock()
	if prev := s.active; prev != nil {
		// Switch the session over to the new client rather than
		// rejecting it. A stale connection (crashed terminal, a
		// killed `go run` wrapper, a forgotten nested attach from
		// inside another session) would otherwise lock the session
		// out from ever being attached again from a clean client.
		log.Print("pst: evicting previous client for a new attach")
		prev.Write([]byte("\r\npst: attached from elsewhere, disconnecting\r\n"))
		prev.Close()
		conn.Write([]byte("pst: an existing client was disconnected to make room for this attach\r\n"))
	}
	// Replay recent output so reattaching isn't silent, under the same
	// lock as setting s.active so pumpMaster can't interleave a live
	// write with the replay or duplicate it.
	hadScrollback := replay && len(s.scrollback) > 0
	if hadScrollback {
		conn.Write(s.scrollback)
		conn.Write([]byte(liveMarker))
	}
	s.active = conn
	s.mu.Unlock()
	log.Print("pst: client attached")

	if !hadScrollback {
		// Nothing replayed (a brand-new session, or replay was skipped):
		// nudge the shell to draw its prompt so attaching doesn't look
		// hung. Skipped when scrollback was replayed, since that already
		// ends with whatever the shell last drew.
		s.master.Write([]byte{0x0C})
	}

	defer func() {
		s.mu.Lock()
		if s.active == conn {
			s.active = nil
			log.Print("pst: client detached")
		}
		s.mu.Unlock()
	}()

	// Client -> PTY input/control frames, until the client disconnects
	// or the session is torn down (which closes conn out from under us).
	for {
		f, err := proto.ReadFrame(conn)
		if err != nil {
			return
		}
		switch f.Type {
		case proto.FrameData:
			s.master.Write(f.Payload)
		case proto.FrameResize:
			rows, cols, err := proto.DecodeResize(f.Payload)
			if err == nil {
				term.SetSize(int(s.master.Fd()), term.Winsize{Rows: rows, Cols: cols})
			}
		}
	}
}

// serveWatch adds conn as a read-only observer: it receives output like
// an attach, but never writes to the PTY and never becomes (or evicts)
// the active client.
func (s *sessionServer) serveWatch(conn net.Conn) {
	s.mu.Lock()
	hadScrollback := len(s.scrollback) > 0
	if hadScrollback {
		conn.Write(s.scrollback)
		conn.Write([]byte(liveMarker))
	}
	s.watchers[conn] = true
	s.mu.Unlock()
	log.Print("pst: watcher attached")

	defer func() {
		s.mu.Lock()
		delete(s.watchers, conn)
		s.mu.Unlock()
		log.Print("pst: watcher detached")
	}()

	// Nothing a watcher sends is acted on; just block until it
	// disconnects (or the session tears down conn out from under us).
	for {
		if _, err := proto.ReadFrame(conn); err != nil {
			return
		}
	}
}
