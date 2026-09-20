package ios

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestDialDevicePortContextConnectsSelectedDeviceAndPort(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	serverErr := make(chan error, 1)
	go func() {
		request, err := readTestMuxMessage(serverConn)
		if err != nil {
			serverErr <- err
			return
		}
		payload, err := ParsePlist(request.Payload)
		if err != nil {
			serverErr <- err
			return
		}
		if got := payload["MessageType"]; got != "Connect" {
			serverErr <- &testProtocolError{message: "unexpected message type"}
			return
		}
		if got := payload["DeviceID"]; got != uint64(42) {
			serverErr <- &testProtocolError{message: "unexpected device ID"}
			return
		}
		if got := payload["PortNumber"]; got != uint64(Ntohs(8100)) {
			serverErr <- &testProtocolError{message: "unexpected port"}
			return
		}
		if err := writeTestMuxResponse(serverConn, request.Header.Tag, 0); err != nil {
			serverErr <- err
			return
		}

		buffer := make([]byte, 4)
		if _, err := io.ReadFull(serverConn, buffer); err != nil {
			serverErr <- err
			return
		}
		if string(buffer) != "ping" {
			serverErr <- &testProtocolError{message: "unexpected device payload"}
			return
		}
		_, err = serverConn.Write([]byte("pong"))
		serverErr <- err
	}()

	dial := func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}
	conn, err := dialDevicePortContext(context.Background(), 42, 8100, "tcp://usbmux.test:27015", dial)
	if err != nil {
		t.Fatalf("dialDevicePortContext: %v", err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write device payload: %v", err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatalf("read device payload: %v", err)
	}
	if string(response) != "pong" {
		t.Fatalf("response = %q, want pong", response)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("fake usbmux server: %v", err)
	}
}

func TestDialDevicePortContextRejectsUsbmuxConnectFailure(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })

	go func() {
		request, err := readTestMuxMessage(serverConn)
		if err == nil {
			err = writeTestMuxResponse(serverConn, request.Header.Tag, 3)
		}
		_ = serverConn.Close()
	}()

	dial := func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}
	conn, err := dialDevicePortContext(context.Background(), 42, 8100, "tcp://usbmux.test:27015", dial)
	if conn != nil {
		_ = conn.Close()
		t.Fatal("connection returned after usbmux rejection")
	}
	if err == nil || !strings.Contains(err.Error(), "error code:3") {
		t.Fatalf("error = %v, want usbmux error code 3", err)
	}
}

func TestDialDevicePortContextHonorsCancellationDuringHandshake(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })

	go func() {
		_, _ = readTestMuxMessage(serverConn)
		_, _ = io.Copy(io.Discard, serverConn)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	dial := func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}
	conn, err := dialDevicePortContext(ctx, 42, 8100, "tcp://usbmux.test:27015", dial)
	if conn != nil {
		_ = conn.Close()
		t.Fatal("connection returned after context timeout")
	}
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
}

func readTestMuxMessage(conn net.Conn) (UsbMuxMessage, error) {
	var header UsbMuxHeader
	if err := binary.Read(conn, binary.LittleEndian, &header); err != nil {
		return UsbMuxMessage{}, err
	}
	payload := make([]byte, header.Length-16)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return UsbMuxMessage{}, err
	}
	return UsbMuxMessage{Header: header, Payload: payload}, nil
}

func writeTestMuxResponse(conn net.Conn, tag uint32, responseCode uint32) error {
	payload := ToPlistBytes(MuxResponse{MessageType: "Result", Number: responseCode})
	header := UsbMuxHeader{Length: uint32(len(payload)) + 16, Version: 1, Request: 8, Tag: tag}
	if err := binary.Write(conn, binary.LittleEndian, header); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}

type testProtocolError struct {
	message string
}

func (e *testProtocolError) Error() string {
	return e.message
}
