#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

targets=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
)

for t in "${targets[@]}"; do
  read -r goos goarch <<<"${t}"
  echo "Building ${goos}/${goarch}..."
  GOOS="${goos}" GOARCH="${goarch}" ./scripts/build.sh
done

echo "All binaries are in dist/"
