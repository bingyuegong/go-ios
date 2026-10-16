// Package hidkeyboard performs an experimental CoreDevice Universal HID keyboard
// request. Sending a report is not proof that iOS accepted it: on some devices
// an active DisplayService media stream is required to authorize remote input.
package hidkeyboard

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bingyuegong/go-ios/ios"
	"github.com/bingyuegong/go-ios/ios/xpc"
)

const (
	ServiceName              = "com.apple.coredevice.hid.universalhidservice"
	featureName              = "com.apple.coredevice.feature.remote.universalhidservice"
	keyboardServiceID uint64 = 0x100002001
	requestTimeout           = 10 * time.Second
	attachDelay              = 300 * time.Millisecond
	modifierSettle           = 80 * time.Millisecond
	keyHold                  = 80 * time.Millisecond

	keyCommand byte = 0xe3 // Left GUI on HID keyboard usage page 0x07.
	keyA       byte = 0x04
	keyV       byte = 0x19
)

// Named USB HID keyboard usages (page 0x07). Names denote US keyboard key
// positions, not text characters: Shift must be specified explicitly.
var namedKeys = map[string]byte{
	"control": 0xe0, "ctrl": 0xe0, "leftcontrol": 0xe0, "lctrl": 0xe0,
	"shift": 0xe1, "leftshift": 0xe1, "lshift": 0xe1,
	"alt": 0xe2, "option": 0xe2, "leftalt": 0xe2, "lalt": 0xe2, "leftoption": 0xe2,
	"command": keyCommand, "cmd": keyCommand, "meta": keyCommand, "gui": keyCommand, "⌘": keyCommand, "leftcommand": keyCommand, "lcmd": keyCommand,
	"rightcontrol": 0xe4, "rctrl": 0xe4, "rightshift": 0xe5, "rshift": 0xe5,
	"rightalt": 0xe6, "ralt": 0xe6, "rightoption": 0xe6,
	"rightcommand": 0xe7, "rcmd": 0xe7,
	"enter": 0x28, "return": 0x28, "escape": 0x29, "esc": 0x29,
	"backspace": 0x2a, "tab": 0x2b, "space": 0x2c, "spacebar": 0x2c,
	"minus": 0x2d, "-": 0x2d, "equal": 0x2e, "equals": 0x2e, "=": 0x2e,
	"leftbracket": 0x2f, "[": 0x2f, "rightbracket": 0x30, "]": 0x30,
	"backslash": 0x31, "\\": 0x31, "nonusbackslash": 0x32,
	"semicolon": 0x33, ";": 0x33, "quote": 0x34, "apostrophe": 0x34,
	"grave": 0x35, "backtick": 0x35, "comma": 0x36, ",": 0x36,
	"period": 0x37, "dot": 0x37, ".": 0x37, "slash": 0x38, "/": 0x38,
	"capslock": 0x39, "printscreen": 0x46, "scrolllock": 0x47, "pause": 0x48,
	"insert": 0x49, "home": 0x4a, "pageup": 0x4b, "pgup": 0x4b,
	"delete": 0x4c, "forwarddelete": 0x4c, "end": 0x4d,
	"pagedown": 0x4e, "pgdown": 0x4e,
	"right": 0x4f, "rightarrow": 0x4f, "left": 0x50, "leftarrow": 0x50,
	"down": 0x51, "downarrow": 0x51, "up": 0x52, "uparrow": 0x52,
	"numlock": 0x53, "kp/": 0x54, "kp*": 0x55, "kp-": 0x56,
	"kp+": 0x57, "kpenter": 0x58, "kp0": 0x62, "kp.": 0x63,
	"nonuspipe": 0x64, "menu": 0x65, "application": 0x65,
	"power": 0x66, "kp=": 0x67,
}

