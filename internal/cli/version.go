package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/ohmyjob/omj-agent/internal/version"
)

func runVersion(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(stderr)

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}

		return ExitUsage
	}

	fmt.Fprintf(stdout, "omj-agent %s (%s, %s) protocol %d\n", version.Version, version.Commit, version.Date, version.ProtocolVersion)

	return ExitOK
}
