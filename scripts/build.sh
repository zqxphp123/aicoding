#!/usr/bin/env bash
# 交叉编译记账本。用法：VERSION=0.1.0 ./scripts/build.sh
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${VERSION:-0.1.0}"
LDFLAGS="-s -w -X main.version=${VERSION}"
mkdir -p dist

build() {
  local goos="$1" goarch="$2" out="$3"
  echo "→ ${goos}/${goarch} ${out}"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "$LDFLAGS" -o "dist/${out}" ./cmd/book
}

build darwin arm64 accountbook-darwin-arm64
build darwin amd64 accountbook-darwin-amd64
build linux amd64 accountbook-linux-amd64
build windows amd64 记账本.exe

ls -lh dist