// ParseKeys accepts one or more keyboard keys pressed simultaneously. A raw
// hid:0xNN usage is useful for other page-0x07 keyboard usages that fit the
// 240-bit CoreDevice report, but cannot represent Apple Fn (page 0xff,
// usage 0x03) or consumer/media keys.
func ParseKeys(names []string) ([]byte, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("at least one keyboard key is required")
	}
	keys := make([]byte, 0, len(names))
	seen := make(map[byte]bool, len(names))
	ordinary := 0
	for _, name := range names {
		normalized := strings.ToLower(strings.TrimSpace(name))
		usage, known := namedKeys[normalized]
		switch {
		case known:
		case len(normalized) == 1 && normalized[0] >= 'a' && normalized[0] <= 'z':
			usage = keyA + normalized[0] - 'a'
		case len(normalized) == 1 && normalized[0] >= '0' && normalized[0] <= '9':
			if normalized == "0" {
				usage = 0x27
			} else {
				usage = 0x1e + normalized[0] - '1'
			}
		case normalized == "fn" || strings.HasPrefix(normalized, "fn:"):
			return nil, fmt.Errorf("Fn/Globe is unsupported by the current keyboard-page HID report (Apple Fn uses vendor page 0xff, usage 0x03); fn:0xNN is not a working Fn substitute")
		case strings.HasPrefix(normalized, "hid:0x"):
			raw, err := strconv.ParseUint(strings.TrimPrefix(normalized, "hid:0x"), 16, 8)
			if err != nil || raw < 4 || raw >= 240 {
				return nil, fmt.Errorf("invalid HID keyboard usage %q (expected hid:0x04 through hid:0xef)", name)
			}
			usage = byte(raw)
		case strings.HasPrefix(normalized, "f"):
			function, err := strconv.Atoi(normalized[1:])
			if err != nil || function < 1 || function > 24 {
				return nil, fmt.Errorf("unsupported key %q; expected f1 through f24", name)
			}
			if function <= 12 {
				usage = byte(0x3a + function - 1)
			} else {
				usage = byte(0x68 + function - 13)
			}
		case strings.HasPrefix(normalized, "kp") && len(normalized) == 3 && normalized[2] >= '1' && normalized[2] <= '9':
			usage = 0x59 + normalized[2] - '1'
		default:
			return nil, fmt.Errorf("unsupported keyboard key %q (use a-z, 0-9, f1-f24, named keys, or hid:0xNN for raw keyboard usages; Fn/Globe and consumer/media keys are not supported)", name)
		}
		if seen[usage] {
			return nil, fmt.Errorf("duplicate keyboard key %q (HID usage 0x%02x)", name, usage)
		}
		seen[usage] = true
		if usage < 0xe0 || usage > 0xe7 {
			ordinary++
			if ordinary > 6 {
				return nil, fmt.Errorf("at most six non-modifier keys can be pressed together")
			}
		}
		keys = append(keys, usage)
	}
	return keys, nil
}

// Descriptor used to register a Generic Desktop keyboard. The Universal HID
// report itself is a 240-bit pressed-usage bitmap, not the descriptor's boot
// keyboard layout, matching the captured CoreDevice virtual keyboard protocol.
var keyboardDescriptor = []byte{
	0x05, 0x01, 0x09, 0x06, 0xa1, 0x01, 0x05, 0x07,
	0x19, 0xe0, 0x29, 0xe7, 0x15, 0x00, 0x25, 0x01,
	0x95, 0x08, 0x75, 0x01, 0x81, 0x02, 0x95, 0x01,
	0x75, 0x08, 0x81, 0x01, 0x05, 0x07, 0x19, 0x00,
	0x29, 0xff, 0x15, 0x00, 0x26, 0xff, 0x00, 0x95,
	0x06, 0x75, 0x08, 0x81, 0x00, 0x05, 0x08,
	0x19, 0x01, 0x29, 0x05, 0x15, 0x00, 0x25, 0x01,
	0x95, 0x05, 0x75, 0x01, 0x91, 0x02, 0x95, 0x01,
	0x75, 0x03, 0x91, 0x01, 0xc0,
}

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

// Session keeps a single virtual keyboard registration and RemoteXPC
// connection alive across multiple key combinations. It is not safe for
// concurrent use. Close releases all keys and closes the connection.
type Session struct {
	connection *Connection
	serviceID  uint64
	closed     bool
}

func New(device ios.DeviceEntry) (*Connection, error) {
	if !device.SupportsRsd() {
		return nil, fmt.Errorf("keyboard requires an iOS 17+ tunnel; start it with ios tunnel start")
	}
	if device.Rsd.GetPort(ServiceName) == 0 {
		return nil, fmt.Errorf("%s is unavailable in RSD; check ios rsd ls", ServiceName)
	}
	conn, err := ios.ConnectToXpcServiceTunnelIface(device, ServiceName)
	if err != nil {
		return nil, fmt.Errorf("connect to Universal HID service: %w", err)
	}
	return &Connection{conn: conn, timeout: requestTimeout}, nil
}

func (c *Connection) Close() error { return c.conn.Close() }

