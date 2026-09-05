// Package enroll exchanges a one-time token for a Machine configuration and credential.
package enroll

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/ohmyjob/omj-agent/internal/client"
	"github.com/ohmyjob/omj-agent/internal/config"
	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/sysinfo"
)

const (
	TokenPrefix        = "omj_enroll_"
	DefaultOwner       = "ohmyjob"
	DefaultServiceUnit = "/etc/systemd/system/omj-agent.service"

	configDirMode os.FileMode = 0o750
)

// Reason classifies a failed enrollment so the CLI can pick an exit code.
type Reason int

const (
	ReasonUnknown Reason = iota
	ReasonInvalidInput
	ReasonAlreadyEnrolled
	ReasonTokenInvalid
	ReasonTokenExpired
	ReasonUnsupportedOS
	ReasonVersionRejected
	ReasonThrottled
	ReasonUnreachable
	ReasonPermission
)

type Error struct {
	Reason  Reason
	Message string
	Err     error
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Err }

type Options struct {
	ServerURL    string
	Token        string
	Name         string
	InsecureHTTP bool
	User         string
	Force        bool

	Paths      config.Paths
	Collect    func(context.Context) (sysinfo.Info, error)
	HTTPClient *http.Client
	System     System
	Logger     *slog.Logger
}

// System is what enrollment needs to know about the host it runs on; tests
// inject it so nothing here needs root.
type System struct {
	UID         int
	Username    string
	LookupUser  func(name string) (uid, gid int, err error)
	Chown       func(path string, uid, gid int) error
	ServiceUnit string
}

func DefaultSystem() System {
	sys := System{
		UID:         os.Getuid(),
		LookupUser:  lookupUser,
		Chown:       os.Chown,
		ServiceUnit: DefaultServiceUnit,
	}

	if current, err := user.Current(); err == nil {
		sys.Username = current.Username
	}

	return sys
}

type Result struct {
	MachineID      string
	ConfigFile     string
	CredentialFile string
	Owner          string
	NextStep       string
}

func Enroll(ctx context.Context, opts Options) (Result, error) {
	opts = withDefaults(opts)

	if err := validate(opts); err != nil {
		return Result{}, err
	}

	owner, err := resolveOwner(opts)
	if err != nil {
		return Result{}, err
	}

	cfg, err := existingConfig(opts)
	if err != nil {
		return Result{}, err
	}

	if cfg.MachineID != "" && !opts.Force {
		return Result{}, &Error{
			Reason:  ReasonAlreadyEnrolled,
			Message: fmt.Sprintf("this machine is already enrolled as %s; remove it in the Oh My Job UI first, then run enroll again with --force", cfg.MachineID),
		}
	}

	// A token is single-use, so the directory is checked before it is spent.
	if err := ensureWritable(opts.Paths.ConfigDir); err != nil {
		return Result{}, classifyFilesystem(opts.Paths.ConfigDir, err)
	}

	api, err := client.New(client.Options{
		ServerURL:    opts.ServerURL,
		InsecureHTTP: opts.InsecureHTTP,
		HTTPClient:   opts.HTTPClient,
		Logger:       opts.Logger,
	})
	if err != nil {
		return Result{}, &Error{Reason: ReasonInvalidInput, Message: err.Error(), Err: err}
	}

	info, err := opts.Collect(ctx)
	if err != nil {
		return Result{}, &Error{Reason: ReasonUnknown, Message: "collect machine information: " + err.Error(), Err: err}
	}

	response, err := api.Enroll(ctx, info.EnrollRequest(opts.Token, opts.Name, opts.InsecureHTTP))
	if err != nil {
		return Result{}, classifyServer(opts.ServerURL, err)
	}

	credential, err := config.NewCredential(response.Credential)
	if err != nil {
		return Result{}, &Error{Reason: ReasonUnknown, Message: "the server answered with an unusable credential: " + err.Error(), Err: err}
	}

	cfg.ServerURL = opts.ServerURL
	cfg.MachineID = response.MachineID
	cfg.InsecureHTTP = opts.InsecureHTTP

	if err := config.Save(opts.Paths, cfg); err != nil {
		return Result{}, classifyFilesystem(opts.Paths.ConfigFile, err)
	}

	if err := config.SaveCredential(opts.Paths, credential); err != nil {
		return Result{}, classifyFilesystem(opts.Paths.CredentialFile, err)
	}

	if err := owner.apply(opts.System, opts.Paths.ConfigFile, opts.Paths.CredentialFile); err != nil {
		return Result{}, err
	}

	return Result{
		MachineID:      response.MachineID,
		ConfigFile:     opts.Paths.ConfigFile,
		CredentialFile: opts.Paths.CredentialFile,
		Owner:          owner.name,
		NextStep:       nextStep(opts.System.ServiceUnit),
	}, nil
}

