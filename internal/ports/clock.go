package ports

import "time"

// Clock abstracts system time for deterministic testing.
type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
}

// Timer abstracts a cancellable timer channel.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// Jitter generates random floats in [0, 1) for retry backoff jitter.
type Jitter interface {
	Float64() float64
}
