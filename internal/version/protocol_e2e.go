//go:build e2e

package version

import (
	"os"
	"strconv"
)

// Release binaries must not allow the environment to override the protocol version.
func init() {
	value, err := strconv.Atoi(os.Getenv("OMJ_TEST_PROTOCOL_VERSION"))
	if err != nil || value <= 0 {
		return
	}

	ProtocolVersion = value
}
