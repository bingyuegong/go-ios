package http

import (
	"bytes"
	"io"
	"sync/atomic"
	"testing"

	"golang.org/x/net/http2"
)

// newTestConnection returns a connection that reads frames from in and
// writes frames to out.
func newTestConnection(in io.Reader, out io.Writer) *HttpConnection {
	return &HttpConnection{
		framer:             http2.NewFramer(out, in),
		clientServerStream: bytes.NewBuffer(nil),
		serverClientStream: bytes.NewBuffer(nil),
		closer:             io.NopCloser(nil),
		csIsOpen:           &atomic.Bool{},
		scIsOpen:           &atomic.Bool{},
		unacknowledged:     map[uint32]uint32{},
	}
}

func windowUpdates(t *testing.T, written []byte) map[uint32]uint32 {
	t.Helper()
	framer := http2.NewFramer(io.Discard, bytes.NewReader(written))
	updates := map[uint32]uint32{}
	for {
		frame, err := framer.ReadFrame()
		if err == io.EOF {
			return updates
		}
		if err != nil {
			t.Fatalf("read written frame: %v", err)
		}
		if update, ok := frame.(*http2.WindowUpdateFrame); ok {
			updates[update.StreamID] += update.Increment
		}
	}
}

func TestReadReturnsFlowControlCredit(t *testing.T) {
	var incoming bytes.Buffer
	peer := http2.NewFramer(&incoming, nil)
	chunk := bytes.Repeat([]byte{'x'}, 16*1024)
	const frames = 80 // 1.25 MiB, more than the advertised 1 MiB window
	for i := 0; i < frames; i++ {
		if err := peer.WriteData(uint32(ClientServer), false, chunk); err != nil {
			t.Fatal(err)
		}
	}
	var written bytes.Buffer
	conn := newTestConnection(&incoming, &written)

	received := make([]byte, len(chunk)*frames)
	if _, err := io.ReadFull(HttpStreamReadWriter{h: conn, streamId: uint32(ClientServer)}, received); err != nil {
		t.Fatalf("read: %v", err)
	}

	updates := windowUpdates(t, written.Bytes())
	total := uint32(len(received))
	for _, id := range []uint32{uint32(InitStream), uint32(ClientServer)} {
		if updates[id] != total {
			t.Fatalf("stream %d credit = %d, want %d (updates=%v)", id, updates[id], total, updates)
		}
	}
}

func TestSmallReadsBelowThresholdDoNotWriteWindowUpdates(t *testing.T) {
	var incoming bytes.Buffer
	peer := http2.NewFramer(&incoming, nil)
	if err := peer.WriteData(uint32(ServerClient), false, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	var written bytes.Buffer
	conn := newTestConnection(&incoming, &written)
	buf := make([]byte, 5)
	if _, err := conn.ReadServerClientStream(buf); err != nil {
		t.Fatal(err)
	}
	if written.Len() != 0 {
		t.Fatalf("unexpected frames written for 5 bytes: %d bytes", written.Len())
	}
}
