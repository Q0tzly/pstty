// Package session locates and enumerates pstty session sockets on disk.
package session

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const sockSuffix = ".sock"

// Dir returns the directory pstty stores session sockets in, creating it if
// necessary. It honors $PSTTY_DIR, then falls back to a pst/ subdirectory of
// the OS temp dir, scoped by uid so multiple users on one host don't collide.
func Dir() (string, error) {
	if d := os.Getenv("PSTTY_DIR"); d != "" {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", err
		}
		return d, nil
	}

	dir := filepath.Join(os.TempDir(), fmt.Sprintf("pst-%d", os.Getuid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// SockPath returns the socket path for the named session.
func SockPath(name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+sockSuffix), nil
}

// Info describes one on-disk session socket.
type Info struct {
	Name  string
	Path  string
	Alive bool
}

// List returns all known sessions, probing each socket for liveness.
func List() ([]Info, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var infos []Info
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), sockSuffix) {
			continue
		}
		name := strings.TrimSuffix(e.Name(), sockSuffix)
		path := filepath.Join(dir, e.Name())
		infos = append(infos, Info{Name: name, Path: path, Alive: Alive(path)})
	}
	return infos, nil
}

// Alive reports whether a session's socket currently has a listener.
func Alive(path string) bool {
	conn, err := net.DialTimeout("unix", path, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
