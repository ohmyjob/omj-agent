package version

// Set through -ldflags by the Makefile and the release pipeline.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// ProtocolVersion is a variable rather than a constant so the end-to-end suite can
// build an Agent that claims an unsupported version and watch the Server refuse it.
// Only a binary built with the e2e tag reads that override; see protocol_e2e.go.
var ProtocolVersion = 1

func UserAgent() string {
	return "omj-agent/" + Version
}
