package http

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/bingyuegong/go-ios/ios/golog"
	"golang.org/x/net/http2"
)

const logModule = "go-ios/http"

type StreamId uint32

const (
	InitStream   = StreamId(0)
	ClientServer = StreamId(1)
	ServerClient = StreamId(3)
)

// windowUpdateThreshold is the number of received DATA bytes after which the
// consumed flow-control window is returned to the peer. The advertised
// windows are 1 MiB, so returning credit every 32 KiB keeps the peer from
// ever stalling. Without WINDOW_UPDATE frames the device stops sending once
// 1 MiB has been received on a connection (observed with large
// ListDirectoryFileNodes replies on iOS 18.5).
const windowUpdateThreshold = 32 * 1024

// HttpConnection is a wrapper around a http2.Framer that provides a simple interface to read and write http2 streams for iOS17+.
type HttpConnection struct {
	framer             *http2.Framer
	clientServerStream *bytes.Buffer
	serverClientStream *bytes.Buffer
	closer             io.Closer
	csIsOpen           *atomic.Bool
	scIsOpen           *atomic.Bool

	// writeMu serializes frame writes: the reader path also writes
	// (SETTINGS ACK, WINDOW_UPDATE) while callers may be sending data.
	writeMu sync.Mutex
	// unacknowledged DATA bytes per stream ID (0 = connection level).
	unacknowledged map[uint32]uint32
}

func (r *HttpConnection) Close() error {
	return r.closer.Close()
}

func NewHttpConnection(rw io.ReadWriteCloser) (*HttpConnection, error) {
	framer := http2.NewFramer(rw, rw)

	_, err := rw.Write([]byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"))
	if err != nil {
		return nil, fmt.Errorf("NewHttpConnection: could not write PRI. %w", err)
	}

	err = framer.WriteSettings(
		http2.Setting{ID: http2.SettingMaxConcurrentStreams, Val: 100},
		http2.Setting{ID: http2.SettingInitialWindowSize, Val: 1048576},
	)
	if err != nil {
		return nil, fmt.Errorf("NewHttpConnection: could not write settings. %w", err)
	}

	err = framer.WriteWindowUpdate(uint32(InitStream), 983041)
	if err != nil {
		return nil, fmt.Errorf("NewHttpConnection: could not write window update. %w", err)
	}
	//
	frame, err := framer.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("NewHttpConnection: could not read frame. %w", err)
	}
	if frame.Header().Type == http2.FrameSettings {
		settings := frame.(*http2.SettingsFrame)
		v, ok := settings.Value(http2.SettingInitialWindowSize)
		if ok {
			framer.SetMaxReadFrameSize(v)
		}
		err := framer.WriteSettingsAck()
		if err != nil {
			return nil, fmt.Errorf("NewHttpConnection: could not write settings ack. %w", err)
		}
	} else {
		golog.Warn("expected setttings frame", "module", logModule, "frame", frame.Header().String())
	}

	return &HttpConnection{
		framer:             framer,
		clientServerStream: bytes.NewBuffer(nil),
		serverClientStream: bytes.NewBuffer(nil),
		closer:             rw,
		csIsOpen:           &atomic.Bool{},
		scIsOpen:           &atomic.Bool{},
		unacknowledged:     map[uint32]uint32{},
	}, nil
}

func (r *HttpConnection) ReadClientServerStream(p []byte) (int, error) {
	for r.clientServerStream.Len() < len(p) {
		err := r.readDataFrame()
		if err != nil {
			return 0, fmt.Errorf("ReadClientServerStream: %w", err)
		}
	}
	return r.clientServerStream.Read(p)
}

func (r *HttpConnection) WriteClientServerStream(p []byte) (int, error) {
	return r.write(p, uint32(ClientServer), r.csIsOpen)
}

func (r *HttpConnection) WriteServerClientStream(p []byte) (int, error) {
	return r.write(p, uint32(ServerClient), r.scIsOpen)
}

func (r *HttpConnection) write(p []byte, stream uint32, isOpen *atomic.Bool) (int, error) {
	if isOpen.CompareAndSwap(false, true) {
		r.writeMu.Lock()
		err := r.framer.WriteHeaders(http2.HeadersFrameParam{
			StreamID:   stream,
			EndHeaders: true,
		})
		r.writeMu.Unlock()
		if err != nil {
			return 0, fmt.Errorf("write: could not send headers. %w", err)
		}
	}
	return r.Write(p, stream)
}

