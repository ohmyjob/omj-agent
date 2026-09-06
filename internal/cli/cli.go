// Package cli dispatches the omj-agent subcommands.
package cli

import (
	"fmt"
	"io"
)

const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

type command struct {
	name    string
	summary string
	run     func(args []string, stdout, stderr io.Writer) int
}

func commands() []command {
	return []command{
		{name: "enroll", summary: "Enroll this machine with a server", run: runEnroll},
		{name: "run", summary: "Run the agent in the foreground", run: runRun},
		{name: "status", summary: "Show the configuration, machine id and server reachability", run: runStatus},
		{name: "version", summary: "Print the agent and protocol versions", run: runVersion},
		{name: "doctor", summary: "Check the installation and exit 1 on any problem", run: runDoctor},
		{name: "discover", summary: "Print the scheduled work this machine already has, and send nothing", run: runDiscover},
	}
}

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)

		return ExitUsage
	}

	switch args[0] {
	case "help", "-h", "--help":
		usage(stdout)

		return ExitOK
	}

	for _, c := range commands() {
		if c.name == args[0] {
			return c.run(args[1:], stdout, stderr)
		}
	}

	fmt.Fprintf(stderr, "omj-agent: unknown command %q\n\n", args[0])
	usage(stderr)

	return ExitUsage
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: omj-agent <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")

	for _, c := range commands() {
		fmt.Fprintf(w, "  %-8s %s\n", c.name, c.summary)
	}
}
