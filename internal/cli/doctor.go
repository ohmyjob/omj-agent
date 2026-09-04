package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/ohmyjob/omj-agent/internal/doctor"
)

func runDoctor(args []string, stdout, stderr io.Writer) int {
	return hostCommand{host: doctor.DefaultHost()}.doctor(args, stdout, stderr)
}

func (c hostCommand) doctor(args []string, stdout, stderr io.Writer) int {
	if stop, code := parseNoFlags("doctor", args, stderr); stop {
		return code
	}

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	report := doctor.Run(ctx, c.host)

	for _, check := range report.Checks {
		fmt.Fprintf(stdout, "%-4s  %-16s %s\n", check.Status, check.Name, check.Detail)
	}

	if report.Failed() {
		fmt.Fprintln(stderr, "omj-agent doctor: at least one check failed")

		return ExitError
	}

	return ExitOK
}
