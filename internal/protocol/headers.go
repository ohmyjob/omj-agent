// Package protocol mirrors version 1 of the Server's agent protocol: the
// request and response shapes, the string constants they use and the header
// names. It is the only package that knows the wire format.
package protocol

import "github.com/ohmyjob/omj-agent/internal/version"

const (
	ProtocolVersion = version.ProtocolVersion

	BasePath = "/api/agent/v1"

	HeaderProtocolVersion = "X-OMJ-Protocol-Version"
	HeaderAgentVersion    = "X-OMJ-Agent-Version"
	HeaderServerVersion   = "X-OMJ-Server-Version"
)
