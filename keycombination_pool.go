package main

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/bingyuegong/go-ios/ios"
	"github.com/bingyuegong/go-ios/ios/hidkeyboard"
)

// keyboardSession is the part of hidkeyboard.Session a pool needs. It allows
// tests to substitute a fake session without a real device.
type keyboardSession interface {
	PressKeys([]byte) error
	Close() error
}

// keyboardPool keeps one long-lived virtual keyboard session per device. Each
// session is created once and reused, because an attached virtual keyboard is
// what keeps the on-screen keyboard from reappearing on the device.
type keyboardPool struct {
	mu       sync.Mutex // Sessions are not safe for concurrent PressKeys/Close calls.
	sessions map[string]keyboardSession
	failed   map[string]poolFailure
	dialer   func(ios.DeviceEntry) (keyboardSession, error)
}

type poolFailure struct {
	attempts int
	retryAt  time.Time
}

// newKeyboardPool starts sessions for the devices handed in. Devices without a
// tunnel or without the Universal HID service are skipped, not fatal, so
// unrelated connected devices do not stop the others.
func newKeyboardPool(devices []ios.DeviceEntry, dialer func(ios.DeviceEntry) (keyboardSession, error)) *keyboardPool {
	pool := &keyboardPool{
		sessions: make(map[string]keyboardSession),
		failed:   make(map[string]poolFailure),
		dialer:   dialer,
	}
	pool.Sync(devices)
	return pool
}

// Sync opens sessions for newly eligible devices and drops sessions for
// devices that disappeared. A failed device is retried with backoff instead of
// being retried on every poll.
func (p *keyboardPool) Sync(devices []ios.DeviceEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := make(map[string]bool, len(devices))
	for _, device := range devices {
		udid := device.Properties.SerialNumber
		if udid == "" {
			continue
		}
		seen[udid] = true
		if _, ok := p.sessions[udid]; ok {
			continue
		}
		if !device.SupportsRsd() {
			continue
		}
		failure, hasFailed := p.failed[udid]
		if hasFailed && time.Now().Before(failure.retryAt) {
			continue
		}
		session, err := p.dialer(device)
		if err != nil {
			attempts := failure.attempts + 1
			p.failed[udid] = poolFailure{attempts: attempts, retryAt: time.Now().Add(backoffFor(attempts))}
			slog.Warn("virtual keyboard session unavailable", "udid", udid, "attempt", attempts, "error", err)
			continue
		}
		p.sessions[udid] = session
		delete(p.failed, udid)
		slog.Info("virtual keyboard session started", "udid", udid)
	}
	for udid, session := range p.sessions {
		if seen[udid] {
			continue
		}
		if err := session.Close(); err != nil {
			slog.Warn("failed to close virtual keyboard session", "udid", udid, "error", err)
		}
		delete(p.sessions, udid)
		delete(p.failed, udid)
		slog.Info("virtual keyboard session removed", "udid", udid)
	}
	for udid := range p.failed {
		if !seen[udid] {
			delete(p.failed, udid)
		}
	}
}

// PressKeys sends one combination to a single device, or to every active
// session when udid is empty. A failed send closes that session so the next
// poll can recreate it.
func (p *keyboardPool) PressKeys(udid string, keys []byte) []daemonTargetResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	if udid != "" {
		session, ok := p.sessions[udid]
		if !ok {
			return []daemonTargetResult{{UDID: udid, Error: "no active virtual keyboard session for this device"}}
		}
		result := p.pressAndDropBroken(udid, session, keys)
		return []daemonTargetResult{result}
	}
	udids := make([]string, 0, len(p.sessions))
	for existing := range p.sessions {
		udids = append(udids, existing)
	}
	sort.Strings(udids)
	results := make([]daemonTargetResult, 0, len(udids))
	for _, target := range udids {
		results = append(results, p.pressAndDropBroken(target, p.sessions[target], keys))
	}
	return results
}

// Called with p.mu held; the next poll recreates a session after a failed send.
func (p *keyboardPool) pressAndDropBroken(udid string, session keyboardSession, keys []byte) daemonTargetResult {
	result := pressOnSession(udid, session, keys)
	if result.Error != "" {
		if err := session.Close(); err != nil {
			slog.Warn("failed to close broken virtual keyboard session", "udid", udid, "error", err)
		}
		delete(p.sessions, udid)
	}
	return result
}

// A HID write can fail with a panic inside the XPC framer; do not take the
// whole daemon down with it.
func pressOnSession(udid string, session keyboardSession, keys []byte) (result daemonTargetResult) {
	result = daemonTargetResult{UDID: udid}
	defer func() {
		if recovered := recover(); recovered != nil {
			result.Error = fmt.Sprintf("panic while sending HID reports: %v", recovered)
		}
	}()
	if err := session.PressKeys(keys); err != nil {
		result.Error = err.Error()
	}
	return result
}

// UDIDs reports the devices with an active session, sorted for stable output.
func (p *keyboardPool) UDIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	udids := make([]string, 0, len(p.sessions))
	for udid := range p.sessions {
		udids = append(udids, udid)
	}
	sort.Strings(udids)
	return udids
}

// Pending reports devices waiting for a retry, for status output.
func (p *keyboardPool) Pending() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	udids := make([]string, 0, len(p.failed))
	for udid := range p.failed {
		udids = append(udids, udid)
	}
	sort.Strings(udids)
	return udids
}

func (p *keyboardPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var errs []error
	for udid, session := range p.sessions {
		if err := session.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", udid, err))
		}
	}
	p.sessions = make(map[string]keyboardSession)
	p.failed = make(map[string]poolFailure)
	return errors.Join(errs...)
}

func backoffFor(attempts int) time.Duration {
	delay := time.Duration(attempts) * 5 * time.Second
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

// dialKeyboardSession opens a real virtual keyboard session for one device.
func dialKeyboardSession(device ios.DeviceEntry) (keyboardSession, error) {
	conn, err := hidkeyboard.New(device)
	if err != nil {
		return nil, err
	}
	session, err := conn.StartSession()
	if err != nil {
		// The caller cannot close conn through the session interface here.
		return nil, errors.Join(err, conn.Close())
	}
	return session, nil
}
