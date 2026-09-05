//go:build e2e

package version

import (
	"os"
	"strconv"
)

// A release binary never carries this file: it is behind the e2e build tag, so the
// override cannot be reached in production however the environment is set. The
// end-to-end suite builds one Agent with the tag to prove the Server refuses a
// protocol version it does not speak.
func init() {
	value, err := strconv.Atoi(os.Getenv("OMJ_TEST_PROTOCOL_VERSION"))
	if err != nil || value <= 0 {
		return
	}

	ProtocolVersion = value
}
