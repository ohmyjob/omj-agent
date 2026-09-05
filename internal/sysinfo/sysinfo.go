// Package sysinfo collects the Machine metadata the Server displays.
package sysinfo

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"
)

const (
	MaxReportedIPs = 16

	// UnknownUID is reported when the platform has no numeric user id, because 0 would mean root.
	UnknownUID = -1

	defaultOSReleasePath = "/etc/os-release"
)

type Info struct {
	Hostname      string
	OS            string
	OSVersion     string
	Arch          string
	KernelVersion string
	ReportedIPs   []string
	AgentUser     string
	AgentUID      int
}

// Collector reads the local host when zero-valued.
type Collector struct {
	OSReleasePath string
	Logger        *slog.Logger
}

func Collect(ctx context.Context) (Info, error) {
	return Collector{}.Collect(ctx)
}

// Collect never fails on a missing field: the field stays empty and the reason goes to the debug log.
func (c Collector) Collect(ctx context.Context) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}

	logger := c.Logger
	if logger == nil {
		logger = slog.Default()
	}

	sysname, release, err := uname()
	if err != nil {
		logger.Debug("kernel version unavailable", "error", err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		logger.Debug("hostname unavailable", "error", err)
	}

	agentUser, agentUID := currentUser(logger)

	return Info{
		Hostname:      hostname,
		OS:            runtime.GOOS,
		OSVersion:     c.osVersion(logger, sysname, release),
		Arch:          runtime.GOARCH,
		KernelVersion: release,
		ReportedIPs:   selectIPs(hostInterfaces(logger)),
		AgentUser:     agentUser,
		AgentUID:      agentUID,
	}, nil
}

func (c Collector) osVersion(logger *slog.Logger, sysname, release string) string {
	path := c.OSReleasePath
	if path == "" {
		path = defaultOSReleasePath
	}

	name, err := prettyName(path)
	if err != nil {
		logger.Debug("os-release unavailable", "path", path, "error", err)
	}

	if name != "" {
		return name
	}

	return strings.TrimSpace(sysname + " " + release)
}

func prettyName(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // The path is /etc/os-release or a test sample, never input.
	if err != nil {
		return "", err
	}

	return parseOSRelease(string(data))["PRETTY_NAME"], nil
}

// parseOSRelease reads the shell-compatible KEY=value format of os-release(5).
func parseOSRelease(data string) map[string]string {
	values := make(map[string]string)

	for line := range strings.Lines(data) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}

		values[strings.TrimSpace(key)] = unquote(strings.TrimSpace(value))
	}

	return values
}

func unquote(value string) string {
	if len(value) < 2 {
		return value
	}

	quote := value[0]
	if (quote != '"' && quote != '\'') || value[len(value)-1] != quote {
		return value
	}

	value = value[1 : len(value)-1]
	if quote == '\'' {
		return value
	}

	return strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\$`, `$`, "\\`", "`").Replace(value)
}

type netInterface struct {
	Flags net.Flags
	Addrs []net.Addr
}

func hostInterfaces(logger *slog.Logger) []netInterface {
	ifaces, err := net.Interfaces()
	if err != nil {
		logger.Debug("network interfaces unavailable", "error", err)

		return nil
	}

	result := make([]netInterface, 0, len(ifaces))

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			logger.Debug("interface addresses unavailable", "interface", iface.Name, "error", err)

			continue
		}

		result = append(result, netInterface{Flags: iface.Flags, Addrs: addrs})
	}

	return result
}

// selectIPs keeps the addresses a person would use to reach the Machine: global unicast only,
// IPv4 before IPv6, each once, capped at MaxReportedIPs.
func selectIPs(ifaces []netInterface) []string {
	var v4, v6 []string

	seen := make(map[string]bool)

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		for _, addr := range iface.Addrs {
			ip := addrIP(addr)
			if ip == nil || !ip.IsGlobalUnicast() {
				continue
			}

			text := ip.String()
			if seen[text] {
				continue
			}

			seen[text] = true

			if ip.To4() != nil {
				v4 = append(v4, text)
			} else {
				v6 = append(v6, text)
			}
		}
	}

	ips := make([]string, 0, len(v4)+len(v6))
	ips = append(ips, v4...)
	ips = append(ips, v6...)

	if len(ips) > MaxReportedIPs {
		ips = ips[:MaxReportedIPs]
	}

	return ips
}

func addrIP(addr net.Addr) net.IP {
	switch a := addr.(type) {
	case *net.IPNet:
		return a.IP
	case *net.IPAddr:
		return a.IP
	default:
		return nil
	}
}

func currentUser(logger *slog.Logger) (string, int) {
	u, err := user.Current()
	if err != nil {
		logger.Debug("agent user unavailable", "error", err)

		return "", UnknownUID
	}

	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		logger.Debug("agent uid is not numeric", "uid", u.Uid)

		return u.Username, UnknownUID
	}

	return u.Username, uid
}
