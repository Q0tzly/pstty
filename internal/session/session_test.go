package session

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/Q0tzly/pstty/internal/proto"
)

// shortTempDir is like t.TempDir(), but without the test-name-derived
// path component: once a unix socket lives under it, that routinely
// blows past the ~104-byte sun_path limit on macOS.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "psttytest")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestSockPathHonorsPSTTYDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PSTTY_DIR", dir)

	path, err := SockPath("foo")
	if err != nil {
		t.Fatalf("SockPath: %v", err)
	}
	if want := filepath.Join(dir, "foo.sock"); path != want {
		t.Errorf("SockPath = %q, want %q", path, want)
	}
}

func TestLogPathHonorsPSTTYDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PSTTY_DIR", dir)

	path, err := LogPath("foo")
	if err != nil {
		t.Fatalf("LogPath: %v", err)
	}
	if want := filepath.Join(dir, "foo.log"); path != want {
		t.Errorf("LogPath = %q, want %q", path, want)
	}
}

func TestAliveFalseForMissingSocket(t *testing.T) {
	dir := t.TempDir()
	if Alive(filepath.Join(dir, "nope.sock")) {
		t.Error("Alive = true for a socket that doesn't exist")
	}
}

// fakeStatusServer accepts connections on path and answers the status
// handshake with attached, for exercising Status/Alive/List without a
// real sessionServer.
func fakeStatusServer(t *testing.T, path string, attached bool) net.Listener {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var hs [1]byte
				if _, err := conn.Read(hs[:]); err != nil {
					return
				}
				if proto.Handshake(hs[0]) == proto.HandshakeStatus {
					var b byte
					if attached {
						b = 1
					}
					conn.Write([]byte{b})
				}
			}()
		}
	}()
	return ln
}

func TestStatusReportsAttached(t *testing.T) {
	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "attached.sock")
	ln := fakeStatusServer(t, sockPath, true)
	defer ln.Close()

	alive, attached := Status(sockPath)
	if !alive {
		t.Fatal("Status: alive = false, want true")
	}
	if !attached {
		t.Error("Status: attached = false, want true")
	}
}

func TestStatusReportsDetached(t *testing.T) {
	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "detached.sock")
	ln := fakeStatusServer(t, sockPath, false)
	defer ln.Close()

	alive, attached := Status(sockPath)
	if !alive {
		t.Fatal("Status: alive = false, want true")
	}
	if attached {
		t.Error("Status: attached = true, want false")
	}
}

func TestListFiltersToSockFilesAndReportsLiveness(t *testing.T) {
	dir := shortTempDir(t)
	t.Setenv("PSTTY_DIR", dir)

	ln := fakeStatusServer(t, filepath.Join(dir, "live.sock"), true)
	defer ln.Close()

	if err := os.WriteFile(filepath.Join(dir, "dead.sock"), nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "live.log"), nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	infos, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("List returned %d entries, want 2 (.txt/.log must be ignored): %+v", len(infos), infos)
	}

	byName := map[string]Info{}
	for _, info := range infos {
		byName[info.Name] = info
	}

	live, ok := byName["live"]
	if !ok {
		t.Fatal(`List: "live" missing`)
	}
	if !live.Alive || !live.Attached {
		t.Errorf(`List: "live" = %+v, want Alive=true Attached=true`, live)
	}

	dead, ok := byName["dead"]
	if !ok {
		t.Fatal(`List: "dead" missing`)
	}
	if dead.Alive {
		t.Errorf(`List: "dead" = %+v, want Alive=false`, dead)
	}
}
