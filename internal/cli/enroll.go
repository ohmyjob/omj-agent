package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"

	"github.com/ohmyjob/omj-agent/internal/enroll"
)

// Exit codes of omj-agent enroll beyond the shared ones, so the installer
// can tell the outcomes apart.
const (
	ExitAlreadyEnrolled = 3
	ExitTokenInvalid    = 4
	ExitTokenExpired    = 5
	ExitUnsupportedOS   = 6
	ExitVersionRejected = 7
	ExitThrottled       = 8
	ExitUnreachable     = 9
	ExitPermission      = 10
)

type enrollCommand struct {
	enroll func(context.Context, enroll.Options) (enroll.Result, error)
}

func runEnroll(args []string, stdout, stderr io.Writer) int {
	return enrollCommand{enroll: enroll.Enroll}.run(args, stdout, stderr)
}

func (c enrollCommand) run(args []string, stdout, stderr io.Writer) int {
	var opts enroll.Options

	flags := flag.NewFlagSet("enroll", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.ServerURL, "server", "", "server URL, for example https://omj.example.com")
	flags.StringVar(&opts.Token, "token", "", "enrollment token shown by Add Machine")
	flags.StringVar(&opts.Name, "name", "", "friendly name for this machine (defaults to its hostname)")
	flags.BoolVar(&opts.InsecureHTTP, "insecure-http", false, "allow a plain http:// server URL")
	flags.StringVar(&opts.User, "user", "", "owner of the written files (ohmyjob when run as root, otherwise the current user)")
	flags.BoolVar(&opts.Force, "force", false, "replace an existing enrollment")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}

		return ExitUsage
	}

	if opts.ServerURL == "" || opts.Token == "" {
		fmt.Fprintln(stderr, "omj-agent enroll: --server and --token are required")
		flags.Usage()

		return ExitUsage
	}

	if opts.InsecureHTTP {
		fmt.Fprintln(stderr, "Warning: --insecure-http sends the credential and every command over plain HTTP; use it only on a network you trust.")
	}

	opts.Logger = slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	result, err := c.enroll(ctx, opts)
	if err != nil {
		fmt.Fprintf(stderr, "omj-agent enroll: %v\n", err)

		return exitCodeFor(err)
	}

	fmt.Fprintf(stdout, "Enrolled as machine %s.\n", result.MachineID)
	fmt.Fprintf(stdout, "Wrote %s and %s, owned by %s.\n", result.ConfigFile, result.CredentialFile, result.Owner)
	fmt.Fprintf(stdout, "Next: %s\n", result.NextStep)

	return ExitOK
}

func exitCodeFor(err error) int {
	var enrollErr *enroll.Error
	if !errors.As(err, &enrollErr) {
		return ExitError
	}

	switch enrollErr.Reason {
	case enroll.ReasonInvalidInput:
		return ExitUsage
	case enroll.ReasonAlreadyEnrolled:
		return ExitAlreadyEnrolled
	case enroll.ReasonTokenInvalid:
		return ExitTokenInvalid
	case enroll.ReasonTokenExpired:
		return ExitTokenExpired
	case enroll.ReasonUnsupportedOS:
		return ExitUnsupportedOS
	case enroll.ReasonVersionRejected:
		return ExitVersionRejected
	case enroll.ReasonThrottled:
		return ExitThrottled
	case enroll.ReasonUnreachable:
		return ExitUnreachable
	case enroll.ReasonPermission:
		return ExitPermission
	case enroll.ReasonUnknown:
		return ExitError
	default:
		return ExitError
	}
}
