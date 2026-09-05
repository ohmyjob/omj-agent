// Package doctor provides installation checks shared by status and doctor.
package doctor

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"time"

	"github.com/ohmyjob/omj-agent/internal/client"
	"github.com/ohmyjob/omj-agent/internal/config"
	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/state"
)

type Pinger interface {
	Ping(ctx context.Context) (protocol.PingResponse, error)
	ServerVersion() string
}

type Systemctl interface {
	Available() bool
	Run(ctx context.Context, args ...string) (string, error)
}

// Host isolates checks from root access, the Server, and systemd.
type Host struct {
	Paths          config.Paths
	UID            int
	Username       string
	Now            func() time.Time
	LoadConfig     func(config.Paths) (config.Config, error)
	LoadCredential func(config.Paths) (config.Credential, error)
	LoadState      func(path string) (*state.Store, error)
	LookupUser     func(name string) (uid int, err error)
	Dial           func(config.Config, config.Credential) (Pinger, error)
	Systemctl      Systemctl
}

func DefaultHost() Host {
	h := Host{Paths: config.DefaultPaths(), UID: os.Getuid()}

	if current, err := user.Current(); err == nil {
		h.Username = current.Username
	}

	return h.WithDefaults()
}

func (h Host) WithDefaults() Host {
	if h.Now == nil {
		h.Now = time.Now
	}

	if h.LoadConfig == nil {
		h.LoadConfig = config.Load
	}

	if h.LoadCredential == nil {
		h.LoadCredential = config.LoadCredential
	}

	if h.LoadState == nil {
		h.LoadState = state.Load
	}

	if h.LookupUser == nil {
		h.LookupUser = config.LookupUser
	}

	if h.Dial == nil {
		h.Dial = dial
	}

	if h.Systemctl == nil {
		h.Systemctl = DefaultSystemctl()
	}

	return h
}

func dial(cfg config.Config, credential config.Credential) (Pinger, error) {
	return client.New(client.Options{
		ServerURL:    cfg.ServerURL,
		Credential:   credential,
		InsecureHTTP: cfg.InsecureHTTP,
		Logger:       slog.New(slog.DiscardHandler),
	})
}

type Probe struct {
	Response      protocol.PingResponse
	ServerVersion string
	Skew          time.Duration
	Err           error
}

func (h Host) Probe(ctx context.Context, cfg config.Config, credential config.Credential) Probe {
	pinger, err := h.Dial(cfg, credential)
	if err != nil {
		return Probe{Err: err}
	}

	response, err := pinger.Ping(ctx)

	probe := Probe{Response: response, ServerVersion: pinger.ServerVersion(), Err: err}
	if err == nil {
		probe.Skew = response.ServerTime.Sub(h.Now())
	}

	return probe
}

type systemctl struct {
	path string
}

type noSystemctl struct{}

// DefaultSystemctl shells out to systemctl when it is on the PATH and reports
// systemd as absent otherwise, so macOS and containers get a plain answer.
func DefaultSystemctl() Systemctl {
	path, err := exec.LookPath("systemctl")
	if err != nil {
		return noSystemctl{}
	}

	return systemctl{path: path}
}

func (s systemctl) Available() bool { return true }

// Run returns systemctl's output even when it exits non-zero, because
// is-active and is-enabled report their answer that way.
func (s systemctl) Run(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, s.path, args...).Output()

	return strings.TrimSpace(string(out)), err
}

func (noSystemctl) Available() bool { return false }

func (noSystemctl) Run(context.Context, ...string) (string, error) { return "", nil }
