package client

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"time"
)

const (
	BackoffMin    = time.Second
	BackoffMax    = 60 * time.Second
	BackoffFactor = 2.0
	BackoffJitter = 0.2

	// AuthRetryInterval is how often the Agent retries after a 401 or 426,
	// which waiting cannot fix but a re-enrolment or a Server upgrade can.
	AuthRetryInterval = 5 * time.Minute
)

// Sleeper waits for the duration or until the context ends.
type Sleeper func(ctx context.Context, d time.Duration) error

// Backoff produces the exponential, jittered delays of PRD §16.6. The zero
// value uses the protocol defaults; Rand and Sleep are injectable for tests.
type Backoff struct {
	Min    time.Duration
	Max    time.Duration
	Factor float64
	Jitter float64
	Rand   func() float64
	Sleep  Sleeper

	attempt int
}

func (b *Backoff) Next() time.Duration {
	minimum, maximum, factor, jitter := b.settings()

	base := float64(minimum) * math.Pow(factor, float64(b.attempt))
	if base > float64(maximum) {
		base = float64(maximum)
	} else {
		b.attempt++
	}

	random := rand.Float64
	if b.Rand != nil {
		random = b.Rand
	}

	// random in [0, 1) maps onto [-jitter, +jitter).
	return time.Duration(base * (1 + jitter*(2*random()-1)))
}

func (b *Backoff) Reset() {
	b.attempt = 0
}

func (b *Backoff) settings() (minimum, maximum time.Duration, factor, jitter float64) {
	minimum, maximum, factor, jitter = b.Min, b.Max, b.Factor, b.Jitter

	if minimum <= 0 {
		minimum = BackoffMin
	}

	if maximum <= 0 {
		maximum = BackoffMax
	}

	if factor <= 0 {
		factor = BackoffFactor
	}

	if jitter <= 0 {
		jitter = BackoffJitter
	}

	return minimum, maximum, factor, jitter
}

func (b *Backoff) sleep(ctx context.Context, d time.Duration) error {
	if b.Sleep != nil {
		return b.Sleep(ctx, d)
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Retry runs op until it succeeds, fails with a final error, or the context
// ends. Only idempotent operations belong here; every protocol endpoint except
// enroll is one. A 429 waits at least its Retry-After.
func Retry(ctx context.Context, backoff *Backoff, op func(ctx context.Context) error) error {
	for {
		err := op(ctx)
		if err == nil {
			backoff.Reset()

			return nil
		}

		if !IsRetryable(err) {
			return err
		}

		delay := backoff.Next()
		if wait := RetryAfter(err); wait > delay {
			delay = wait
		}

		if sleepErr := backoff.sleep(ctx, delay); sleepErr != nil {
			return errors.Join(sleepErr, err)
		}
	}
}
