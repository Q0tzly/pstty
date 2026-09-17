package term

import (
	"testing"

	"github.com/Q0tzly/pstty/internal/pty"
	"golang.org/x/sys/unix"
)

func TestMakeRawAndRestore(t *testing.T) {
	p, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer p.Close()

	fd := int(p.Slave.Fd())

	before, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		t.Fatalf("IoctlGetTermios: %v", err)
	}

	state, err := MakeRaw(fd)
	if err != nil {
		t.Fatalf("MakeRaw: %v", err)
	}

	raw, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		t.Fatalf("IoctlGetTermios after MakeRaw: %v", err)
	}
	if raw.Lflag&unix.ECHO != 0 {
		t.Error("MakeRaw: ECHO still set")
	}
	if raw.Lflag&unix.ICANON != 0 {
		t.Error("MakeRaw: ICANON still set")
	}
	if raw.Oflag&unix.OPOST != 0 {
		t.Error("MakeRaw: OPOST still set (this is the flag whose leftover-raw-mode bug garbled the prompt on detach)")
	}

	if err := Restore(fd, state); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	after, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		t.Fatalf("IoctlGetTermios after Restore: %v", err)
	}
	if after.Lflag != before.Lflag || after.Oflag != before.Oflag ||
		after.Iflag != before.Iflag || after.Cflag != before.Cflag {
		t.Errorf("Restore did not fully restore termios: got %+v, want %+v", after, before)
	}
}

func TestGetSizeSetSize(t *testing.T) {
	p, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer p.Close()

	fd := int(p.Master.Fd())
	want := Winsize{Rows: 40, Cols: 100}
	if err := SetSize(fd, want); err != nil {
		t.Fatalf("SetSize: %v", err)
	}
	got, err := GetSize(fd)
	if err != nil {
		t.Fatalf("GetSize: %v", err)
	}
	if got != want {
		t.Errorf("GetSize = %+v, want %+v", got, want)
	}
}
