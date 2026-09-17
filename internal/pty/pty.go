// Package pty opens PTY master/slave pairs directly via
// golang.org/x/sys/unix syscalls, without depending on a third-party PTY
// library.
package pty

import "os"

// PTY holds an open master/slave pseudo-terminal pair.
type PTY struct {
	Master *os.File
	Slave  *os.File
}

// Close closes both ends of the pair.
func (p *PTY) Close() error {
	err := p.Master.Close()
	if serr := p.Slave.Close(); err == nil {
		err = serr
	}
	return err
}

// Open allocates a new PTY master/slave pair. The platform-specific
// implementation lives in pty_linux.go / pty_darwin.go.
func Open() (*PTY, error) {
	return open()
}
