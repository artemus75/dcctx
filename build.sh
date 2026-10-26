#!/usr/bin/env bash
# Cross-compile dcctx into dist/ and write checksums.
# Usage: ./build.sh [version]   (default: output of `git describe`, else "dev")
set -euo pipefail
cd "$(dirname "$0")"

VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
mkdir -p dist
rm -f dist/dcctx-* dist/SHA256SUMS

for target in linux/amd64 linux/arm64 darwin/arm64 darwin/amd64 windows/amd64; do
  os=${target%/*}; arch=${target#*/}
  ext=""; [ "$os" = windows ] && ext=".exe"
  echo "→ $os/$arch ($VERSION)"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "dist/dcctx-$os-$arch$ext" .
done

(cd dist && if command -v sha256sum >/dev/null; then sha256sum dcctx-*; else shasum -a 256 dcctx-*; fi > SHA256SUMS)
echo "Done: dist/"
