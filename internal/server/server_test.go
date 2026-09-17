package server

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/Q0tzly/pstty/internal/proto"
)

func TestAppendScrollbackBelowCap(t *testing.T) {
	s := &sessionServer{}
	s.appendScrollback([]byte("hello"))
	s.appendScrollback([]byte(" world"))
	if got := string(s.scrollback); got != "hello world" {
		t.Errorf("scrollback = %q, want %q", got, "hello world")
	}
}

func TestAppendScrollbackTrimsToCap(t *testing.T) {
	s := &sessionServer{}
	first := bytes.Repeat([]byte("a"), scrollbackCap-10)
	s.appendScrollback(first)
	if len(s.scrollback) != len(first) {
		t.Fatalf("len = %d, want %d", len(s.scrollback), len(first))
	}

	second := bytes.Repeat([]byte("b"), 30)
	s.appendScrollback(second)
	if len(s.scrollback) != scrollbackCap {
		t.Fatalf("len = %d, want %d (capped)", len(s.scrollback), scrollbackCap)
	}
	if !bytes.Equal(s.scrollback[len(s.scrollback)-len(second):], second) {
		t.Error("scrollback tail doesn't match the most recent write")
	}
}

// connPair returns a connected client/server net.Conn pair over a real
// unix socket in a temp dir, so writes are kernel-buffered instead of
// the lockstep rendezvous net.Pipe would force. It uses a short,
// independent temp dir rather than t.TempDir(), whose test-name-derived
// path routinely blows past the ~104-byte sun_path limit on macOS.
func connPair(t *testing.T) (server, client net.Conn) {
	t.Helper()
	dir, err := os.MkdirTemp("", "psttytest")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	ln, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	acceptCh := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		acceptCh <- c
	}()

	client, err = net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	server = <-acceptCh
	if server == nil {
		t.Fatal("Accept failed")
	}
	return server, client
}

func newTestServer(t *testing.T) (*sessionServer, *os.File) {
	t.Helper()
	masterR, masterW, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	t.Cleanup(func() {
		masterR.Close()
		masterW.Close()
	})
	return &sessionServer{master: masterW, killCh: make(chan struct{})}, masterR
}

func TestServeAttachReplaysScrollbackAndNudges(t *testing.T) {
	s, masterR := newTestServer(t)

	serverConn, clientConn := connPair(t)
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		s.serveAttach(serverConn)
		close(done)
	}()

	// Empty scrollback: expect the redraw nudge on the master, not a
	// replay on the client.
	nudge := make([]byte, 1)
	if _, err := masterR.Read(nudge); err != nil {
		t.Fatalf("read nudge: %v", err)
	}
	if nudge[0] != 0x0C {
		t.Errorf("nudge byte = %#x, want Ctrl-L (0x0C)", nudge[0])
	}

	clientConn.Close()
	<-done
}

func TestServeAttachReplaysExistingScrollback(t *testing.T) {
	s, _ := newTestServer(t)
	s.scrollback = []byte("previous output\n")

	serverConn, clientConn := connPair(t)
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		s.serveAttach(serverConn)
		close(done)
	}()

	buf := make([]byte, len(s.scrollback))
	if _, err := io.ReadFull(clientConn, buf); err != nil {
		t.Fatalf("read replay: %v", err)
	}
	if string(buf) != "previous output\n" {
		t.Errorf("replay = %q, want %q", buf, "previous output\n")
	}

	clientConn.Close()
	<-done
}

func TestServeAttachEvictsPreviousClient(t *testing.T) {
	s, _ := newTestServer(t)
	s.scrollback = []byte("x") // skip the redraw-nudge write to master

	serverA, clientA := connPair(t)
	doneA := make(chan struct{})
	go func() {
		s.serveAttach(serverA)
		close(doneA)
	}()

	// Drain A's scrollback replay so its goroutine moves on to blocking
	// in ReadFrame, matching real client behavior.
	io.ReadFull(clientA, make([]byte, len(s.scrollback)))

	serverB, clientB := connPair(t)
	defer clientB.Close()
	doneB := make(chan struct{})
	go func() {
		s.serveAttach(serverB)
		close(doneB)
	}()

	buf := make([]byte, 4096)
	n, err := clientA.Read(buf)
	if err != nil {
		t.Fatalf("clientA read: %v", err)
	}
	if !bytes.Contains(buf[:n], []byte("attached from elsewhere")) {
		t.Errorf("clientA got %q, want the eviction message", buf[:n])
	}
	if _, err := clientA.Read(buf); err == nil {
		t.Error("clientA: want EOF after eviction, got more data")
	}
	<-doneA

	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	if active != serverB {
		t.Error("s.active is not the new client after eviction")
	}

	clientB.Close()
	<-doneB
}

func TestHandleConnStatus(t *testing.T) {
	s, masterR := newTestServer(t)

	status := func() bool {
		serverConn, clientConn := connPair(t)
		defer clientConn.Close()
		go s.handleConn(serverConn)

		clientConn.Write([]byte{byte(proto.HandshakeStatus)})
		var b [1]byte
		if _, err := io.ReadFull(clientConn, b[:]); err != nil {
			t.Fatalf("read status: %v", err)
		}
		return b[0] == 1
	}

	if status() {
		t.Error("status = attached, want detached (nobody attached yet)")
	}

	serverConn, clientConn := connPair(t)
	defer clientConn.Close()
	go s.serveAttach(serverConn)
	// Empty scrollback means serveAttach sets s.active and unlocks
	// before writing the redraw nudge to master, so seeing the nudge
	// here guarantees s.active is already set.
	if _, err := masterR.Read(make([]byte, 1)); err != nil {
		t.Fatalf("read nudge: %v", err)
	}

	if !status() {
		t.Error("status = detached, want attached")
	}
}
