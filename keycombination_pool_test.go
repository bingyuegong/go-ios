package main

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bingyuegong/go-ios/ios"
	"github.com/docopt/docopt-go"
)

type fakeKeyboardSession struct {
	pressed [][]byte
	err     error
	closed  bool
}

func (f *fakeKeyboardSession) PressKeys(keys []byte) error {
	f.pressed = append(f.pressed, append([]byte(nil), keys...))
	return f.err
}
func (f *fakeKeyboardSession) Close() error { f.closed = true; return nil }

type fakeRsd struct{ port int }

func (f fakeRsd) GetPort(service string) int                  { return f.port }
func (f fakeRsd) GetService(int) string                       { return "" }
func (f fakeRsd) GetServices() map[string]ios.RsdServiceEntry { return nil }

func deviceWithSerial(serial string, port int) ios.DeviceEntry {
	if port <= 0 {
		return ios.DeviceEntry{Properties: ios.DeviceProperties{SerialNumber: serial}}
	}
	return ios.DeviceEntry{
		Properties: ios.DeviceProperties{SerialNumber: serial},
		Rsd:        fakeRsd{port: port},
	}
}

func TestKeyboardPoolStartsOneSessionPerDevice(t *testing.T) {
	sessions := make(map[string]*fakeKeyboardSession)
	pool := newKeyboardPool([]ios.DeviceEntry{
		deviceWithSerial("udid-a", 100),
		deviceWithSerial("udid-b", 100),
		deviceWithSerial("no-tunnel", 0),
	}, func(device ios.DeviceEntry) (keyboardSession, error) {
		session := &fakeKeyboardSession{}
		sessions[device.Properties.SerialNumber] = session
		return session, nil
	})
	if !reflect.DeepEqual(pool.UDIDs(), []string{"udid-a", "udid-b"}) {
		t.Fatalf("sessions = %v", pool.UDIDs())
	}
	if pool.PressKeys("udid-a", []byte{0xe3, 0x04})[0].Error != "" {
		t.Fatal("press to a known device failed")
	}
	if got := pool.PressKeys("", []byte{0x04}); len(got) != 2 || got[0].UDID != "udid-a" || got[1].UDID != "udid-b" {
		t.Fatalf("broadcast results = %+v", got)
	}
	// udid-a received the targeted press and the broadcast; udid-b only the
	// broadcast.
	if len(sessions["udid-a"].pressed) != 2 || len(sessions["udid-b"].pressed) != 1 {
		t.Fatalf("presses: a=%d b=%d", len(sessions["udid-a"].pressed), len(sessions["udid-b"].pressed))
	}
	if pool.PressKeys("missing", []byte{0x04})[0].Error == "" {
		t.Fatal("missing device reported success")
	}
}

func TestKeyboardPoolDropsRemovedDevicesAndRetriesWithBackoff(t *testing.T) {
	var closed *fakeKeyboardSession
	attempts := 0
	pool := newKeyboardPool([]ios.DeviceEntry{deviceWithSerial("udid-a", 100)}, func(ios.DeviceEntry) (keyboardSession, error) {
		attempts++
		closed = &fakeKeyboardSession{}
		return closed, nil
	})
	pool.Sync(nil)
	if !closed.closed || len(pool.UDIDs()) != 0 {
		t.Fatalf("removed device session kept: %v", pool.UDIDs())
	}
	// A device whose session cannot be created must not be retried on every
	// poll, but must stay pending for a later retry.
	failing := true
	pool.failed = map[string]poolFailure{}
	attempts = 0
	pool.dialer = func(ios.DeviceEntry) (keyboardSession, error) {
		attempts++
		if failing {
			return nil, errors.New("service unavailable")
		}
		return &fakeKeyboardSession{}, nil
	}
	pool.Sync([]ios.DeviceEntry{deviceWithSerial("udid-b", 100)})
	pool.Sync([]ios.DeviceEntry{deviceWithSerial("udid-b", 100)})
	if attempts != 1 {
		t.Fatalf("dial attempts = %d, want 1 while backing off", attempts)
	}
	if !reflect.DeepEqual(pool.Pending(), []string{"udid-b"}) {
		t.Fatalf("pending = %v", pool.Pending())
	}
	// Once the device becomes reachable, a session is created and the pending
	// entry is cleared.
	failing = false
	pool.failed = map[string]poolFailure{}
	pool.Sync([]ios.DeviceEntry{deviceWithSerial("udid-b", 100)})
	if !reflect.DeepEqual(pool.UDIDs(), []string{"udid-b"}) || len(pool.Pending()) != 0 {
		t.Fatalf("recovered device: sessions=%v pending=%v", pool.UDIDs(), pool.Pending())
	}
}

