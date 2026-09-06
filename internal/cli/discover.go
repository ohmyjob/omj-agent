package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/ohmyjob/omj-agent/internal/agent"
	"github.com/ohmyjob/omj-agent/internal/discovery"
)

type discoverCommand struct {
	collect func(context.Context) (discovery.Result, error)
}

func runDiscover(args []string, stdout, stderr io.Writer) int {
	return discoverCommand{collect: discovery.Collect}.discover(args, stdout, stderr)
}

// discover sends nothing. An operator has to be able to read what a discovery
// would carry off the machine before letting the Server ask for one, so this
// prints the exact body and stops there.
func (c discoverCommand) discover(args []string, stdout, stderr io.Writer) int {
	if stop, code := parseNoFlags("discover", args, stderr); stop {
		return code
	}

	result, err := c.collect(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "omj-agent discover: %v\n", err)

		return ExitError
	}

	payload := agent.DiscoveryPayload(result)

	encoded, err := json.MarshalIndent(payload, "", "    ")
	if err != nil {
		fmt.Fprintf(stderr, "omj-agent discover: %v\n", err)

		return ExitError
	}

	fmt.Fprintf(stdout, "%s\n", encoded)

	// The summary goes to stderr so stdout stays the payload and nothing else.
	fmt.Fprintf(stderr, "\n%d entries, %d omitted, %d unreadable sources. Nothing was sent.\n",
		len(payload.Entries), payload.OmittedEntries, len(payload.UnreadableSources))

	return ExitOK
}
