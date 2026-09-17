// Package proto defines the tiny framing pstty uses over its Unix domain
// socket. Server -> client traffic is just the raw PTY output byte stream.
// Client -> server traffic is framed so window-resize notifications can be
// interleaved with stdin data on the same connection.
package proto

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Handshake is the single byte a client sends right after connecting, to
// tell the server what kind of session it wants.
type Handshake byte

const (
	// HandshakeAttach opens (or takes over) the interactive session.
	HandshakeAttach Handshake = 0x01
	// HandshakeKill asks the server to terminate the shell and exit.
	HandshakeKill Handshake = 0x02
)

// FrameType tags a client -> server frame.
type FrameType byte

const (
	// FrameData carries raw bytes to be written to the PTY master (i.e.
	// stdin from the attached terminal).
	FrameData FrameType = 0x01
	// FrameResize carries a new terminal size to apply to the PTY master.
	FrameResize FrameType = 0x02
)

// WriteData frames and writes a stdin chunk.
func WriteData(w io.Writer, p []byte) error {
	return writeFrame(w, FrameData, p)
}

// WriteResize frames and writes a window-size update.
func WriteResize(w io.Writer, rows, cols uint16) error {
	var payload [4]byte
	binary.BigEndian.PutUint16(payload[0:2], rows)
	binary.BigEndian.PutUint16(payload[2:4], cols)
	return writeFrame(w, FrameResize, payload[:])
}

func writeFrame(w io.Writer, t FrameType, payload []byte) error {
	var header [5]byte
	header[0] = byte(t)
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

// Frame is one decoded client -> server frame.
type Frame struct {
	Type    FrameType
	Payload []byte
}

// ReadFrame reads and decodes the next frame from r.
func ReadFrame(r io.Reader) (Frame, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Frame{}, err
	}
	t := FrameType(header[0])
	n := binary.BigEndian.Uint32(header[1:])
	if n > 1<<20 {
		return Frame{}, fmt.Errorf("proto: frame too large: %d bytes", n)
	}
	payload := make([]byte, n)
	if n > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return Frame{}, err
		}
	}
	return Frame{Type: t, Payload: payload}, nil
}

// DecodeResize extracts rows/cols from a FrameResize payload.
func DecodeResize(payload []byte) (rows, cols uint16, err error) {
	if len(payload) != 4 {
		return 0, 0, fmt.Errorf("proto: bad resize payload length: %d", len(payload))
	}
	return binary.BigEndian.Uint16(payload[0:2]), binary.BigEndian.Uint16(payload[2:4]), nil
}