func TestKeyboardPoolClosesBrokenSessionOnPressFailure(t *testing.T) {
	session := &fakeKeyboardSession{err: errors.New("wire failed")}
	pool := &keyboardPool{sessions: map[string]keyboardSession{"udid-a": session}, failed: map[string]poolFailure{}}
	result := pool.PressKeys("", []byte{0x04})
	if len(result) != 1 || result[0].Error != "wire failed" {
		t.Fatalf("results = %+v", result)
	}
	if !session.closed || len(pool.UDIDs()) != 0 {
		t.Fatal("broken session was kept")
	}
}

func TestKeyboardPoolClosesBrokenTargetedSession(t *testing.T) {
	session := &fakeKeyboardSession{err: errors.New("wire failed")}
	pool := &keyboardPool{sessions: map[string]keyboardSession{"udid-a": session}, failed: map[string]poolFailure{}}
	result := pool.PressKeys("udid-a", []byte{0x04})
	if len(result) != 1 || result[0].Error != "wire failed" || !session.closed || len(pool.UDIDs()) != 0 {
		t.Fatalf("targeted failure left broken session: result=%+v closed=%t", result, session.closed)
	}
}

func TestKeyboardPoolConcurrentRequestsAndSync(t *testing.T) {
	device := deviceWithSerial("udid-a", 100)
	pool := newKeyboardPool([]ios.DeviceEntry{device}, func(ios.DeviceEntry) (keyboardSession, error) {
		return &fakeKeyboardSession{}, nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				pool.Sync([]ios.DeviceEntry{device})
				pool.PressKeys("udid-a", []byte{0x04})
				pool.UDIDs()
				pool.Pending()
			}
		}()
	}
	wg.Wait()
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestKeyboardPoolSurvivesPanicFromPress(t *testing.T) {
	pool := &keyboardPool{
		sessions: map[string]keyboardSession{"udid-a": panickingSession{}},
		failed:   map[string]poolFailure{},
	}
	result := pool.PressKeys("udid-a", []byte{0x04})
	if result[0].Error == "" {
		t.Fatal("panic was not converted to an error")
	}
}

type panickingSession struct{}

func (panickingSession) PressKeys([]byte) error { panic("boom") }
func (panickingSession) Close() error           { return nil }