func withDefaults(opts Options) Options {
	if opts.Paths == (config.Paths{}) {
		opts.Paths = config.DefaultPaths()
	}

	if opts.Collect == nil {
		opts.Collect = sysinfo.Collect
	}

	if opts.System.LookupUser == nil && opts.System.Chown == nil {
		opts.System = DefaultSystem()
	}

	if opts.System.ServiceUnit == "" {
		opts.System.ServiceUnit = DefaultServiceUnit
	}

	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	return opts
}

func validate(opts Options) error {
	if opts.ServerURL == "" {
		return &Error{Reason: ReasonInvalidInput, Message: "a server URL is required"}
	}

	if !strings.HasPrefix(opts.Token, TokenPrefix) || len(opts.Token) == len(TokenPrefix) {
		return &Error{Reason: ReasonInvalidInput, Message: fmt.Sprintf("the token should start with %q, exactly as Add Machine shows it", TokenPrefix)}
	}

	return nil
}

type owner struct {
	name   string
	uid    int
	gid    int
	change bool
}

// resolveOwner decides who owns the written files: root hands them to the
// service user, anyone else keeps them, and only root may name someone else.
func resolveOwner(opts Options) (owner, error) {
	sys := opts.System
	root := sys.UID == 0

	if opts.User == "" {
		if root {
			if uid, gid, err := sys.LookupUser(DefaultOwner); err == nil {
				return owner{name: DefaultOwner, uid: uid, gid: gid, change: true}, nil
			}
		}

		return owner{name: sys.Username}, nil
	}

	if opts.User == sys.Username {
		return owner{name: opts.User}, nil
	}

	if !root {
		return owner{}, &Error{Reason: ReasonPermission, Message: fmt.Sprintf("giving the files to user %q needs root; run enroll with sudo", opts.User)}
	}

	uid, gid, err := sys.LookupUser(opts.User)
	if err != nil {
		return owner{}, &Error{Reason: ReasonInvalidInput, Message: fmt.Sprintf("user %q does not exist on this machine", opts.User), Err: err}
	}

	return owner{name: opts.User, uid: uid, gid: gid, change: true}, nil
}

func (o owner) apply(sys System, paths ...string) error {
	if !o.change {
		return nil
	}

	for _, path := range paths {
		if err := sys.Chown(path, o.uid, o.gid); err != nil {
			return classifyFilesystem(path, fmt.Errorf("give %s to %s: %w", path, o.name, err))
		}
	}

	return nil
}

// existingConfig keeps whatever the file already holds (log level, limits)
// so enrolling only fills in the server, the machine id and the scheme.
func existingConfig(opts Options) (config.Config, error) {
	data, err := os.ReadFile(opts.Paths.ConfigFile)
	if errors.Is(err, fs.ErrNotExist) {
		return config.Default(), nil
	}

	if err != nil {
		return config.Config{}, classifyFilesystem(opts.Paths.ConfigFile, err)
	}

	cfg, err := config.Parse(bytes.NewReader(data))
	if err == nil {
		return cfg, nil
	}

	if opts.Force {
		opts.Logger.Warn("replacing an unreadable configuration file", "path", opts.Paths.ConfigFile, "error", err)

		return config.Default(), nil
	}

	return config.Config{}, &Error{
		Reason:  ReasonUnknown,
		Message: fmt.Sprintf("%s could not be read (%v); fix it or run enroll again with --force to replace it", opts.Paths.ConfigFile, err),
		Err:     err,
	}
}

