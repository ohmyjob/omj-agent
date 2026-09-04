package output

import "github.com/ohmyjob/omj-agent/internal/protocol"

func (c Chunk) Protocol() protocol.OutputChunk {
	return protocol.OutputChunk{
		Seq:    c.Seq,
		Stream: protocol.Stream(c.Stream),
		At:     c.At,
		Data:   c.Data,
	}
}