// PressKeys registers a virtual keyboard for a single key combination. The
// send calls do not acknowledge delivery to the foreground app; a display
// media stream may be required on some devices.
func (c *Connection) PressKeys(keys []byte) error {
	modifiers, ordinary, err := splitKeys(keys)
	if err != nil {
		return err
	}
	serviceID, err := c.createKeyboard()
	if err != nil {
		return fmt.Errorf("create virtual keyboard: %w", err)
	}
	// The device can list the keyboard before it has attached the service.
	time.Sleep(attachDelay)
	return c.pressKeys(serviceID, modifiers, ordinary)
}

// StartSession registers one virtual keyboard and keeps it attached until
// Session.Close. The caller must close the Connection if this call fails.
func (c *Connection) StartSession() (*Session, error) {
	serviceID, err := c.createKeyboard()
	if err != nil {
		return nil, fmt.Errorf("create virtual keyboard: %w", err)
	}
	time.Sleep(attachDelay)
	return &Session{connection: c, serviceID: serviceID}, nil
}

// PressKeys sends a key combination on the existing virtual keyboard. It
// releases all keys after every attempt, including failed sends.
func (s *Session) PressKeys(keys []byte) error {
	if s.closed {
		return fmt.Errorf("virtual keyboard session is closed")
	}
	modifiers, ordinary, err := splitKeys(keys)
	if err != nil {
		return err
	}
	return s.connection.pressKeys(s.serviceID, modifiers, ordinary)
}

// Close best-effort releases keys and closes the RemoteXPC connection. An
// abnormal process termination cannot guarantee cleanup on the device.
func (s *Session) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	releaseErr := s.connection.sendReport(s.serviceID, nil)
	if releaseErr != nil {
		releaseErr = fmt.Errorf("release all keyboard keys: %w", releaseErr)
	}
	return errors.Join(releaseErr, s.connection.Close())
}

func splitKeys(keys []byte) (modifiers, ordinary []byte, err error) {
	if len(keys) == 0 {
		return nil, nil, fmt.Errorf("at least one keyboard key is required")
	}
	seen := make(map[byte]bool, len(keys))
	for _, key := range keys {
		if key < 4 || key >= 240 {
			return nil, nil, fmt.Errorf("HID keyboard usage 0x%02x is outside the supported report range", key)
		}
		if seen[key] {
			return nil, nil, fmt.Errorf("duplicate HID keyboard usage 0x%02x", key)
		}
		seen[key] = true
		if key >= 0xe0 && key <= 0xe7 {
			modifiers = append(modifiers, key)
		} else {
			ordinary = append(ordinary, key)
		}
	}
	if len(ordinary) > 6 {
		return nil, nil, fmt.Errorf("at most six non-modifier keys can be pressed together")
	}
	return modifiers, ordinary, nil
}

func (c *Connection) pressKeys(serviceID uint64, modifiers, ordinary []byte) (err error) {
	defer func() {
		if releaseErr := c.sendReport(serviceID, nil); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("release all keyboard keys: %w", releaseErr))
		}
	}()

	// A modifier introduced in the same report as a letter can be ignored by
	// the device. Settle each modifier before introducing subsequent keys.
	for i := range modifiers {
		if err = c.sendReport(serviceID, modifiers[:i+1]); err != nil {
			return fmt.Errorf("press modifier 0x%02x: %w", modifiers[i], err)
		}
		if i+1 < len(modifiers) || len(ordinary) != 0 {
			time.Sleep(modifierSettle)
		}
	}
	if len(ordinary) == 0 {
		time.Sleep(keyHold)
		return nil
	}
	all := append(append([]byte(nil), modifiers...), ordinary...)
	if err = c.sendReport(serviceID, all); err != nil {
		return fmt.Errorf("press keys: %w", err)
	}
	time.Sleep(keyHold)
	if len(modifiers) != 0 {
		if err = c.sendReport(serviceID, modifiers); err != nil {
			return fmt.Errorf("release ordinary keys: %w", err)
		}
	}
	return nil
}

// PressCommandLetter preserves the original Command+A/V API.
func (c *Connection) PressCommandLetter(letter byte) error {
	if letter != 'a' && letter != 'v' {
		return fmt.Errorf("only Command+A and Command+V are supported")
	}
	return c.PressKeys([]byte{keyCommand, keyA + letter - 'a'})
}

