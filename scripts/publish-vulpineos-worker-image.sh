#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

: "${IMAGE_TAG:?Set IMAGE_TAG, for example: IMAGE_TAG=2026-06-10-1}"

GHCR_OWNER="${GHCR_OWNER:-vulpineos}"
DOCKER_PLATFORM="${DOCKER_PLATFORM:-linux/amd64}"
PUBLISH_LATEST="${PUBLISH_LATEST:-1}"
BUILD_BROWSER="${BUILD_BROWSER:-1}"
BUILD_JOBS="${BUILD_JOBS:-2}"
SOURCE_DIR="${SOURCE_DIR:-camoufox-146.0.1-beta.25}"

if [[ "$BUILD_BROWSER" == "1" || "$BUILD_BROWSER" == "true" ]]; then
  rm -f camoufox-*-lin.x86_64.zip
  BUILD_TARGET=linux,x86_64 make dir
  (
    cd "$SOURCE_DIR"
    ./mach configure
    ./mach build -j "$BUILD_JOBS"
  )
  make package-linux arch=x86_64
fi

if [[ ! -x dist/camoufox-linux/camoufox ]]; then
  shopt -s nullglob
  packages=(camoufox-*-lin.x86_64.zip)
  shopt -u nullglob

  if (( ${#packages[@]} == 0 )); then
    echo "Missing packaged Linux Camoufox zip and dist/camoufox-linux/camoufox" >&2
    echo "Run with BUILD_BROWSER=1 or provide dist/camoufox-linux/camoufox first." >&2
    exit 1
  fi

  rm -rf dist/camoufox-linux
  mkdir -p dist/camoufox-linux
  7z x "${packages[0]}" -odist/camoufox-linux
fi

test -x dist/camoufox-linux/camoufox

image="ghcr.io/${GHCR_OWNER}/vulpineos-worker"
tags=(-t "$image:$IMAGE_TAG")

if [[ "$PUBLISH_LATEST" == "1" || "$PUBLISH_LATEST" == "true" ]]; then
  tags+=(-t "$image:latest")
fi

docker build --platform "$DOCKER_PLATFORM" \
  -f Dockerfile.vulpinebox \
  "${tags[@]}" \
  .

docker push "$image:$IMAGE_TAG"

if [[ "$PUBLISH_LATEST" == "1" || "$PUBLISH_LATEST" == "true" ]]; then
  docker push "$image:latest"
fi

echo "Published $image:$IMAGE_TAG"
