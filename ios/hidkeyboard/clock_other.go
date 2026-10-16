//go:build !darwin && !linux

package hidkeyboard

import "time"

// Fallback on platforms where this package does not yet bind to an OS
// monotonic clock. Input delivery on these platforms is not device-tested.
var processOrigin = time.Now()

func monotonicTimestamp() (uint64, error) {
	return uint64(time.Since(processOrigin).Nanoseconds()), nil
}
