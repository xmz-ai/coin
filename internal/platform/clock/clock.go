package clock

import "time"

// Clock provides current UTC time.
type Clock interface {
	NowUTC() time.Time
}

type fixedClock struct {
	now time.Time
}

// NewFixed returns a clock with stable UTC time.
func NewFixed(now time.Time) Clock {
	return fixedClock{now: now.UTC()}
}

func (c fixedClock) NowUTC() time.Time {
	return c.now
}

type systemClock struct{}

// NewSystem returns a clock backed by time.Now().
func NewSystem() Clock {
	return systemClock{}
}

func (systemClock) NowUTC() time.Time {
	return time.Now().UTC()
}
