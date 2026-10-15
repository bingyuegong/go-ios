package pasteboard

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bingyuegong/go-ios/ios"
)

type frame struct {
	body map[string]interface{}
	err  error
}

type fakeConn struct {
	mu        sync.Mutex
	frames    []frame
	index     int
	sent      []map[string]interface{}
	closed    chan struct{}
	closeOnce sync.Once
}

func newFakeConn(frames ...frame) *fakeConn {
	return &fakeConn{frames: frames, closed: make(chan struct{})}
}

func (f *fakeConn) Send(request map[string]interface{}, _ ...uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, request)
	return nil
}

func (f *fakeConn) ReceiveOnServerClientStream() (map[string]interface{}, error) {
	f.mu.Lock()
	if f.index < len(f.frames) {
		r := f.frames[f.index]
		f.index++
		f.mu.Unlock()
		return r.body, r.err
	}
	f.mu.Unlock()
	<-f.closed
	return nil, errors.New("closed")
}

func (f *fakeConn) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

func TestNewRequiresTunnelAndAdvertisedService(t *testing.T) {
	if _, err := New(ios.DeviceEntry{}); err == nil || !strings.Contains(err.Error(), "tunnel") {
		t.Fatalf("expected missing tunnel error, got %v", err)
	}
	device := ios.DeviceEntry{Rsd: ios.RsdHandshakeResponse{Services: map[string]ios.RsdServiceEntry{}}}
	if _, err := New(device); err == nil || !strings.Contains(err.Error(), ServiceName) {
		t.Fatalf("expected missing RSD service error, got %v", err)
	}
}

func TestSetTextEncodesUnicodeAndEmptyString(t *testing.T) {
	for _, text := range []string{"测试 🌍", ""} {
		t.Run(text, func(t *testing.T) {
			f := newFakeConn(frame{body: map[string]interface{}{"command": "SET_REPLY"}})
			c := &Connection{conn: f, timeout: time.Second}
			if err := c.SetText(text); err != nil {
				t.Fatal(err)
			}
			if len(f.sent) != 1 || f.sent[0]["command"] != commandSet || f.sent[0]["pasteboardName"] != GeneralPasteboard {
				t.Fatalf("unexpected SET request shape")
			}
			items, ok := f.sent[0]["items"].([]interface{})
			if !ok || len(items) != 1 {
				t.Fatalf("unexpected items shape")
			}
			item := items[0].(map[string]interface{})
			data := item["data"].(map[string]interface{})
			for _, uti := range textUTIs {
				entry := data[uti].(map[string]interface{})
				if got := string(entry["data"].([]byte)); got != text {
					t.Fatalf("UTI %s: got %q", uti, got)
				}
			}
		})
	}
}

func TestGetTextSkipsEmptyFramesAndExtractsText(t *testing.T) {
	f := newFakeConn(
		frame{body: nil},
		frame{body: map[string]interface{}{}},
		frame{body: map[string]interface{}{"pasteboard": map[string]interface{}{
			"items": []interface{}{map[string]interface{}{
				"data": map[string]interface{}{UTIUTF8PlainText: map[string]interface{}{"data": []byte("你好")}},
			}},
		}}},
	)
	c := &Connection{conn: f, timeout: time.Second}
	got, ok, err := c.GetText()
	if err != nil || !ok || got != "你好" {
		t.Fatalf("GetText = %q, %t, %v", got, ok, err)
	}
	if len(f.sent) != 1 || f.sent[0]["command"] != commandPull {
		t.Fatalf("unexpected PULL request")
	}
}

func TestGetTextEmptyOrPromised(t *testing.T) {
	for _, datum := range []map[string]interface{}{
		{"data": []byte{}},
		{"isPromised": true},
	} {
		items := []interface{}{map[string]interface{}{"data": map[string]interface{}{UTIUTF8PlainText: datum}}}
		if text, ok := snapshotText(map[string]interface{}{"items": items}); ok || text != "" {
			t.Fatalf("snapshotText = %q, %t; want no inline text", text, ok)
		}
	}
}

func TestServiceErrorAndReadError(t *testing.T) {
	f := newFakeConn(frame{body: map[string]interface{}{"error": map[string]interface{}{
		"domain": "NSCocoaErrorDomain", "code": int64(4864),
	}}})
	c := &Connection{conn: f, timeout: time.Second}
	if err := c.SetText("x"); err == nil || !strings.Contains(err.Error(), "4864") {
		t.Fatalf("expected service error, got %v", err)
	}
	want := errors.New("stream closed")
	c = &Connection{conn: newFakeConn(frame{err: want}), timeout: time.Second}
	if _, _, err := c.GetText(); !errors.Is(err, want) {
		t.Fatalf("expected wrapped read error, got %v", err)
	}
}

func TestTimeoutClosesConnection(t *testing.T) {
	f := newFakeConn()
	c := &Connection{conn: f, timeout: 20 * time.Millisecond}
	if _, _, err := c.GetText(); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
	select {
	case <-f.closed:
	case <-time.After(time.Second):
		t.Fatal("timeout did not close connection")
	}
}
