package output

import (
	"bytes"
	"slices"
	"sync"
	"testing"

	"github.com/ohmyjob/omj-agent/internal/runner"
)

func chunk(runID string, seq uint64, size int) Chunk {
	return Chunk{RunID: runID, Seq: seq, Stream: runner.Stdout, At: epoch, Data: bytes.Repeat([]byte("x"), size)}
}

func seqs(chunks []Chunk) []uint64 {
	out := make([]uint64, 0, len(chunks))

	for _, c := range chunks {
		out = append(out, c.Seq)
	}

	return out
}

func TestNextBatchRespectsTheLimits(t *testing.T) {
	tests := []struct {
		name   string
		sizes  []int
		bytes  int
		chunks int
		want   []uint64
	}{
		{name: "everything fits", sizes: []int{10, 10, 10}, bytes: 100, chunks: 10, want: []uint64{1, 2, 3}},
		{name: "stops at the byte limit", sizes: []int{40, 40, 40}, bytes: 100, chunks: 10, want: []uint64{1, 2}},
		{name: "stops at the chunk limit", sizes: []int{1, 1, 1, 1}, bytes: 100, chunks: 3, want: []uint64{1, 2, 3}},
		{name: "always returns the first chunk", sizes: []int{500}, bytes: 100, chunks: 10, want: []uint64{1}},
		{name: "nothing buffered", sizes: nil, bytes: 100, chunks: 10, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBuffer(BufferOptions{BatchBytes: tt.bytes, BatchChunks: tt.chunks})

			for i, size := range tt.sizes {
				b.Add(chunk("run-1", uint64(i+1), size))
			}

			if got := seqs(b.NextBatch("run-1")); !slices.Equal(got, tt.want) {
				t.Errorf("NextBatch() seqs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNextBatchStopsAtAGapInTheSequence(t *testing.T) {
	b := NewBuffer(BufferOptions{})

	b.Add(chunk("run-1", 1, 1))
	b.Add(chunk("run-1", 2, 1))
	b.Add(chunk("run-1", 4, 1))

	if got := seqs(b.NextBatch("run-1")); !slices.Equal(got, []uint64{1, 2}) {
		t.Errorf("NextBatch() seqs = %v, want [1 2]", got)
	}
}

func TestAckReleasesChunksAndTheSameBatchRepeatsUntilThen(t *testing.T) {
	b := NewBuffer(BufferOptions{BatchChunks: 2})

	for seq := uint64(1); seq <= 4; seq++ {
		b.Add(chunk("run-1", seq, 10))
	}

	if got := seqs(b.NextBatch("run-1")); !slices.Equal(got, []uint64{1, 2}) {
		t.Fatalf("first NextBatch() seqs = %v, want [1 2]", got)
	}

	if got := seqs(b.NextBatch("run-1")); !slices.Equal(got, []uint64{1, 2}) {
		t.Fatalf("repeated NextBatch() seqs = %v, want [1 2] again", got)
	}

	b.AckUpTo("run-1", 2)

	if got := seqs(b.NextBatch("run-1")); !slices.Equal(got, []uint64{3, 4}) {
		t.Errorf("NextBatch() after ack seqs = %v, want [3 4]", got)
	}

	if b.Size() != 20 {
		t.Errorf("Size() = %d, want 20", b.Size())
	}

	b.AckUpTo("run-1", 10)

	if got := b.NextBatch("run-1"); got != nil || b.Size() != 0 {
		t.Errorf("NextBatch() = %v with Size() %d after acking everything", got, b.Size())
	}
}

func TestGlobalCapDropsTheOldestChunksOfTheLargestRun(t *testing.T) {
	b := NewBuffer(BufferOptions{GlobalCap: 300})

	b.Add(chunk("run-a", 1, 100))
	b.Add(chunk("run-a", 2, 100))
	b.Add(chunk("run-b", 1, 100))

	b.Add(chunk("run-b", 2, 50))

	if b.Size() != 250 {
		t.Errorf("Size() = %d, want 250 after dropping one chunk", b.Size())
	}

	if got := seqs(b.NextBatch("run-a")); !slices.Equal(got, []uint64{2}) {
		t.Errorf("run-a seqs = %v, want [2]", got)
	}

	if got := seqs(b.NextBatch("run-b")); !slices.Equal(got, []uint64{1, 2}) {
		t.Errorf("run-b seqs = %v, want [1 2]", got)
	}

	if !b.Truncated("run-a") || b.Truncated("run-b") {
		t.Errorf("Truncated() run-a %v run-b %v, want true false", b.Truncated("run-a"), b.Truncated("run-b"))
	}
}

func TestAChunkLargerThanTheCapIsDroppedAndFlagged(t *testing.T) {
	b := NewBuffer(BufferOptions{GlobalCap: 100})

	b.Add(chunk("run-1", 1, 60))
	b.Add(chunk("run-1", 2, 150))

	if got := b.NextBatch("run-1"); got != nil || b.Size() != 0 {
		t.Errorf("NextBatch() = %v with Size() %d, want nothing left once the cap cannot be met", got, b.Size())
	}

	if !b.Truncated("run-1") {
		t.Error("Truncated() = false, want true")
	}
}

func TestForgetReleasesARun(t *testing.T) {
	b := NewBuffer(BufferOptions{GlobalCap: 100})

	b.Add(chunk("run-1", 1, 60))
	b.Add(chunk("run-1", 2, 60))
	b.Forget("run-1")

	if b.Size() != 0 || b.Truncated("run-1") || b.NextBatch("run-1") != nil {
		t.Errorf("Size() = %d, Truncated() = %v after Forget", b.Size(), b.Truncated("run-1"))
	}
}

func TestBufferIsSafeForConcurrentUse(t *testing.T) {
	b := NewBuffer(BufferOptions{GlobalCap: 1 << 10})

	var wg sync.WaitGroup

	for _, runID := range []string{"run-a", "run-b", "run-c"} {
		wg.Go(func() {
			for seq := uint64(1); seq <= 100; seq++ {
				b.Add(chunk(runID, seq, 32))

				if seq%10 == 0 {
					b.AckUpTo(runID, seq-5)
					b.NextBatch(runID)
				}
			}
		})
	}

	wg.Wait()

	if b.Size() > 1<<10 {
		t.Errorf("Size() = %d, want at most the cap", b.Size())
	}
}