func ensureWritable(dir string) error {
	if err := os.MkdirAll(dir, configDirMode); err != nil {
		return err
	}

	probe, err := os.CreateTemp(dir, ".enroll-*")
	if err != nil {
		return err
	}

	name := probe.Name()

	if err := probe.Close(); err != nil {
		return err
	}

	return os.Remove(name)
}

func nextStep(serviceUnit string) string {
	if _, err := os.Stat(serviceUnit); err == nil {
		return "systemctl enable --now omj-agent"
	}

	return "omj-agent run"
}

func classifyFilesystem(path string, err error) *Error {
	if errors.Is(err, fs.ErrPermission) {
		return &Error{Reason: ReasonPermission, Message: fmt.Sprintf("cannot write %s: permission denied; run enroll with sudo", path), Err: err}
	}

	return &Error{Reason: ReasonUnknown, Message: err.Error(), Err: err}
}

func classifyServer(serverURL string, err error) *Error {
	var apiErr *client.APIError

	if errors.As(err, &apiErr) {
		return classifyAPIError(apiErr, err)
	}

	var certErr *tls.CertificateVerificationError

	if errors.As(err, &certErr) {
		return &Error{
			Reason:  ReasonUnreachable,
			Message: fmt.Sprintf("could not verify the TLS certificate of %s: %v; if the server uses its own certificate authority, install that authority on this machine first", serverURL, certErr),
			Err:     err,
		}
	}

	var netErr net.Error

	if errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded) {
		return &Error{Reason: ReasonUnreachable, Message: fmt.Sprintf("could not reach %s: %v", serverURL, err), Err: err}
	}

	return &Error{Reason: ReasonUnknown, Message: "enrollment failed: " + err.Error(), Err: err}
}

func classifyAPIError(apiErr *client.APIError, err error) *Error {
	switch {
	case apiErr.Status == http.StatusUnauthorized:
		return &Error{Reason: ReasonTokenInvalid, Message: "the server did not accept the enrollment token; it may have been used already or mistyped, so generate a new one with Add Machine", Err: err}
	case apiErr.Status == http.StatusGone:
		return &Error{Reason: ReasonTokenExpired, Message: "the enrollment token has expired; generate a new one with Add Machine", Err: err}
	case apiErr.Code == protocol.ErrUnsupportedOS:
		return &Error{Reason: ReasonUnsupportedOS, Message: "the server does not support this operating system: " + apiErr.Message, Err: err}
	case apiErr.Status == http.StatusUpgradeRequired:
		return &Error{Reason: ReasonVersionRejected, Message: versionRejectedMessage(apiErr), Err: err}
	case apiErr.Status == http.StatusTooManyRequests:
		return &Error{Reason: ReasonThrottled, Message: throttledMessage(apiErr), Err: err}
	default:
		return &Error{Reason: ReasonUnknown, Message: "the server refused the enrollment: " + apiErr.Error(), Err: err}
	}
}

func versionRejectedMessage(apiErr *client.APIError) string {
	message := "the server rejected this agent: " + apiErr.Message

	if len(apiErr.SupportedProtocolVersions) > 0 {
		versions := make([]string, len(apiErr.SupportedProtocolVersions))
		for i, v := range apiErr.SupportedProtocolVersions {
			versions[i] = strconv.Itoa(v)
		}

		message += fmt.Sprintf(" (this agent speaks protocol %d, the server supports %s)", protocol.ProtocolVersion, strings.Join(versions, ", "))
	}

	if apiErr.MinAgentVersion != "" {
		message += "; the server requires agent " + apiErr.MinAgentVersion + " or newer"
	}

	return message
}

func throttledMessage(apiErr *client.APIError) string {
	if apiErr.RetryAfter > 0 {
		return fmt.Sprintf("the server is limiting enrollment attempts; try again in %s", apiErr.RetryAfter.Round(time.Second))
	}

	return "the server is limiting enrollment attempts; wait a minute and try again"
}

func lookupUser(name string) (uid, gid int, err error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, err
	}

	uid, err = strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, fmt.Errorf("uid of %s: %w", name, err)
	}

	gid, err = strconv.Atoi(u.Gid)
	if err != nil {
		return 0, 0, fmt.Errorf("gid of %s: %w", name, err)
	}

	return uid, gid, nil
}
