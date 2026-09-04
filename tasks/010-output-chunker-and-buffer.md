# 010 · Output chunker and buffer

Status: todo
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
