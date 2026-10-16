//go:build darwin || linux

package hidkeyboard

import "golang.org/x/sys/unix"

// The device expects host monotonic time in the report, not time elapsed
// since this go-ios process was started.
func monotonicTimestamp() (uint64, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		return 0, err
	}
	return uint64(ts.Sec)*1_000_000_000 + uint64(ts.Nsec), nil
}