func TestKeyboardDaemonRequestHandling(t *testing.T) {
	pool := &keyboardPool{
		sessions: map[string]keyboardSession{"udid-a": &fakeKeyboardSession{}},
		failed:   map[string]poolFailure{},
	}
	if response := handleKeyboardDaemonRequest(pool, daemonRequest{Command: "keys", UDID: "udid-a", Keys: []string{"command", "a"}}); !response.OK || len(response.Results) != 1 {
		t.Fatalf("keys response = %+v", response)
	}
	for _, keys := range [][]string{{"fn", "b"}, {"fn:0x65", "b"}} {
		if response := handleKeyboardDaemonRequest(pool, daemonRequest{Command: "keys", Keys: keys}); response.OK || response.Message == "" {
			t.Fatalf("unsupported Fn reported success for %v: %+v", keys, response)
		}
	}
	if response := handleKeyboardDaemonRequest(pool, daemonRequest{Command: "keys", Keys: []string{"nope"}}); response.OK {
		t.Fatal("invalid keys accepted")
	}
	if response := handleKeyboardDaemonRequest(pool, daemonRequest{Command: "status"}); !response.OK || len(response.UDIDs) != 1 {
		t.Fatalf("status response = %+v", response)
	}
	if response := handleKeyboardDaemonRequest(pool, daemonRequest{Command: "other"}); response.OK {
		t.Fatal("unknown command accepted")
	}
	pool.Sync(nil)
	if response := handleKeyboardDaemonRequest(pool, daemonRequest{Command: "keys", Keys: []string{"a"}}); response.OK || response.Message == "" {
		t.Fatalf("empty pool reported successful delivery: %+v", response)
	}
}

func TestSendKeysToDaemonRequiresManagedDeviceDaemon(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "daemon.json")
	t.Setenv("GO_IOS_KEYBOARD_STATE", statePath)

	pool := &keyboardPool{
		sessions: map[string]keyboardSession{"udid-a": &fakeKeyboardSession{}},
		failed:   map[string]poolFailure{},
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go serveKeyboardDaemon(listener, pool)
	if err := writeDaemonState(statePath, listener.Addr().String()); err != nil {
		t.Fatal(err)
	}

	if _, err := sendKeysToDaemon("udid-a", []string{"command", "v"}); err == nil || !strings.Contains(err.Error(), "daemon start -u udid-a") {
		t.Fatalf("missing per-device daemon was not reported: %v", err)
	}
	if _, err := sendKeysToDaemon("", []string{"a"}); err == nil {
		t.Fatal("missing target UDID accepted")
	}
	if got := len(pool.sessions["udid-a"].(*fakeKeyboardSession).pressed); got != 0 {
		t.Fatalf("foreground daemon received %d unexpected presses", got)
	}
}

func TestManagedKeyboardDaemonStartStopAndPress(t *testing.T) {
	t.Setenv("GO_IOS_KEYBOARD_STATE", filepath.Join(t.TempDir(), "keyboard.json"))
	const udid = "00008110-000E2C8C3AF1401E"
	path := deviceDaemonStatePath(udid)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	state := daemonState{Address: listener.Addr().String(), PID: os.Getpid(), UDID: udid, Token: "test-token"}
	pool := newKeyboardPool([]ios.DeviceEntry{deviceWithSerial(udid, 100)}, func(ios.DeviceEntry) (keyboardSession, error) {
		return &fakeKeyboardSession{}, nil
	})
	defer func() { _ = pool.Close() }()
	stopped := make(chan struct{})
	go serveKeyboardDaemonWithStop(listener, pool, udid, state.Token, func() {
		removeDaemonStateIfOwned(path, state)
		close(stopped)
	})
	if err := writeDaemonStateRecord(path, state); err != nil {
		t.Fatal(err)
	}
	if err := startDeviceKeyboardDaemon(udid, docopt.Opts{}); err != nil {
		t.Fatalf("duplicate start: %v", err)
	}
	for _, keys := range [][]string{{"a"}, {"7"}, {"command", "a"}} {
		response, err := sendKeysToDaemon(udid, keys)
		if err != nil || !response.OK || len(response.Results) != 1 || response.Results[0].UDID != udid {
			t.Fatalf("device press %v: %+v, %v", keys, response, err)
		}
	}
	session := pool.sessions[udid].(*fakeKeyboardSession)
	if want := [][]byte{{0x04}, {0x24}, {0xe3, 0x04}}; !reflect.DeepEqual(session.pressed, want) {
		t.Fatalf("pressed = %x, want %x", session.pressed, want)
	}
	if _, err := sendKeysToDaemon("another-device", []string{"a"}); err == nil {
		t.Fatal("missing device daemon unexpectedly accepted input")
	}
	for _, command := range []string{"stop", "force-stop"} {
		if response, err := requestKeyboardDaemon(state, daemonRequest{Command: command, UDID: udid, Token: "wrong"}); err != nil || response.OK {
			t.Fatalf("unauthorized %s: %+v, %v", command, response, err)
		}
	}
	if response, err := requestKeyboardDaemon(state, daemonRequest{Command: "identity"}); err != nil || !response.OK || response.PID != os.Getpid() || response.Target != udid {
		t.Fatalf("identity: %+v, %v", response, err)
	}
	if err := stopDeviceKeyboardDaemon(udid); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("daemon was not asked to stop")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("state was not removed: %v", err)
	}
	if err := stopDeviceKeyboardDaemon(udid); err != nil {
		t.Fatalf("repeated stop: %v", err)
	}
}

