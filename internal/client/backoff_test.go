package client

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/ohmyjob/omj-agent/internal/protocol"
)

func fixedRand(values ...float64) func() float64 {
	i := 0

	return func() float64 {
		v := values[i%len(values)]
		i++

		return v
	}
}

func TestBackoffSequenceIsReproducible(t *testing.T) {
	b := &Backoff{Rand: fixedRand(0.5)}

	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second}

	for i, d := range want {
		if got := b.Next(); got != d {
			t.Fatalf("Next() #%d = %s, want %s", i+1, got, d)
		}
	}
}

func TestJitterStaysWithinTwentyPercent(t *testing.T) {
	tests := []struct {
		name string
		rand float64
		want time.Duration
	}{
		{name: "lowest", rand: 0, want: 800 * time.Millisecond},
		{name: "middle", rand: 0.5, want: time.Second},
		{name: "highest", rand: 0.999999, want: 1199999 * time.Microsecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Backoff{Rand: fixedRand(tt.rand)}

			got := b.Next()
			if got < 800*time.Millisecond || got > 1200*time.Millisecond {
				t.Fatalf("Next() = %s, outside ±20%% of 1s", got)
			}

			if got.Round(time.Millisecond) != tt.want.Round(time.Millisecond) {
				t.Errorf("Next() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestResetStartsOver(t *testing.T) {
	b := &Backoff{Rand: fixedRand(0.5)}

	b.Next()
	b.Next()
	b.Reset()

	if got := b.Next(); got != time.Second {
		t.Errorf("Next() after Reset() = %s, want 1s", got)
	}
}

type fakeSleeper struct {
	slept []time.Duration
	err   error
}

func (f *fakeSleeper) sleep(_ context.Context, d time.Duration) error {
	f.slept = append(f.slept, d)

	return f.err
}

func TestRetryStopsOnAFinalError(t *testing.T) {
	sleeper := &fakeSleeper{}
	b := &Backoff{Rand: fixedRand(0.5), Sleep: sleeper.sleep}
	calls := 0
	final := newAPIError(http.StatusUnprocessableEntity, protocol.ErrorResponse{Error: protocol.ErrValidationFailed}, 0)

	err := Retry(context.Background(), b, func(context.Context) error {
		calls++

		return final
	})

	if !errors.Is(err, final) {
		t.Fatalf("Retry() error = %v, want the final error", err)
	}

	if calls != 1 || len(sleeper.slept) != 0 {
		t.Errorf("calls = %d, sleeps = %v; want one call and no sleep", calls, sleeper.slept)
	}
}

func TestRetryWaitsBetweenRetryableFailures(t *testing.T) {
	sleeper := &fakeSleeper{}
	b := &Backoff{Rand: fixedRand(0.5), Sleep: sleeper.sleep}
	calls := 0

	err := Retry(context.Background(), b, func(context.Context) error {
		calls++
		if calls < 3 {
			return newAPIError(http.StatusInternalServerError, protocol.ErrorResponse{}, 0)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Retry() error = %v", err)
	}

	want := []time.Duration{time.Second, 2 * time.Second}
	if calls != 3 || len(sleeper.slept) != len(want) || sleeper.slept[0] != want[0] || sleeper.slept[1] != want[1] {
		t.Errorf("calls = %d, sleeps = %v; want 3 calls and %v", calls, sleeper.slept, want)
	}

	if got := b.Next(); got != time.Second {
		t.Errorf("Next() after success = %s, want the backoff reset to 1s", got)
	}
}

func TestRetryHonoursRetryAfter(t *testing.T) {
	sleeper := &fakeSleeper{}
	b := &Backoff{Rand: fixedRand(0.5), Sleep: sleeper.sleep}
	calls := 0

	err := Retry(context.Background(), b, func(context.Context) error {
		calls++
		if calls == 1 {
			return newAPIError(http.StatusTooManyRequests, protocol.ErrorResponse{Error: protocol.ErrThrottled}, 5*time.Second)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Retry() error = %v", err)
	}

	if len(sleeper.slept) != 1 || sleeper.slept[0] != 5*time.Second {
		t.Errorf("sleeps = %v, want [5s]", sleeper.slept)
	}
}

func TestRetryStopsWhenTheContextEnds(t *testing.T) {
	sleeper := &fakeSleeper{err: context.Canceled}
	b := &Backoff{Rand: fixedRand(0.5), Sleep: sleeper.sleep}
	failure := newAPIError(http.StatusBadGateway, protocol.ErrorResponse{}, 0)

	err := Retry(context.Background(), b, func(context.Context) error { return failure })

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Retry() error = %v, want the context error", err)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadGateway {
		t.Errorf("Retry() error = %v, want it to carry the last failure", err)
	}
}

func TestTheDefaultSleeperReturnsWhenTheContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	b := &Backoff{}

	started := time.Now()
	err := b.sleep(ctx, time.Hour)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("sleep() error = %v, want context.Canceled", err)
	}

	if time.Since(started) > time.Second {
		t.Error("sleep() waited for the timer instead of the context")
	}
}
