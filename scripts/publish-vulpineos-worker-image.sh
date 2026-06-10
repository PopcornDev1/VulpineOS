#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

: "${IMAGE_TAG:?Set IMAGE_TAG to the VulpineOS release tag, for example: IMAGE_TAG=v0.1.8-dev.4}"

GHCR_OWNER="${GHCR_OWNER:-vulpineos}"
DOCKER_PLATFORM="${DOCKER_PLATFORM:-linux/amd64}"
PUBLISH_LATEST="${PUBLISH_LATEST:-1}"
PREPARE_BROWSER="${PREPARE_BROWSER:-1}"
VULPINEOS_REPO="${VULPINEOS_REPO:-VulpineOS/VulpineOS}"
VULPINEOS_BROWSER_ASSET_PATTERN="${VULPINEOS_BROWSER_ASSET_PATTERN:-camoufox-*-lin.x86_64.zip}"

download_browser_asset() {
  local dest="$1"

  if [[ -n "${VULPINEOS_BROWSER_URL:-}" ]]; then
    echo "Downloading browser artifact from VULPINEOS_BROWSER_URL"
    curl --fail --location --retry 3 --output "$dest" "$VULPINEOS_BROWSER_URL"
    return 0
  fi

  local url
  url="$(python3 - "$VULPINEOS_REPO" "${VULPINEOS_RELEASE_TAG:-}" "$VULPINEOS_BROWSER_ASSET_PATTERN" <<'PY'
import fnmatch
import json
import os
import sys
import urllib.error
import urllib.request

repo, release_tag, pattern = sys.argv[1:]
release_path = "latest" if not release_tag else f"tags/{release_tag}"
url = f"https://api.github.com/repos/{repo}/releases/{release_path}"
headers = {"Accept": "application/vnd.github+json", "User-Agent": "vulpineos-worker-image-publisher"}
token = os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN")
if token:
    headers["Authorization"] = "Bearer " + token

try:
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=30) as response:
        release = json.load(response)
except urllib.error.HTTPError as exc:
    sys.stderr.write(f"GitHub release lookup failed for {repo} {release_tag or 'latest'}: HTTP {exc.code}\n")
    sys.exit(1)

for asset in release.get("assets") or []:
    name = asset.get("name", "")
    if fnmatch.fnmatch(name, pattern):
        print(asset["browser_download_url"])
        sys.exit(0)

tag = release.get("tag_name", release_tag or "latest")
sys.stderr.write(f"Release {tag} is missing required asset matching {pattern}\n")
sys.exit(1)
PY
)"

  echo "Downloading browser artifact: $url"
  curl --fail --location --retry 3 --output "$dest" "$url"
}

prepare_browser_dist() {
  local archive="$1"
  local extract_dir
  extract_dir="$(mktemp -d)"

  rm -rf dist/camoufox-linux
  mkdir -p dist/camoufox-linux

  unzip -q "$archive" -d "$extract_dir"
  cp -a "$extract_dir"/. dist/camoufox-linux/
  rm -rf "$extract_dir"

  if [[ -f dist/camoufox-linux/camoufox ]]; then
    chmod 0755 dist/camoufox-linux/camoufox >/dev/null 2>&1 || true
  fi

  if [[ ! -x dist/camoufox-linux/camoufox ]]; then
    echo "Missing executable Linux browser artifact at dist/camoufox-linux/camoufox" >&2
    echo "The release asset must be a Linux x86_64 VulpineOS/Camoufox package." >&2
    exit 1
  fi

  echo "Prepared dist/camoufox-linux from release artifact."
}

if [[ "$PREPARE_BROWSER" == "1" || "$PREPARE_BROWSER" == "true" ]]; then
  browser_archive="${BROWSER_ARCHIVE:-/tmp/vulpineos-camoufox-linux-x86_64.zip}"
  download_browser_asset "$browser_archive"
  prepare_browser_dist "$browser_archive"
fi

if [[ ! -x dist/camoufox-linux/camoufox ]]; then
  echo "Missing dist/camoufox-linux/camoufox. Set PREPARE_BROWSER=1 or provide the extracted browser artifact first." >&2
  exit 1
fi

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