func TestManagedKeyboardDaemonRejectsInvalidPort(t *testing.T) {
	t.Setenv("GO_IOS_KEYBOARD_STATE", filepath.Join(t.TempDir(), "keyboard.json"))
	const udid = "00008110-000E2C8C3AF1401E"
	if err := startDeviceKeyboardDaemon(udid, docopt.Opts{"--tunnel-info-port": "not-a-port"}); err == nil {
		t.Fatal("invalid port was silently ignored")
	}
	if _, err := os.Stat(deviceDaemonStatePath(udid)); !os.IsNotExist(err) {
		t.Fatalf("invalid port spawned a daemon: %v", err)
	}
}

func TestManagedKeyboardDaemonRefusesUnverifiedPID(t *testing.T) {
	t.Setenv("GO_IOS_KEYBOARD_STATE", filepath.Join(t.TempDir(), "keyboard.json"))
	const udid = "00008110-000E2C8C3AF1401E"
	state := daemonState{Address: "127.0.0.1:1", PID: os.Getpid(), UDID: udid, Token: "test-token"}
	path := deviceDaemonStatePath(udid)
	if err := writeDaemonStateRecord(path, state); err != nil {
		t.Fatal(err)
	}
	if err := stopDeviceKeyboardDaemon(udid); err == nil {
		t.Fatal("unverified process was stopped")
	}
	if err := startDeviceKeyboardDaemon(udid, docopt.Opts{}); err == nil {
		t.Fatal("duplicate process was spawned for unverified live PID")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("live process state was removed: %v", err)
	}
}

func TestDeviceDaemonLockSerializesActions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyboard.json")
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- withDeviceDaemonLock(path, func() error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	select {
	case <-firstEntered:
	case err := <-firstDone:
		t.Fatalf("first lock failed: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("first lock was not acquired")
	}

	secondAttempting := make(chan struct{})
	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondAttempting)
		secondDone <- withDeviceDaemonLock(path, func() error {
			close(secondEntered)
			return nil
		})
	}()
	<-secondAttempting
	overlapped := false
	select {
	case <-secondEntered:
		overlapped = true
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	for _, done := range []<-chan error{firstDone, secondDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("locking failed: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("lock was not released")
		}
	}
	if overlapped {
		t.Fatal("concurrent daemon action entered while the lock was held")
	}
}

func TestProcessExistsCurrentProcess(t *testing.T) {
	if !processExists(os.Getpid()) {
		t.Fatal("current process was reported as absent")
	}
	if processExists(0) {
		t.Fatal("PID zero was reported as a process")
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	if got := backoffFor(1); got != 5*time.Second {
		t.Fatalf("backoff(1) = %s", got)
	}
	if got := backoffFor(100); got != 30*time.Second {
		t.Fatalf("backoff(100) = %s", got)
	}
}

func TestDaemonStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := writeDaemonState(path, "127.0.0.1:1234"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state daemonState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Address != "127.0.0.1:1234" {
		t.Fatalf("address = %q", state.Address)
	}
}
