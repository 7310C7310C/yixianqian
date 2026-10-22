package platform

import "time"

// Clock is the tiny time abstraction used by request handlers so tests can
// control lockout and rate-limit decisions.
type Clock interface {
	Now() time.Time
}

// SystemClock reads the wall clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }
