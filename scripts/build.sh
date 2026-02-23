#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"

mkdir -p "${DIST_DIR}"

GOOS="${GOOS:-linux}"
GOARCH="${GOARCH:-amd64}"
EXT=""
if [[ "${GOOS}" == "windows" ]]; then
  EXT=".exe"
fi

OUT="${DIST_DIR}/infermeshd-${GOOS}-${GOARCH}${EXT}"
echo "Building ${OUT}"
cd "${ROOT_DIR}"
go build -o "${OUT}" ./cmd/infermeshd
echo "Done: ${OUT}"
