# 010 · Output chunker and buffer

Status: done
Repo: ohmyjob-agent
Depends on: 008
PRD: §14.2 (`output` payload, limits), §16.5 (flush, cap), §16.6 (bounded buffering)

## Goal

Turn raw stdout/stderr bytes into ordered, sized, retriable chunks without unbounded memory.

## Scope

- `internal/output.Chunker` implements the runner sink: one monotonic `seq` per Run starting at 1, chunks tagged `stdout`/`stderr` in arrival order, flush on 500 ms tick or when the pending chunk reaches 64 KiB (both injectable), never splits a chunk beyond the size cap.
- Local cap: after `MaxOutput` bytes the chunker stops emitting, marks `Truncated`, but keeps draining the pipes so the process never blocks.
- `output.Buffer`: holds unacknowledged chunks per Run up to the Run's cap and a global cap (64 MiB across Runs, injectable); when the global cap is hit, drop the oldest chunks of the largest Run and mark that Run truncated. `AckUpTo(seq)` releases memory. `NextBatch(maxBytes = 256 KiB, maxChunks = 64)` returns the next contiguous chunks to send.
- `Chunker.Close()` flushes remaining bytes and returns the final `seq` and truncation flag for `finish`.

## Files

- `internal/output/chunker.go`, `buffer.go`, `*_test.go`

## Acceptance criteria

- [ ] Sequence numbers are contiguous and stable across flushes; no chunk exceeds 64 KiB.
- [ ] With a fake clock, 100 bytes written at t=0 flush at t=500 ms; 70 KiB written at once flushes immediately as one 64 KiB chunk plus a pending remainder.
- [ ] Global cap drops oldest data and flags truncation instead of growing.

## Tests

- Deterministic tests with fake clock and small caps; race-free under `-race`.

## Outcome (2026-09-04)

- `output.Chunk` carries `RunID`, `Seq` (uint64, one counter per Run across both streams, starting at 1), `Stream` (the runner's type), `At` (when the first byte of the chunk arrived) and `Data`; task 003 maps it onto the protocol struct and base64-encodes `Data` there.
- `NewChunker(runID, receive func(Chunk), ChunkerOptions)` hands finished chunks to a callback under its own lock, so arrival order and sequence order are the same thing; the `Buffer` is that callback in production. Writes copy into a pending chunk and return at once; a stream change flushes the pending chunk first so interleaving is preserved; only a full 64 KiB chunk or the 500 ms tick flushes otherwise. The tick comes from a real ticker unless `Ticks` is injected, and `Now` is injectable, which is how the tests stay deterministic.
- The local cap counts bytes accepted, truncates the write that crosses it at exactly `MaxOutput`, then drops everything after while still returning immediately, so the process is drained but nothing more is emitted. `MaxOutput` of zero or less means no local cap; the agent loop always passes the clamped effective value. `Close()` stops the ticker, flushes the remainder, and returns the last sequence and the truncation flag; later writes (a grandchild past the runner's wait delay) are dropped and a second `Close()` returns the same answer.
- The `Buffer` enforces the global cap only (64 MiB by default, injectable). The per-Run cap needs no second enforcement because the chunker never emits more than `MaxOutput` bytes for a Run. Making room drops the oldest chunks of the Run holding the most bytes, one at a time, and marks that Run truncated; a chunk that cannot fit even in an empty buffer is dropped and flagged the same way. Drops only ever remove from the front of a Run, so what remains stays contiguous.
- `NextBatch(runID)` is per Run because output is posted per Run; it returns the oldest unacknowledged chunks within the batch limits (256 KiB, 64 chunks, injectable), always at least one, stopping at a gap, and returns the same chunks again until `AckUpTo(runID, seq)` releases them, which is what makes a failed send retriable. `Forget(runID)` releases a finished Run; `Truncated(runID)` is what the finish report ORs with the chunker's flag; `Size()` is the total buffered bytes.

