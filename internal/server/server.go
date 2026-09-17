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

	s := &sessionServer{master: p.Master, killCh: make(chan struct{})}

	// A single long-lived reader drains the PTY master for the whole
	// life of the session (not per client): this keeps the shell from
	// blocking on a full tty output buffer while nobody is attached, and
	// avoids two goroutines racing to read the master across a
	// detach/reattach. Output is simply dropped when no client is
	// attached (no scrollback replay in this version).
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
	s.mu.Unlock()
	<-acceptDone
	cmd.Process.Kill()
	p.Master.Close()
	os.Remove(sockPath)
	return nil
}

type sessionServer struct {
	master *os.File
	killCh chan struct{}

	mu     sync.Mutex
	active net.Conn // current attached client, if any
}

func (s *sessionServer) pumpMaster() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.master.Read(buf)
		if n > 0 {
			s.mu.Lock()
			active := s.active
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
		}
		if err != nil {
			return
		}
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
		s.serveAttach(conn)
	default:
		// Unknown handshake: drop the connection.
	}
}

func (s *sessionServer) serveAttach(conn net.Conn) {
	s.mu.Lock()
	if s.active != nil {
		s.mu.Unlock()
		conn.Write([]byte("pst: session already has an attached client\r\n"))
		return
	}
	s.active = conn
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		if s.active == conn {
			s.active = nil
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
