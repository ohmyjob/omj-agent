#!/bin/sh
# Oh My Job Agent installer. Served as https://ohmyjob.com/install.sh; the source of truth is
# packaging/install.sh in github.com/ohmyjob/omj-agent.
set -eu

REPOSITORY="ohmyjob/omj-agent"
BINARY_PATH="/usr/local/bin/omj-agent"
UNIT_PATH="/etc/systemd/system/omj-agent.service"
CONFIG_DIR="/etc/ohmyjob"
STATE_DIR="/var/lib/ohmyjob"
DEFAULT_USER="ohmyjob"

server=""
token=""
name=""
user="$DEFAULT_USER"
version="latest"
insecure_http=0
no_enroll=0
uninstall=0
purge=0
base_url=""

usage() {
    cat <<'USAGE'
Usage: install.sh --server URL --token TOKEN [options]
       install.sh --no-enroll [options]
       install.sh --uninstall [--purge]

Installs the Oh My Job Agent, creates its service user, installs the systemd
unit and enrolls this machine with your Server.

Options:
  --server URL       Server URL, as shown on the Add Machine page
  --token TOKEN      One-time enrollment token from the Add Machine page
  --name NAME        Friendly name for this machine (defaults to the hostname)
  --user NAME        Run the Agent as this existing user (default: ohmyjob)
  --version VERSION  Agent release to install (default: latest)
  --insecure-http    Allow a plain http:// Server URL
  --no-enroll        Install without enrolling; run omj-agent enroll later
  --uninstall        Stop and remove the service and the binary
  --purge            With --uninstall, also remove /etc/ohmyjob and /var/lib/ohmyjob
  -h, --help         Show this help
USAGE
}

say() {
    printf '%s\n' "$1"
}

warn() {
    printf 'warning: %s\n' "$1" >&2
}

fail() {
    printf 'install.sh: %s\n' "$1" >&2
    exit "${2:-1}"
}

need_value() {
    [ "$#" -ge 2 ] || fail "$1 needs a value" 2
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --server) need_value "$@"; server="$2"; shift 2 ;;
        --server=*) server="${1#*=}"; shift ;;
        --token) need_value "$@"; token="$2"; shift 2 ;;
        --token=*) token="${1#*=}"; shift ;;
        --name) need_value "$@"; name="$2"; shift 2 ;;
        --name=*) name="${1#*=}"; shift ;;
        --user) need_value "$@"; user="$2"; shift 2 ;;
        --user=*) user="${1#*=}"; shift ;;
        --version) need_value "$@"; version="$2"; shift 2 ;;
        --version=*) version="${1#*=}"; shift ;;
        --base-url) need_value "$@"; base_url="$2"; shift 2 ;;
        --base-url=*) base_url="${1#*=}"; shift ;;
        --insecure-http) insecure_http=1; shift ;;
        --no-enroll) no_enroll=1; shift ;;
        --uninstall) uninstall=1; shift ;;
        --purge) purge=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) usage >&2; fail "unknown option $1" 2 ;;
    esac
done

[ "$(id -u)" -eq 0 ] || fail "run this script as root, for example: curl -fsSL https://ohmyjob.com/install.sh | sudo sh -s -- ..." 2

has_systemd() {
    command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]
}

if [ "$uninstall" -eq 1 ]; then
    if has_systemd; then
        systemctl disable --now omj-agent >/dev/null 2>&1 || true
    fi
    rm -f "$UNIT_PATH"
    if has_systemd; then
        systemctl daemon-reload
    fi
    rm -f "$BINARY_PATH"
    if [ "$purge" -eq 1 ]; then
        rm -rf "$CONFIG_DIR" "$STATE_DIR"
        say "Removed the omj-agent service, binary, $CONFIG_DIR and $STATE_DIR. The $DEFAULT_USER account was left in place."
    else
        say "Removed the omj-agent service and binary. Kept $CONFIG_DIR and $STATE_DIR; pass --purge to remove them too."
    fi
    exit 0
fi

if [ "$no_enroll" -eq 0 ]; then
    [ -n "$server" ] && [ -n "$token" ] || fail "pass --server and --token (both are on the Add Machine page), or --no-enroll to install without enrolling" 2