func (r *HttpConnection) Write(p []byte, streamId uint32) (int, error) {
	r.writeMu.Lock()
	err := r.framer.WriteData(streamId, false, p)
	r.writeMu.Unlock()
	if err != nil {
		return 0, fmt.Errorf("Write: could not write data. %w", err)
	}
	return len(p), nil
}

func (r *HttpConnection) readDataFrame() error {
	for {
		f, err := r.framer.ReadFrame()
		if err != nil {
			return fmt.Errorf("readDataFrame: could not read frame. %w", err)
		}
		switch f.Header().Type {
		case http2.FrameData:
			d := f.(*http2.DataFrame)
			switch d.StreamID {
			case 1:
				r.clientServerStream.Write(d.Data())
			case 3:
				r.serverClientStream.Write(d.Data())
			default:
				return fmt.Errorf("readDataFrame: unknown stream id %d", d.StreamID)
			}
			if err := r.consumeFlowControl(d.StreamID, d.Header().Length); err != nil {
				return fmt.Errorf("readDataFrame: %w", err)
			}
			return nil
		case http2.FrameGoAway:
			return fmt.Errorf("received GOAWAY")
		case http2.FrameSettings:
			s := f.(*http2.SettingsFrame)
			if s.Flags&http2.FlagSettingsAck != http2.FlagSettingsAck {
				r.writeMu.Lock()
				err := r.framer.WriteSettingsAck()
				r.writeMu.Unlock()
				if err != nil {
					return fmt.Errorf("readDataFrame: could not write settings ack. %w", err)
				}
			}
		case http2.FrameRSTStream:
			r := f.(*http2.RSTStreamFrame)
			return fmt.Errorf("readDataFrame: got RST frame with error code: %s", r.ErrCode.String())
		default:
			break
		}
	}
}

// consumeFlowControl records length received DATA bytes (including padding,
// which counts against the window) on streamID and returns the credit to the
// peer with WINDOW_UPDATE frames for the stream and the connection once
// windowUpdateThreshold bytes have accumulated.
func (r *HttpConnection) consumeFlowControl(streamID uint32, length uint32) error {
	if length == 0 {
		return nil
	}
	if r.unacknowledged == nil {
		r.unacknowledged = map[uint32]uint32{}
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	for _, id := range []uint32{uint32(InitStream), streamID} {
		r.unacknowledged[id] += length
		if r.unacknowledged[id] < windowUpdateThreshold {
			continue
		}
		if err := r.framer.WriteWindowUpdate(id, r.unacknowledged[id]); err != nil {
			return fmt.Errorf("could not write window update for stream %d: %w", id, err)
		}
		r.unacknowledged[id] = 0
	}
	return nil
}

func (r *HttpConnection) ReadServerClientStream(p []byte) (int, error) {
	for r.serverClientStream.Len() < len(p) {
		err := r.readDataFrame()
		if err != nil {
			return 0, err
		}
	}
	return r.serverClientStream.Read(p)
}

type HttpStreamReadWriter struct {
	h        *HttpConnection
	streamId uint32
}

func NewStreamReadWriter(h *HttpConnection, streamId StreamId) HttpStreamReadWriter {
	return HttpStreamReadWriter{
		h:        h,
		streamId: uint32(streamId),
	}
}

func (h HttpStreamReadWriter) Read(p []byte) (n int, err error) {
	if h.streamId == 1 {
		return h.h.ReadClientServerStream(p)
	}
	if h.streamId == 3 {
		return h.h.ReadServerClientStream(p)
	}
	return 0, fmt.Errorf("Read: unknown stream id %d", h.streamId)
}

func (h HttpStreamReadWriter) Write(p []byte) (n int, err error) {
	if h.streamId == 1 {
		return h.h.WriteClientServerStream(p)
	}
	if h.streamId == 3 {
		return h.h.WriteServerClientStream(p)
	}
	return 0, fmt.Errorf("Write: unknown stream id %d", h.streamId)
}
