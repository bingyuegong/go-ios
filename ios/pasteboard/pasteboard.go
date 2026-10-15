// Package pasteboard reads and writes the device pasteboard on iOS 17+ via
// the com.apple.coredevice.pasteboardservice RemoteXPC service.
package pasteboard

import (
	"fmt"
	"time"

	"github.com/bingyuegong/go-ios/ios"
	"github.com/bingyuegong/go-ios/ios/xpc"
)

const (
	// ServiceName is advertised by the mounted developer disk image in RSD.
	ServiceName       = "com.apple.coredevice.pasteboardservice"
	GeneralPasteboard = "general"
	requestTimeout    = 10 * time.Second
	commandPull       = "PULL"
	commandSet        = "SET"
	UTIUTF8PlainText  = "public.utf8-plain-text"
	UTIPlainText      = "public.plain-text"
	UTIText           = "public.text"
)

var textUTIs = []string{UTIUTF8PlainText, UTIPlainText, UTIText}

// xpcConn allows the protocol to be tested without connecting to a device.
type xpcConn interface {
	Send(map[string]interface{}, ...uint32) error
	ReceiveOnServerClientStream() (map[string]interface{}, error)
	Close() error
}

// Connection is not safe for concurrent use.
type Connection struct {
	conn    xpcConn
	timeout time.Duration
}

// New connects through the device's tunnel and requires RSD to advertise the
// pasteboard service. An older mounted developer disk image may lack it.
func New(device ios.DeviceEntry) (*Connection, error) {
	if !device.SupportsRsd() {
		return nil, fmt.Errorf("pasteboard requires an iOS 17+ tunnel; start it with ios tunnel start")
	}
	if device.Rsd.GetPort(ServiceName) == 0 {
		return nil, fmt.Errorf("%s is unavailable in RSD; mount a recent developer disk image and check ios rsd ls", ServiceName)
	}
	conn, err := ios.ConnectToXpcServiceTunnelIface(device, ServiceName)
	if err != nil {
		return nil, fmt.Errorf("connect to pasteboard service: %w", err)
	}
	return &Connection{conn: conn, timeout: requestTimeout}, nil
}

func (c *Connection) Close() error { return c.conn.Close() }

// SetText replaces the general pasteboard with UTF-8 text in standard UTIs.
func (c *Connection) SetText(text string) error {
	data := map[string]interface{}{}
	types := make([]interface{}, len(textUTIs))
	for i, uti := range textUTIs {
		types[i] = uti
		data[uti] = map[string]interface{}{"data": []byte(text)}
	}
	request := map[string]interface{}{
		"command":        commandSet,
		"pasteboardName": GeneralPasteboard,
		"items": []interface{}{map[string]interface{}{
			"types": types,
			"data":  data,
		}},
		"sourceMetadata": nil,
	}
	_, err := c.sendReceive(request)
	if err != nil {
		return fmt.Errorf("set pasteboard: %w", err)
	}
	return nil
}

// GetText returns the first inline text item. An empty or promised-only
// pasteboard has no decodable text, indicated by the second return value.
func (c *Connection) GetText() (string, bool, error) {
	request := map[string]interface{}{
		"command":        commandPull,
		"pasteboardName": GeneralPasteboard,
		"dataPolicy":     map[string]interface{}{"allResolved": map[string]interface{}{}},
	}
	reply, err := c.sendReceive(request)
	if err != nil {
		return "", false, fmt.Errorf("get pasteboard: %w", err)
	}
	text, ok := snapshotText(reply)
	return text, ok, nil
}

// sendReceive skips empty control frames so a later request cannot consume a
// stale reply. The connection is closed on timeout to unblock the reader.
func (c *Connection) sendReceive(request map[string]interface{}) (map[string]interface{}, error) {
	if err := c.conn.Send(request, xpc.HeartbeatRequestFlag); err != nil {
		return nil, fmt.Errorf("send pasteboard request: %w", err)
	}
	type result struct {
		reply map[string]interface{}
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		for {
			reply, err := c.conn.ReceiveOnServerClientStream()
			if err != nil {
				ch <- result{err: err}
				return
			}
			if len(reply) != 0 {
				ch <- result{reply: reply}
				return
			}
		}
	}()

	timer := time.NewTimer(c.timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("read pasteboard response: %w", r.err)
		}
		if err := serviceError(r.reply); err != nil {
			return nil, err
		}
		return r.reply, nil
	case <-timer.C:
		_ = c.conn.Close()
		return nil, fmt.Errorf("timed out after %s waiting for pasteboard service; connection closed", c.timeout)
	}
}

func serviceError(reply map[string]interface{}) error {
	errDict, ok := reply["error"].(map[string]interface{})
	if !ok {
		return nil
	}
	domain, _ := errDict["domain"].(string)
	return fmt.Errorf("pasteboard service error (domain %s, code %v)", domain, errDict["code"])
}

func snapshotText(reply map[string]interface{}) (string, bool) {
	if pb, ok := reply["pasteboard"].(map[string]interface{}); ok {
		reply = pb
	}
	items, ok := reply["items"].([]interface{})
	if !ok {
		return "", false
	}
	for _, raw := range items {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		data, ok := item["data"].(map[string]interface{})
		if !ok {
			continue
		}
		for _, uti := range textUTIs {
			entry, ok := data[uti].(map[string]interface{})
			if !ok {
				continue
			}
			if bytes, ok := entry["data"].([]byte); ok && len(bytes) > 0 {
				return string(bytes), true
			}
		}
	}
	return "", false
}
