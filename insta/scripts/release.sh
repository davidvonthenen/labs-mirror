#!/usr/bin/env bash
set -euo pipefail

insta_version="${1:-v0.0.1}"
if [[ ! "$insta_version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]]; then
    echo "Usage: scripts/release.sh vMAJOR.MINOR.PATCH[-suffix]" >&2
    exit 2
fi

insta_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$insta_root"
mkdir -p bin dist

for insta_os in linux darwin; do
    for insta_arch in amd64 arm64; do
        insta_target="${insta_os}-${insta_arch}"
        insta_binary="bin/${insta_target}/insta"
        mkdir -p "$(dirname "$insta_binary")"
        CGO_ENABLED=0 GOOS="$insta_os" GOARCH="$insta_arch" \
            go build -buildvcs=false -trimpath -ldflags "-s -w -X github.com/instaclustr/labs/insta/internal/cli.Version=${insta_version#v}" \
            -o "$insta_binary" ./cmd/insta
        tar -czf "dist/insta_${insta_version#v}_${insta_os}_${insta_arch}.tar.gz" \
            -C "bin/${insta_target}" insta -C "$insta_root" LICENSE INSTALL.md
    done
done

if command -v sha256sum >/dev/null 2>&1; then
    sha256sum bin/*/insta > ./bin/SHA256SUMS
    (cd dist && sha256sum ./*.tar.gz > SHA256SUMS)
else
    shasum -a 256 bin/*/insta > ./bin/SHA256SUMS
    (cd dist && shasum -a 256 ./*.tar.gz > SHA256SUMS)
fi

echo "Release archives and checksums are in $insta_root/dist"
