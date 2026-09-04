package output

import (
	"sync"
)

const (
	DefaultGlobalCap   int64 = 64 << 20
	DefaultBatchBytes        = 256 << 10
	DefaultBatchChunks       = 64
)

type BufferOptions struct {
	GlobalCap   int64
	BatchBytes  int
	BatchChunks int
}

type runOutput struct {
	chunks    []Chunk
	bytes     int64
	truncated bool
}

// Buffer keeps the chunks the Server has not acknowledged, per Run, under one
// global cap shared by every Run on this Machine.
type Buffer struct {
	globalCap   int64
	batchBytes  int
	batchChunks int

	mu   sync.Mutex
	runs map[string]*runOutput
	size int64
}

func NewBuffer(opts BufferOptions) *Buffer {
	b := &Buffer{
		globalCap:   opts.GlobalCap,
		batchBytes:  opts.BatchBytes,
		batchChunks: opts.BatchChunks,
		runs:        map[string]*runOutput{},
	}

	if b.globalCap <= 0 {
		b.globalCap = DefaultGlobalCap
	}

	if b.batchBytes <= 0 {
		b.batchBytes = DefaultBatchBytes
	}

	if b.batchChunks <= 0 {
		b.batchChunks = DefaultBatchChunks
	}

	return b
}

// Add keeps the chunk, making room by dropping the oldest chunks of whichever
// Run holds the most, so one noisy Run cannot starve the others of memory.
func (b *Buffer) Add(chunk Chunk) {
	b.mu.Lock()
	defer b.mu.Unlock()

	size := int64(len(chunk.Data))
	run := b.run(chunk.RunID)

	for b.size+size > b.globalCap {
		largest := b.largestRun()
		if largest == nil {
			break
		}

		largest.truncated = true
		b.dropOldest(largest)
	}

	if b.size+size > b.globalCap {
		run.truncated = true

		return
	}

	run.chunks = append(run.chunks, chunk)
	run.bytes += size
	b.size += size
}

// NextBatch returns the oldest unacknowledged chunks of the Run that fit the
// batch limits, always at least one, in contiguous sequence order. The same
// chunks come back until AckUpTo releases them, which is what makes a failed
// send retriable.
func (b *Buffer) NextBatch(runID string) []Chunk {
	b.mu.Lock()
	defer b.mu.Unlock()

	run := b.runs[runID]
	if run == nil {
		return nil
	}

	var (
		batch []Chunk
		bytes int
	)

	for i, chunk := range run.chunks {
		if len(batch) == b.batchChunks {
			break
		}

		if i > 0 && chunk.Seq != run.chunks[i-1].Seq+1 {
			break
		}

		if len(batch) > 0 && bytes+len(chunk.Data) > b.batchBytes {
			break
		}

		batch = append(batch, chunk)
		bytes += len(chunk.Data)
	}

	return batch
}

func (b *Buffer) AckUpTo(runID string, seq uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	run := b.runs[runID]
	if run == nil {
		return
	}

	for len(run.chunks) > 0 && run.chunks[0].Seq <= seq {
		b.dropOldest(run)
	}
}

func (b *Buffer) Truncated(runID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	run := b.runs[runID]

	return run != nil && run.truncated
}

// Forget releases everything kept for a Run once its finish has been reported.
func (b *Buffer) Forget(runID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if run := b.runs[runID]; run != nil {
		b.size -= run.bytes
		delete(b.runs, runID)
	}
}

func (b *Buffer) Size() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.size
}

func (b *Buffer) run(runID string) *runOutput {
	run := b.runs[runID]
	if run == nil {
		run = &runOutput{}
		b.runs[runID] = run
	}

	return run
}

func (b *Buffer) largestRun() *runOutput {
	var largest *runOutput

	for _, run := range b.runs {
		if len(run.chunks) > 0 && (largest == nil || run.bytes > largest.bytes) {
			largest = run
		}
	}

	return largest
}

func (b *Buffer) dropOldest(run *runOutput) {
	size := int64(len(run.chunks[0].Data))
	run.chunks[0] = Chunk{}
	run.chunks = run.chunks[1:]
	run.bytes -= size
	b.size -= size
}
