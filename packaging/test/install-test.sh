#!/bin/sh
# Runs as root inside a fresh distribution container with the repository at /work, a release
# directory at /dist and a copy with a wrong checksum at /dist-bad. Exercises install.sh end to end.
set -eu

distro="$1"
version="$2"
installer="/work/packaging/install.sh"

case "$distro" in
    debian)
        apt-get update -qq >/dev/null
        apt-get install -y -qq --no-install-recommends curl ca-certificates >/dev/null
        ;;
    fedora)
        command -v tar >/dev/null 2>&1 || dnf install -y -q tar >/dev/null
        ;;
esac

fail() {
    printf 'FAIL (%s): %s\n' "$distro" "$1" >&2
    exit 1
}

assert_mode() {
    actual="$(stat -c '%a' "$1")"
    [ "$actual" = "$2" ] || fail "$1 has mode $actual, expected $2"
}

assert_owner() {
    actual="$(stat -c '%U:%G' "$1")"
    [ "$actual" = "$2" ] || fail "$1 is owned by $actual, expected $2"
}

assert_unit_line() {
    grep -qx "$1" /etc/systemd/system/omj-agent.service || fail "unit lacks the line $1"
}

# A wrong checksum must stop the installer before it touches the system.
if sh "$installer" --base-url file:///dist-bad --version "$version" --no-enroll > /tmp/bad.log 2>&1; then
    fail "a wrong checksum did not abort the installation"
fi
grep -q "checksum mismatch" /tmp/bad.log || fail "the checksum failure was not reported: $(cat /tmp/bad.log)"
[ ! -e /usr/local/bin/omj-agent ] || fail "the binary was installed despite the wrong checksum"
if id ohmyjob >/dev/null 2>&1; then
    fail "the ohmyjob user was created despite the wrong checksum"
fi

# A fresh installation without enrollment.
sh "$installer" --base-url file:///dist --version "$version" --no-enroll > /tmp/install.log 2>&1 || fail "installation failed: $(cat /tmp/install.log)"
assert_mode /usr/local/bin/omj-agent 755
assert_owner /usr/local/bin/omj-agent root:root
/usr/local/bin/omj-agent version | grep -q "^omj-agent $version " || fail "installed binary reports $(/usr/local/bin/omj-agent version)"
shell="$(getent passwd ohmyjob | cut -d: -f7)"
case "$shell" in
    */nologin|/bin/false) ;;
    *) fail "the ohmyjob user has a login shell: $shell" ;;
esac
[ "$(getent passwd ohmyjob | cut -d: -f6)" = /var/lib/ohmyjob ] || fail "the ohmyjob home is not /var/lib/ohmyjob"
assert_mode /etc/ohmyjob 750
assert_owner /etc/ohmyjob ohmyjob:ohmyjob
assert_mode /var/lib/ohmyjob 700
assert_owner /var/lib/ohmyjob ohmyjob:ohmyjob
assert_mode /etc/systemd/system/omj-agent.service 644
assert_unit_line "User=ohmyjob"
assert_unit_line "Group=ohmyjob"
assert_unit_line "ExecStart=/usr/local/bin/omj-agent run"
assert_unit_line "KillMode=control-group"
assert_unit_line "TimeoutStopSec=30"
grep -q "enroll" /tmp/install.log || fail "the installer did not explain how to enroll"

# A second run at the same version changes nothing.
before="$(stat -c '%Y' /usr/local/bin/omj-agent)"
sleep 1
sh "$installer" --base-url file:///dist --version "$version" --no-enroll > /tmp/again.log 2>&1 || fail "the second run failed: $(cat /tmp/again.log)"
grep -q "already installed" /tmp/again.log || fail "the second run did not report the existing installation"
[ "$(stat -c '%Y' /usr/local/bin/omj-agent)" = "$before" ] || fail "the second run replaced the binary"

# An existing user can own the installation instead.
nologin=/usr/sbin/nologin
[ -x "$nologin" ] || nologin=/sbin/nologin
useradd --system --user-group --home-dir /var/lib/ohmyjob --no-create-home --shell "$nologin" omjtest
sh "$installer" --base-url file:///dist --version "$version" --no-enroll --user omjtest > /tmp/user.log 2>&1 || fail "installing for another user failed: $(cat /tmp/user.log)"
assert_unit_line "User=omjtest"
assert_unit_line "Group=omjtest"
assert_owner /etc/ohmyjob omjtest:omjtest
assert_owner /var/lib/ohmyjob omjtest:omjtest

# An unknown user is refused before anything changes.
if sh "$installer" --base-url file:///dist --version "$version" --no-enroll --user nobody-here > /tmp/unknown.log 2>&1; then
    fail "an unknown user was accepted"
fi
grep -q "does not exist" /tmp/unknown.log || fail "the unknown user was not explained: $(cat /tmp/unknown.log)"

# Uninstall keeps the configuration unless purged.
sh "$installer" --uninstall > /tmp/uninstall.log 2>&1 || fail "uninstall failed: $(cat /tmp/uninstall.log)"
[ ! -e /usr/local/bin/omj-agent ] || fail "uninstall left the binary"
[ ! -e /etc/systemd/system/omj-agent.service ] || fail "uninstall left the unit"
[ -d /etc/ohmyjob ] || fail "uninstall removed /etc/ohmyjob without --purge"
sh "$installer" --uninstall --purge > /tmp/purge.log 2>&1 || fail "purge failed: $(cat /tmp/purge.log)"
[ ! -e /etc/ohmyjob ] || fail "purge left /etc/ohmyjob"
[ ! -e /var/lib/ohmyjob ] || fail "purge left /var/lib/ohmyjob"

printf 'PASS (%s)\n' "$distro"