fi
if [ -n "$server" ]; then
    case "$server" in
        https://*) ;;
        http://*) [ "$insecure_http" -eq 1 ] || fail "$server is plain http; the credential would travel unencrypted. Pass --insecure-http to allow it" 2 ;;
        *) fail "--server must start with https:// (or http:// with --insecure-http)" 2 ;;
    esac
fi
[ -n "$user" ] || fail "--user needs a name" 2
if [ "$version" = latest ] && [ -n "$base_url" ]; then
    fail "--version is required together with --base-url" 2
fi

case "$(uname -s)" in
    Linux) ;;
    *) fail "unsupported operating system $(uname -s): the Agent runs on linux/amd64 and linux/arm64" ;;
esac
case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) fail "unsupported architecture $(uname -m): the Agent runs on linux/amd64 and linux/arm64" ;;
esac

if command -v curl >/dev/null 2>&1; then
    fetch() {
        curl -fsSL --retry 3 -o "$2" "$1"
    }
elif command -v wget >/dev/null 2>&1; then
    fetch() {
        wget -q -O "$2" "$1"
    }
else
    fail "curl or wget is required to download the Agent"
fi
for tool in sha256sum tar install; do
    command -v "$tool" >/dev/null 2>&1 || fail "$tool is required but not installed"
done

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

if [ "$version" = latest ]; then
    fetch "https://api.github.com/repos/$REPOSITORY/releases/latest" "$tmp/latest.json" || fail "could not look up the latest release"
    version="$(sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' "$tmp/latest.json" | head -n 1)"
    [ -n "$version" ] || fail "could not determine the latest release from GitHub"
fi
version="${version#v}"
asset="omj-agent_${version}_linux_${arch}.tar.gz"
if [ -n "$base_url" ]; then
    download_base="${base_url%/}"
else
    download_base="https://github.com/$REPOSITORY/releases/download/v${version}"
fi

installed_version=""
if [ -x "$BINARY_PATH" ]; then
    installed_version="$("$BINARY_PATH" version 2>/dev/null | awk 'NR == 1 { print $2 }')" || installed_version=""
fi

if [ "$user" = root ]; then
    warn "the Agent will run as root: every Job runs with full privileges and the Server marks this machine as privileged"
    group=root
elif id "$user" >/dev/null 2>&1; then
    group="$(id -gn "$user")"
else
    [ "$user" = "$DEFAULT_USER" ] || fail "user $user does not exist; create it first, or omit --user to use $DEFAULT_USER" 2
    group="$user"
fi

if [ "$installed_version" = "$version" ]; then
    say "omj-agent $version is already installed at $BINARY_PATH."
    upgraded=0
else
    say "Downloading omj-agent $version for linux/$arch..."
    fetch "$download_base/$asset" "$tmp/$asset" || fail "could not download $download_base/$asset"
    fetch "$download_base/SHA256SUMS" "$tmp/SHA256SUMS" || fail "could not download $download_base/SHA256SUMS"
    awk -v asset="$asset" '$2 == asset || $2 == "*" asset' "$tmp/SHA256SUMS" > "$tmp/SHA256SUMS.asset"
    [ -s "$tmp/SHA256SUMS.asset" ] || fail "SHA256SUMS does not list $asset; nothing was installed"
    (cd "$tmp" && sha256sum -c "$tmp/SHA256SUMS.asset" >/dev/null 2>&1) || fail "checksum mismatch for $asset; nothing was installed"
    mkdir "$tmp/extract"
    tar -xzf "$tmp/$asset" -C "$tmp/extract"
    [ -f "$tmp/extract/omj-agent" ] || fail "$asset does not contain the omj-agent binary"
    upgraded=1
fi

unit_source=""
for candidate in "$tmp/extract/packaging/systemd/omj-agent.service" "$tmp/extract/omj-agent.service"; do
    if [ -f "$candidate" ]; then
        unit_source="$candidate"
        break
    fi
done
if [ "$upgraded" -eq 1 ] && [ -z "$unit_source" ]; then
    fail "$asset does not contain omj-agent.service"
fi
if [ -z "$unit_source" ] && [ -f "$UNIT_PATH" ]; then
    unit_source="$UNIT_PATH"
