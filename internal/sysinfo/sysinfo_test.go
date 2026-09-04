package sysinfo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestPrettyName(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string
	}{
		{name: "debian", file: "os-release-debian", want: "Debian GNU/Linux 12 (bookworm)"},
		{name: "ubuntu", file: "os-release-ubuntu", want: "Ubuntu 24.04.1 LTS"},
		{name: "fedora", file: "os-release-fedora", want: "Fedora Linux 40 (Server Edition)"},
		{name: "alpine", file: "os-release-alpine", want: "Alpine Linux v3.20"},
		{name: "arch", file: "os-release-arch", want: "Arch Linux"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := prettyName(filepath.Join("testdata", tt.file))
			if err != nil {
				t.Fatalf("prettyName() error = %v", err)
			}

			if got != tt.want {
				t.Errorf("prettyName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseOSRelease(t *testing.T) {
	tests := []struct {
		name string
		data string
		want map[string]string
	}{
		{name: "unquoted value", data: "ID=debian\n", want: map[string]string{"ID": "debian"}},
		{name: "single quoted value", data: "NAME='Arch Linux'", want: map[string]string{"NAME": "Arch Linux"}},
		{name: "double quoted value with escapes", data: `PRETTY_NAME="Foo \"Bar\" \$1 \\ done"`, want: map[string]string{"PRETTY_NAME": `Foo "Bar" $1 \ done`}},
		{name: "comments and blank lines", data: "# comment\n\nID=alpine\n   \n", want: map[string]string{"ID": "alpine"}},
		{name: "lines without a separator", data: "garbage\nID=arch", want: map[string]string{"ID": "arch"}},
		{name: "surrounding whitespace", data: "  ID = fedora  ", want: map[string]string{"ID": "fedora"}},
		{name: "unbalanced quote is kept", data: `NAME="Foo`, want: map[string]string{"NAME": `"Foo`}},
		{name: "empty value", data: `VERSION_CODENAME=""`, want: map[string]string{"VERSION_CODENAME": ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseOSRelease(tt.data); !maps.Equal(got, tt.want) {
				t.Errorf("parseOSRelease() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSelectIPs(t *testing.T) {
	up := net.FlagUp

	var many []net.Addr
	for i := range MaxReportedIPs + 4 {
		many = append(many, cidr(t, fmt.Sprintf("10.0.%d.1/24", i)))
	}

	tests := []struct {
		name   string
		ifaces []netInterface
		want   []string
	}{
		{
			name:   "loopback interface is skipped",
			ifaces: []netInterface{{Flags: up | net.FlagLoopback, Addrs: []net.Addr{cidr(t, "127.0.0.1/8"), cidr(t, "::1/128")}}},
			want:   []string{},
		},
		{
			name:   "down interface is skipped",
			ifaces: []netInterface{{Flags: 0, Addrs: []net.Addr{cidr(t, "10.0.0.5/24")}}},
			want:   []string{},
		},
		{
			name:   "link local and multicast addresses are skipped",
			ifaces: []netInterface{{Flags: up, Addrs: []net.Addr{cidr(t, "169.254.1.1/16"), cidr(t, "fe80::1/64"), cidr(t, "224.0.0.1/4")}}},
			want:   []string{},
		},
		{
			name: "ipv4 before ipv6 across interfaces",
			ifaces: []netInterface{
				{Flags: up, Addrs: []net.Addr{cidr(t, "fd00::10/64"), cidr(t, "192.168.1.20/24")}},
				{Flags: up, Addrs: []net.Addr{cidr(t, "10.0.0.5/24"), cidr(t, "2001:db8::1/64")}},
			},
			want: []string{"192.168.1.20", "10.0.0.5", "fd00::10", "2001:db8::1"},
		},
		{
			name:   "duplicates are reported once",
			ifaces: []netInterface{{Flags: up, Addrs: []net.Addr{cidr(t, "10.0.0.5/24")}}, {Flags: up, Addrs: []net.Addr{cidr(t, "10.0.0.5/16")}}},
			want:   []string{"10.0.0.5"},
		},
		{
			name:   "addresses without a mask",
			ifaces: []netInterface{{Flags: up, Addrs: []net.Addr{&net.IPAddr{IP: net.ParseIP("10.1.1.1")}}}},
			want:   []string{"10.1.1.1"},
		},
		{
			name:   "other address types are ignored",
			ifaces: []netInterface{{Flags: up, Addrs: []net.Addr{&net.UnixAddr{Name: "/run/omj.sock", Net: "unix"}}}},
			want:   []string{},
		},
		{
			name:   "capped at sixteen",
			ifaces: []netInterface{{Flags: up, Addrs: many}},
			want:   []string{"10.0.0.1", "10.0.1.1", "10.0.2.1", "10.0.3.1", "10.0.4.1", "10.0.5.1", "10.0.6.1", "10.0.7.1", "10.0.8.1", "10.0.9.1", "10.0.10.1", "10.0.11.1", "10.0.12.1", "10.0.13.1", "10.0.14.1", "10.0.15.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectIPs(tt.ifaces); !slices.Equal(got, tt.want) {
				t.Errorf("selectIPs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCollect(t *testing.T) {
	info, err := Collector{OSReleasePath: filepath.Join("testdata", "os-release-debian"), Logger: debugLogger(&bytes.Buffer{})}.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	hostname, _ := os.Hostname()
	current, _ := user.Current()
	uid, _ := strconv.Atoi(current.Uid)

	if info.Hostname != hostname {
		t.Errorf("Hostname = %q, want %q", info.Hostname, hostname)
	}

	if info.OS != runtime.GOOS || info.Arch != runtime.GOARCH {
		t.Errorf("OS/Arch = %q/%q, want %q/%q", info.OS, info.Arch, runtime.GOOS, runtime.GOARCH)
	}

	if info.OSVersion != "Debian GNU/Linux 12 (bookworm)" {
		t.Errorf("OSVersion = %q, want the sample's PRETTY_NAME", info.OSVersion)
	}

	if info.AgentUser != current.Username || info.AgentUID != uid {
		t.Errorf("agent user = %q/%d, want %q/%d", info.AgentUser, info.AgentUID, current.Username, uid)
	}

	if info.ReportedIPs == nil {
		t.Error("ReportedIPs = nil, want an empty list at least")
	}

	if runtime.GOOS == "linux" && info.KernelVersion == "" {
		t.Error("KernelVersion is empty on Linux")
	}
}

func TestCollectFallsBackWhenOSReleaseIsMissing(t *testing.T) {
	var log bytes.Buffer

	info, err := Collector{OSReleasePath: filepath.Join(t.TempDir(), "missing"), Logger: debugLogger(&log)}.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if !strings.Contains(log.String(), "os-release unavailable") {
		t.Errorf("debug log = %q, want it to mention os-release", log.String())
	}

	want := ""
	if runtime.GOOS == "linux" {
		want = "Linux " + info.KernelVersion
	}

	if info.OSVersion != want {
		t.Errorf("OSVersion = %q, want %q", info.OSVersion, want)
	}
}

func TestCollectHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Collect(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Collect() error = %v, want %v", err, context.Canceled)
	}
}

func cidr(t *testing.T, text string) net.Addr {
	t.Helper()

	ip, network, err := net.ParseCIDR(text)
	if err != nil {
		t.Fatalf("ParseCIDR(%q) error = %v", text, err)
	}

	network.IP = ip

	return network
}

func debugLogger(w *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
