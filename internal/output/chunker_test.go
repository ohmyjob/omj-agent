package output

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ohmyjob/omj-agent/internal/runner"
)

var epoch = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

type harness struct {
	now     time.Time
	ticks   chan time.Time
	chunks  chan Chunk
	chunker *Chunker
}

func newHarness(t *testing.T, opts ChunkerOptions) *harness {
	t.Helper()

	h := &harness{now: epoch, ticks: make(chan time.Time), chunks: make(chan Chunk, 1024)}

	opts.Now = func() time.Time { return h.now }
	opts.Ticks = h.ticks
	h.chunker = NewChunker("run-1", func(c Chunk) { h.chunks <- c }, opts)

	t.Cleanup(func() { h.chunker.Close() })

	return h
}

func (h *harness) tick(after time.Duration) {
	h.now = h.now.Add(after)
	h.ticks <- h.now
}

func (h *harness) next(t *testing.T) Chunk {
	t.Helper()

	select {
	case chunk := <-h.chunks:
		return chunk
	case <-time.After(2 * time.Second):
		t.Fatal("no chunk was emitted")

		return Chunk{}
	}
}

func (h *harness) none(t *testing.T) {
	t.Helper()

	select {
	case chunk := <-h.chunks:
		t.Fatalf("unexpected chunk seq %d with %d bytes", chunk.Seq, len(chunk.Data))
	default:
	}
}

func (h *harness) drain() []Chunk {
	var all []Chunk

	for {
		select {
		case chunk := <-h.chunks:
			all = append(all, chunk)
		default:
			return all
		}
	}
}

func TestSmallOutputWaitsForTheFlushTick(t *testing.T) {
	h := newHarness(t, ChunkerOptions{})

	h.chunker.Write(runner.Stdout, bytes.Repeat([]byte("a"), 100))
	h.none(t)

	h.tick(500 * time.Millisecond)

	chunk := h.next(t)
	if chunk.Seq != 1 || chunk.Stream != runner.Stdout || len(chunk.Data) != 100 || chunk.RunID != "run-1" {
		t.Errorf("chunk = %+v, want seq 1 stdout 100 bytes for run-1", chunk)
	}

	if !chunk.At.Equal(epoch) {
		t.Errorf("At = %v, want the arrival time %v", chunk.At, epoch)
	}
}

func TestLargeOutputFlushesFullChunksImmediately(t *testing.T) {
	h := newHarness(t, ChunkerOptions{})

	h.chunker.Write(runner.Stdout, bytes.Repeat([]byte("b"), 70<<10))

	first := h.next(t)
	if first.Seq != 1 || len(first.Data) != DefaultChunkBytes {
		t.Errorf("first chunk seq %d with %d bytes, want seq 1 with %d", first.Seq, len(first.Data), DefaultChunkBytes)
	}

	h.none(t)

	seq, truncated := h.chunker.Close()

	rest := h.next(t)
	if rest.Seq != 2 || len(rest.Data) != 6<<10 {
		t.Errorf("remainder seq %d with %d bytes, want seq 2 with %d", rest.Seq, len(rest.Data), 6<<10)
	}

	if seq != 2 || truncated {
		t.Errorf("Close() = %d, %v, want 2, false", seq, truncated)
	}
}

func TestSequenceIsContiguousAcrossStreamsAndFlushes(t *testing.T) {
	h := newHarness(t, ChunkerOptions{ChunkBytes: 8})

	h.chunker.Write(runner.Stdout, []byte("out-1"))
	h.chunker.Write(runner.Stderr, []byte("err-1"))
	h.tick(500 * time.Millisecond)
	h.chunker.Write(runner.Stdout, []byte("out-2 is longer than one chunk"))
	h.chunker.Close()

	var (
		got  []string
		seqs []uint64
	)

	for _, chunk := range h.drain() {
		got = append(got, string(chunk.Stream)+":"+string(chunk.Data))
		seqs = append(seqs, chunk.Seq)

		if len(chunk.Data) > 8 {
			t.Errorf("chunk %d has %d bytes, want at most 8", chunk.Seq, len(chunk.Data))
		}
	}

	want := []string{"stdout:out-1", "stderr:err-1", "stdout:out-2 is", "stdout: longer ", "stdout:than one", "stdout: chunk"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("chunks = %v, want %v", got, want)
	}

	for i, seq := range seqs {
		if seq != uint64(i+1) {
			t.Fatalf("seqs = %v, want 1..%d", seqs, len(want))
		}
	}
}

