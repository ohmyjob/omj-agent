package version

// Set through -ldflags by the Makefile and the release pipeline.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// ProtocolVersion is mutable for the e2e-only compatibility override.
var ProtocolVersion = 1

func UserAgent() string {
	return "omj-agent/" + Version
}
