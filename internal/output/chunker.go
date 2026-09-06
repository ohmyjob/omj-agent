// Package output turns a Run's raw stdout and stderr into ordered, sized
// chunks and keeps the ones the Server has not acknowledged yet, without ever
// blocking the process that produces them or growing without bound.
package output

import (
	"sync"
	"time"

	"github.com/ohmyjob/omj-agent/internal/runner"
)

const (
	DefaultChunkBytes    = 64 << 10
	DefaultFlushInterval = 500 * time.Millisecond
)

// Chunk is one entry of the output payload: seq is a single counter per Run
// across both streams, starting at 1, and At is when the first byte arrived.
type Chunk struct {
	RunID  string
	Seq    uint64
	Stream runner.Stream
	At     time.Time
	Data   []byte
}

type ChunkerOptions struct {
	ChunkBytes    int
	FlushInterval time.Duration
	MaxOutput     int64
	Now           func() time.Time
	Ticks         <-chan time.Time
}

type pending struct {
	stream runner.Stream
	at     time.Time
	data   []byte
}

type Chunker struct {
	runID      string
	chunkBytes int
	maxOutput  int64
	now        func() time.Time
	receive    func(Chunk)

	mu        sync.Mutex
	pending   pending
	seq       uint64
	written   int64
	truncated bool
	closed    bool

	ticker    *time.Ticker
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

var _ runner.Sink = (*Chunker)(nil)

// NewChunker starts flushing pending output every FlushInterval until Close.
// A MaxOutput of zero or less means no local cap.
func NewChunker(runID string, receive func(Chunk), opts ChunkerOptions) *Chunker {
	c := &Chunker{
		runID:      runID,
		chunkBytes: opts.ChunkBytes,
		maxOutput:  opts.MaxOutput,
		now:        opts.Now,
		receive:    receive,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}

	if c.chunkBytes <= 0 {
		c.chunkBytes = DefaultChunkBytes
	}

	if c.now == nil {
		c.now = time.Now
	}

	ticks := opts.Ticks
	if ticks == nil {
		interval := opts.FlushInterval
		if interval <= 0 {
			interval = DefaultFlushInterval
		}

		c.ticker = time.NewTicker(interval)
		ticks = c.ticker.C
	}

	go c.flushOnTicks(ticks)

	return c
}

// Write copies the bytes it keeps and returns immediately, so the copying
// goroutines of the runner never wait on the network or on memory pressure.
func (c *Chunker) Write(stream runner.Stream, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}

	for len(data) > 0 {
		if c.maxOutput > 0 {
			remaining := c.maxOutput - c.written
			if remaining <= 0 {
				c.truncated = true

				return
			}

			if int64(len(data)) > remaining {
				data = data[:remaining]
				c.truncated = true
			}
		}

		if len(c.pending.data) > 0 && c.pending.stream != stream {
			c.flushLocked()
		}

		if len(c.pending.data) == 0 {
			c.pending = pending{stream: stream, at: c.now(), data: make([]byte, 0, c.chunkBytes)}
		}

		n := min(c.chunkBytes-len(c.pending.data), len(data))
		c.pending.data = append(c.pending.data, data[:n]...)
		c.written += int64(n)
		data = data[n:]

		if len(c.pending.data) == c.chunkBytes {
			c.flushLocked()
		}
	}
}

// Close flushes what is still pending; the finish report carries the values
// it returns.
func (c *Chunker) Close() (lastSeq uint64, truncated bool) {
	c.closeOnce.Do(func() {
		close(c.stop)
		<-c.done

		if c.ticker != nil {
			c.ticker.Stop()
		}

		c.mu.Lock()
		defer c.mu.Unlock()

		c.flushLocked()
		c.closed = true
	})

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.seq, c.truncated
}

func (c *Chunker) flushOnTicks(ticks <-chan time.Time) {
	defer close(c.done)

	for {
		select {
		case <-ticks:
			c.mu.Lock()
			c.flushLocked()
			c.mu.Unlock()
		case <-c.stop:
			return
		}
	}
}

func (c *Chunker) flushLocked() {
	if len(c.pending.data) == 0 {
		return
	}

	c.seq++
	c.receive(Chunk{
		RunID:  c.runID,
		Seq:    c.seq,
		Stream: c.pending.stream,
		At:     c.pending.at,
		Data:   c.pending.data,
	})
	c.pending = pending{}
}