fi
[ -n "$unit_source" ] || fail "$UNIT_PATH is missing; re-run with --version to reinstall the release"

if [ "$user" != root ] && ! id "$user" >/dev/null 2>&1; then
    nologin=/usr/sbin/nologin
    [ -x "$nologin" ] || nologin=/sbin/nologin
    [ -x "$nologin" ] || nologin=/bin/false
    if command -v useradd >/dev/null 2>&1; then
        useradd --system --user-group --home-dir "$STATE_DIR" --no-create-home --shell "$nologin" "$user"
    elif command -v adduser >/dev/null 2>&1; then
        addgroup -S "$user" 2>/dev/null || true
        adduser -S -D -H -h "$STATE_DIR" -s "$nologin" -G "$user" "$user"
    else
        fail "neither useradd nor adduser is available to create the $user account"
    fi
    say "Created the $user system user."
fi

install -d -m 0750 -o "$user" -g "$group" "$CONFIG_DIR"
install -d -m 0700 -o "$user" -g "$group" "$STATE_DIR"

if [ "$upgraded" -eq 1 ]; then
    install -m 0755 -o root -g root "$tmp/extract/omj-agent" "$BINARY_PATH"
    say "Installed $BINARY_PATH."
fi

sed -e "s/^User=.*/User=$user/" -e "s/^Group=.*/Group=$group/" "$unit_source" > "$tmp/omj-agent.service"
install -d -m 0755 -o root -g root "$(dirname "$UNIT_PATH")"
install -m 0644 -o root -g root "$tmp/omj-agent.service" "$UNIT_PATH"
say "Installed $UNIT_PATH running as $user."

enrolled=0
if [ "$no_enroll" -eq 0 ]; then
    set -- --server "$server" --token "$token" --user "$user"
    [ -z "$name" ] || set -- "$@" --name "$name"
    [ "$insecure_http" -eq 0 ] || set -- "$@" --insecure-http
    enroll_status=0
    "$BINARY_PATH" enroll "$@" || enroll_status=$?
    if [ "$enroll_status" -eq 0 ]; then
        enrolled=1
    else
        case "$enroll_status" in
            3) hint="This machine is already enrolled. Remove it in the Server UI and re-run with a fresh token, or run: omj-agent enroll --force ..." ;;
            4|5) hint="Generate a new token from Add Machine and run: sudo omj-agent enroll --server $server --token <token> --user $user" ;;
            6) hint="The Server does not accept this operating system." ;;
            7) hint="The Server rejected this Agent version; install the version it expects with --version." ;;
            8) hint="The Server is rate limiting enrollments; wait a minute and re-run." ;;
            9) hint="The Server could not be reached; check the URL, the network and the TLS certificate." ;;
            10) hint="Permission denied on $CONFIG_DIR." ;;
            *) hint="Enrollment failed." ;;
        esac
        fail "$hint The binary and service are installed; nothing else is needed before enrolling." "$enroll_status"
    fi
fi

configured=0
if [ -f "$CONFIG_DIR/agent.conf" ] && grep -q '^machine_id *=' "$CONFIG_DIR/agent.conf"; then
    configured=1
fi

if has_systemd; then
    systemctl daemon-reload
    if [ "$configured" -eq 1 ]; then
        systemctl enable --now omj-agent
        if [ "$upgraded" -eq 1 ]; then
            systemctl try-restart omj-agent
        fi
        say "The omj-agent service is enabled and running."
    else
        say "Enroll this machine, then start the service: sudo omj-agent enroll --server <url> --token <token> --user $user && sudo systemctl enable --now omj-agent"
    fi
else
    if [ "$configured" -eq 1 ]; then
        say "systemd is not running here; start the Agent yourself as $user: omj-agent run"
    else
        say "systemd is not running here; enroll with omj-agent enroll, then run the Agent as $user: omj-agent run"
    fi
fi

if [ "$enrolled" -eq 1 ]; then
    say "Machine enrolled. The Add Machine page will show it within a few seconds."
fi

if [ "$configured" -eq 1 ]; then
    say ""
    "$BINARY_PATH" doctor || true
fi
