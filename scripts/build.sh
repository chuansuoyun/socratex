#!/usr/bin/env bash
# Cross-compile socratex for common platforms.
# Usage:
#   ./scripts/build.sh           # all platforms
#   ./scripts/build.sh linux-amd64 windows-amd64 darwin-arm64
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

APP="socratex"
CMD="./cmd/socratex"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 2.0.0)}"
LDFLAGS="-s -w -X main.version=${VERSION}"
DIST="${DIST:-dist}"

declare -A TARGETS=(
  [linux-amd64]="linux/amd64"
  [linux-arm64]="linux/arm64"
  [windows-amd64]="windows/amd64"
  [windows-386]="windows/386"
  [darwin-arm64]="darwin/arm64"
  [darwin-amd64]="darwin/amd64"
)

build_one() {
  local name="$1"
  local pair="${TARGETS[$name]:-}"
  if [[ -z "$pair" ]]; then
    echo "unknown target: $name" >&2
    echo "available: ${!TARGETS[*]}" >&2
    exit 1
  fi
  local goos="${pair%%/*}"
  local goarch="${pair##*/}"
  local out="${DIST}/${APP}-${name}"
  if [[ "$goos" == "windows" ]]; then
    out="${out}.exe"
  fi
  echo "==> ${name}  (GOOS=${goos} GOARCH=${goarch})"
  mkdir -p "$DIST"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -ldflags="$LDFLAGS" -o "$out" "$CMD"
  ls -lh "$out"
}

main() {
  local list=("$@")
  if [[ ${#list[@]} -eq 0 ]]; then
    list=(linux-amd64 linux-arm64 windows-amd64 windows-386 darwin-arm64 darwin-amd64)
  fi
  echo "socratex ${VERSION}  cross-build"
  echo "output: ${DIST}/"
  echo ""
  for t in "${list[@]}"; do
    build_one "$t"
  done
  echo ""
  echo "done."
  ls -lh "$DIST"/
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$DIST" && sha256sum * > SHA256SUMS)
  elif command -v shasum >/dev/null 2>&1; then
    (cd "$DIST" && shasum -a 256 * > SHA256SUMS)
  fi
  if [[ -f "$DIST/SHA256SUMS" ]]; then
    echo ""
    cat "$DIST/SHA256SUMS"
  fi
}

main "$@"
