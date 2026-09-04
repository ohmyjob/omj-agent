package output

import (
	"encoding/json"
	"testing"

	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/runner"
)

func TestChunkProtocol(t *testing.T) {
	chunk := Chunk{RunID: "run-1", Seq: 12, Stream: runner.Stderr, At: epoch, Data: []byte("Backing up /srv/data\n")}

	got := chunk.Protocol()

	if got.Seq != 12 || got.Stream != protocol.StreamStderr || !got.At.Equal(epoch) || string(got.Data) != "Backing up /srv/data\n" {
		t.Errorf("Protocol() = %+v", got)
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"seq":12,"stream":"stderr","at":"2026-09-04T12:00:00Z","data":"QmFja2luZyB1cCAvc3J2L2RhdGEK"}`
	if string(encoded) != want {
		t.Errorf("marshal = %s, want %s", encoded, want)
	}
}
