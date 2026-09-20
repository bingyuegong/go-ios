package ios

import (
	"context"
	"fmt"
	"net"
	"time"
)

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

// DialDevicePortContext opens a TCP connection to a device port through usbmuxd.
// The returned connection carries raw device-port traffic; callers own it and
// must close it. This is the same data path used by the forward command, without
// creating a host-side listening port.
func DialDevicePortContext(ctx context.Context, device DeviceEntry, port uint16) (net.Conn, error) {
	dialer := &net.Dialer{}
	return dialDevicePortContext(ctx, device.DeviceID, port, GetUsbmuxdSocket(), dialer.DialContext)
}

func dialDevicePortContext(ctx context.Context, deviceID int, port uint16, socketAddress string, dial dialContextFunc) (net.Conn, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if deviceID <= 0 {
		return nil, fmt.Errorf("valid usbmux device ID is required")
	}
	if port == 0 {
		return nil, fmt.Errorf("device port is required")
	}

	network, address := GetSocketTypeAndAddress(socketAddress)
	rawConn, err := dial(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("connect to usbmuxd at %s: %w", socketAddress, err)
	}

	cancelDeadline := context.AfterFunc(ctx, func() {
		_ = rawConn.SetDeadline(time.Now())
	})

	muxConn := NewUsbMuxConnection(NewDeviceConnectionWithConn(rawConn))
	if err := muxConn.Connect(deviceID, port); err != nil {
		cancelDeadline()
		_ = muxConn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("connect device %d port %d through usbmuxd: %w", deviceID, port, ctxErr)
		}
		return nil, fmt.Errorf("connect device %d port %d through usbmuxd: %w", deviceID, port, err)
	}
	if !cancelDeadline() {
		_ = muxConn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("connect device %d port %d through usbmuxd: %w", deviceID, port, ctxErr)
		}
		return nil, fmt.Errorf("connect device %d port %d through usbmuxd: context canceled", deviceID, port)
	}
	if err := rawConn.SetDeadline(time.Time{}); err != nil {
		_ = muxConn.Close()
		return nil, fmt.Errorf("clear usbmux handshake deadline: %w", err)
	}

	deviceConn := muxConn.ReleaseDeviceConnection()
	if deviceConn == nil || deviceConn.Conn() == nil {
		if deviceConn != nil {
			_ = deviceConn.Close()
		}
		return nil, fmt.Errorf("usbmuxd returned an unusable device connection")
	}
	return deviceConn.Conn(), nil
}
