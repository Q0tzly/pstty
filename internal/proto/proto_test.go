package proto

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestWriteReadDataFrame(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteData(&buf, []byte("hello")); err != nil {
		t.Fatalf("WriteData: %v", err)
	}

	f, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if f.Type != FrameData {
		t.Errorf("Type = %v, want FrameData", f.Type)
	}
	if string(f.Payload) != "hello" {
		t.Errorf("Payload = %q, want %q", f.Payload, "hello")
	}
}

func TestWriteReadResizeFrame(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteResize(&buf, 24, 80); err != nil {
		t.Fatalf("WriteResize: %v", err)
	}

	f, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if f.Type != FrameResize {
		t.Errorf("Type = %v, want FrameResize", f.Type)
	}

	rows, cols, err := DecodeResize(f.Payload)
	if err != nil {
		t.Fatalf("DecodeResize: %v", err)
	}
	if rows != 24 || cols != 80 {
		t.Errorf("rows,cols = %d,%d, want 24,80", rows, cols)
	}
}

func TestReadFrameEmptyPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteData(&buf, nil); err != nil {
		t.Fatalf("WriteData: %v", err)
	}

	f, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if len(f.Payload) != 0 {
		t.Errorf("Payload = %v, want empty", f.Payload)
	}
}

func TestReadFrameMultipleInSequence(t *testing.T) {
	var buf bytes.Buffer
	WriteData(&buf, []byte("a"))
	WriteResize(&buf, 1, 2)
	WriteData(&buf, []byte("bc"))

	wantTypes := []FrameType{FrameData, FrameResize, FrameData}
	for i, want := range wantTypes {
		f, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame #%d: %v", i, err)
		}
		if f.Type != want {
			t.Errorf("frame #%d Type = %v, want %v", i, f.Type, want)
		}
	}
}

func TestReadFrameTooLarge(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(byte(FrameData))
	var lenBytes [4]byte
	binary.BigEndian.PutUint32(lenBytes[:], 1<<21) // over the 1<<20 cap
	buf.Write(lenBytes[:])

	if _, err := ReadFrame(&buf); err == nil {
		t.Fatal("ReadFrame: want error for an oversized frame, got nil")
	}
}

func TestReadFrameTruncated(t *testing.T) {
	var buf bytes.Buffer
	WriteData(&buf, []byte("hello"))
	truncated := buf.Bytes()[:len(buf.Bytes())-2]

	if _, err := ReadFrame(bytes.NewReader(truncated)); err == nil {
		t.Fatal("ReadFrame: want error for a truncated frame, got nil")
	}
}

func TestDecodeResizeBadLength(t *testing.T) {
	if _, _, err := DecodeResize([]byte{1, 2, 3}); err == nil {
		t.Fatal("DecodeResize: want error for a bad payload length, got nil")
	}
}