func TestLocalCapStopsEmittingButKeepsDraining(t *testing.T) {
	h := newHarness(t, ChunkerOptions{ChunkBytes: 64, MaxOutput: 100})

	h.chunker.Write(runner.Stdout, bytes.Repeat([]byte("c"), 150))
	h.chunker.Write(runner.Stderr, bytes.Repeat([]byte("d"), 10))

	seq, truncated := h.chunker.Close()

	var total int

	for _, chunk := range h.drain() {
		total += len(chunk.Data)
	}

	if total != 100 || seq != 2 || !truncated {
		t.Errorf("emitted %d bytes over %d chunks, truncated %v; want 100 bytes, 2 chunks, true", total, seq, truncated)
	}
}

func TestExactlyTheCapIsNotTruncated(t *testing.T) {
	h := newHarness(t, ChunkerOptions{MaxOutput: 10})

	h.chunker.Write(runner.Stdout, bytes.Repeat([]byte("e"), 10))

	if _, truncated := h.chunker.Close(); truncated {
		t.Error("Close() reported truncation for output exactly at the cap")
	}
}

func TestWritesAfterCloseAreDropped(t *testing.T) {
	h := newHarness(t, ChunkerOptions{})

	h.chunker.Write(runner.Stdout, []byte("before"))
	seq, _ := h.chunker.Close()
	h.chunker.Write(runner.Stdout, []byte("after"))

	if again, _ := h.chunker.Close(); again != seq || seq != 1 {
		t.Errorf("Close() = %d then %d, want 1 both times", seq, again)
	}

	if chunks := h.drain(); len(chunks) != 1 || string(chunks[0].Data) != "before" {
		t.Errorf("chunks = %v, want only the one written before Close", chunks)
	}
}

func TestConcurrentWritersKeepEveryByteInOrderPerStream(t *testing.T) {
	h := newHarness(t, ChunkerOptions{ChunkBytes: 16})

	var wg sync.WaitGroup

	for _, stream := range []runner.Stream{runner.Stdout, runner.Stderr} {
		wg.Go(func() {
			for i := range 200 {
				h.chunker.Write(stream, []byte(string(stream)[:3]+strings.Repeat("x", i%7)+"\n"))
			}
		})
	}

	go func() {
		for range 20 {
			h.ticks <- epoch
		}
	}()

	wg.Wait()

	seq, _ := h.chunker.Close()
	chunks := h.drain()

	if uint64(len(chunks)) != seq {
		t.Fatalf("got %d chunks, Close() reported %d", len(chunks), seq)
	}

	lines := map[runner.Stream]int{}

	for i, chunk := range chunks {
		if chunk.Seq != uint64(i+1) {
			t.Fatalf("chunk %d has seq %d", i, chunk.Seq)
		}

		lines[chunk.Stream] += bytes.Count(chunk.Data, []byte("\n"))
	}

	if lines[runner.Stdout] != 200 || lines[runner.Stderr] != 200 {
		t.Errorf("lines = %v, want 200 per stream", lines)
	}
}

func TestTheRealTickerFlushesAndStopsOnClose(t *testing.T) {
	chunks := make(chan Chunk, 8)
	chunker := NewChunker("run-1", func(c Chunk) { chunks <- c }, ChunkerOptions{FlushInterval: 5 * time.Millisecond})

	chunker.Write(runner.Stdout, []byte("tick"))

	select {
	case chunk := <-chunks:
		if string(chunk.Data) != "tick" {
			t.Errorf("chunk = %q, want %q", chunk.Data, "tick")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the ticker never flushed")
	}

	if seq, _ := chunker.Close(); seq != 1 {
		t.Errorf("Close() = %d, want 1", seq)
	}
}