func (c *Connection) createKeyboard() (uint64, error) {
	const manufacturer = "go-ios"
	const product = "go-ios virtual keyboard"
	usage, page := int64(6), int64(1)
	vendor, productID := int64(0x05ac), int64(0x0250)
	id := keyboardServiceID
	pair := map[string]interface{}{"DeviceUsage": usage, "DeviceUsagePage": page}
	storage := map[string]interface{}{
		"Manufacturer":     map[string]interface{}{"string": manufacturer},
		"Product":          map[string]interface{}{"string": product},
		"ProductID":        map[string]interface{}{"int": productID},
		"VendorID":         map[string]interface{}{"int": vendor},
		"PrimaryUsage":     map[string]interface{}{"int": usage},
		"PrimaryUsagePage": map[string]interface{}{"int": page},
		"DeviceUsagePairs": map[string]interface{}{"array": []interface{}{
			map[string]interface{}{"dictionary": map[string]interface{}{
				"DeviceUsage":     map[string]interface{}{"int": usage},
				"DeviceUsagePage": map[string]interface{}{"int": page},
			}},
		}},
		"Transport":                      map[string]interface{}{"string": "USB"},
		"ReportDescriptor":               map[string]interface{}{"data": keyboardDescriptor},
		"UniversalControlVirtualService": map[string]interface{}{"bool": true},
		"_ServiceID":                     map[string]interface{}{"uint": id},
	}
	request := map[string]interface{}{
		"featureIdentifier": featureName,
		"messageType":       "Request",
		"payload": map[string]interface{}{"createService": map[string]interface{}{
			"_0": map[string]interface{}{
				"DeviceUsagePairs":                   []interface{}{pair},
				"PrimaryUsage":                       uint64(6),
				"PrimaryUsagePage":                   uint64(1),
				"Product":                            product,
				"ProductID":                          productID,
				"VendorID":                           vendor,
				"_CoreDevice_codablePropertyStorage": storage,
				"_ServiceID":                         id,
			},
		}},
	}
	response, err := c.sendReceive(request)
	if err != nil {
		return 0, err
	}
	if value, ok := response["serviceID"]; ok {
		switch v := value.(type) {
		case uint64:
			if v != 0 {
				id = v
			}
		case int64:
			if v > 0 {
				id = uint64(v)
			}
		}
	}
	return id, nil
}

// buildReport matches the 39-byte Universal HID keyboard report: report ID,
// 240-bit pressed-usage bitmap, six-byte LE timestamp, and two reserved bytes.
func buildReport(keys []byte, timestamp uint64) []byte {
	report := make([]byte, 39)
	report[0] = 1
	for _, key := range keys {
		if key < 240 {
			report[1+int(key)/8] |= 1 << (key % 8)
		}
	}
	for i := 0; i < 6; i++ {
		report[31+i] = byte(timestamp >> (8 * i))
	}
	return report
}

func (c *Connection) sendReport(id uint64, keys []byte) error {
	timestamp, err := monotonicTimestamp()
	if err != nil {
		return fmt.Errorf("read host monotonic clock: %w", err)
	}
	report := buildReport(keys, timestamp)
	request := map[string]interface{}{
		"featureIdentifier": featureName,
		"messageType":       "Request",
		"payload": map[string]interface{}{"send": map[string]interface{}{
			"_0": report,
			"_1": id,
		}},
	}
	// The reference client sends HID reports without requesting a reply.
	return c.conn.Send(request)
}

func (c *Connection) sendReceive(request map[string]interface{}) (map[string]interface{}, error) {
	if err := c.conn.Send(request, xpc.HeartbeatRequestFlag); err != nil {
		return nil, fmt.Errorf("send Universal HID request: %w", err)
	}
	type result struct {
		reply map[string]interface{}
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		for {
			reply, err := c.conn.ReceiveOnServerClientStream()
			if err != nil || len(reply) != 0 {
				ch <- result{reply: reply, err: err}
				return
			}
		}
	}()
	timer := time.NewTimer(c.timeout)
	defer timer.Stop()
	select {
	case got := <-ch:
		if got.err != nil {
			return nil, fmt.Errorf("read Universal HID response: %w", got.err)
		}
		if value, exists := got.reply["error"]; exists && value != nil {
			return nil, fmt.Errorf("Universal HID service error: %v", value)
		}
		if got.reply["messageType"] == "Error" {
			return nil, fmt.Errorf("Universal HID service rejected request: %v", got.reply)
		}
		return got.reply, nil
	case <-timer.C:
		_ = c.conn.Close()
		return nil, fmt.Errorf("timed out after %s waiting for Universal HID service; connection closed", c.timeout)
	}
}
