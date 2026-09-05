// Package protocol defines the wire format for version 1 of the agent protocol.
package protocol

import "github.com/ohmyjob/omj-agent/internal/version"

var ProtocolVersion = version.ProtocolVersion

const (
	BasePath = "/api/agent/v1"

	HeaderProtocolVersion = "X-OMJ-Protocol-Version"
	HeaderAgentVersion    = "X-OMJ-Agent-Version"
	HeaderServerVersion   = "X-OMJ-Server-Version"
)
