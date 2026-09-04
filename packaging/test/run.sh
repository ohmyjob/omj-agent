#!/bin/sh
# Packages a release the way task 016 will, stands in for GitHub Releases with a local directory,
# and runs packaging/test/install-test.sh inside Debian and Fedora containers.
set -eu

root="$(cd "$(dirname "$0")/../.." && pwd)"
version="${VERSION:-0.0.0-test}"
release="$root/dist/release"
broken="$root/dist/broken"

case "$(docker info --format '{{.Architecture}}')" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) printf 'unsupported Docker architecture %s\n' "$(docker info --format '{{.Architecture}}')" >&2; exit 1 ;;
esac

if command -v sha256sum >/dev/null 2>&1; then
    checksum() { sha256sum "$@"; }
else
    checksum() { shasum -a 256 "$@"; }
fi

asset="omj-agent_${version}_linux_${arch}.tar.gz"
stage="$root/dist/stage"
rm -rf "$root/dist"
mkdir -p "$stage/packaging/systemd" "$release" "$broken"

package="github.com/ohmyjob/omj-agent/internal/version"
(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
    -ldflags "-s -w -X $package.Version=$version -X $package.Commit=test -X $package.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o "$stage/omj-agent" ./cmd/omj-agent)
cp "$root/LICENSE" "$root/README.md" "$stage/"
cp "$root/packaging/systemd/omj-agent.service" "$stage/packaging/systemd/"
COPYFILE_DISABLE=1 tar --no-xattrs -czf "$release/$asset" -C "$stage" omj-agent LICENSE README.md packaging/systemd/omj-agent.service
(cd "$release" && checksum "$asset" > SHA256SUMS)

cp "$release/$asset" "$broken/"
printf '%064d  %s\n' 0 "$asset" > "$broken/SHA256SUMS"

for image in debian:bookworm-slim fedora:41; do
    distro="${image%%:*}"
    docker run --rm \
        -v "$root:/work:ro" \
        -v "$release:/dist:ro" \
        -v "$broken:/dist-bad:ro" \
        "$image" sh /work/packaging/test/install-test.sh "$distro" "$version"
done
