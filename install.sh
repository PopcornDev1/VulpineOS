#!/bin/bash
set -euo pipefail

VULPINEOS_REPO="${VULPINEOS_REPO:-VulpineOS/VulpineOS}"
VULPINEOS_HOME="${HOME}/.vulpineos"
VULPINEOS_BIN_DIR="${VULPINEOS_BIN_DIR:-}"
VULPINEOS_BROWSER_DIR="${VULPINEOS_BROWSER_DIR:-${VULPINEOS_HOME}/browser}"

log() {
    printf '%s\n' "$*"
}

fatal() {
    printf 'Error: %s\n' "$*" >&2
    exit 1
}

have() {
    command -v "$1" >/dev/null 2>&1
}

path_contains() {
    case ":${PATH}:" in
        *":$1:"*) return 0 ;;
        *) return 1 ;;
    esac
}

detect_goos() {
    case "$(uname -s)" in
        Linux) echo "linux" ;;
        Darwin) echo "darwin" ;;
        *) fatal "unsupported OS: $(uname -s). VulpineOS installer supports Linux and macOS." ;;
    esac
}

detect_goarch() {
    case "$(uname -m)" in
        x86_64|amd64) echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        i386|i686) echo "386" ;;
        *) fatal "unsupported architecture: $(uname -m)" ;;
    esac
}

browser_os_for_goos() {
    case "$1" in
        linux) echo "lin" ;;
        darwin) echo "mac" ;;
        *) fatal "unsupported browser OS: $1" ;;
    esac
}

browser_arch_for_goarch() {
    case "$1" in
        amd64) echo "x86_64" ;;
        arm64) echo "arm64" ;;
        386) echo "i686" ;;
        *) fatal "unsupported browser architecture: $1" ;;
    esac
}

select_bin_dir() {
    if [ -n "${VULPINEOS_BIN_DIR}" ]; then
        echo "${VULPINEOS_BIN_DIR}"
        return 0
    fi

    if path_contains "${HOME}/.local/bin"; then
        echo "${HOME}/.local/bin"
        return 0
    fi

    IFS=':' read -r -a path_dirs <<< "${PATH}"
    for dir in "${path_dirs[@]}"; do
        if [ -n "${dir}" ] && [ "${dir}" != "." ] && [ -d "${dir}" ] && [ -w "${dir}" ]; then
            echo "${dir}"
            return 0
        fi
    done

    echo "${HOME}/.local/bin"
}

ensure_bin_dir_on_path() {
    local bin_dir="$1"
    if path_contains "${bin_dir}"; then
        return 0
    fi

    local profile="${HOME}/.profile"
    case "$(basename "${SHELL:-}")" in
        zsh) profile="${HOME}/.zshrc" ;;
        bash) profile="${HOME}/.bashrc" ;;
    esac

    local path_line="export PATH=\"${bin_dir}:\$PATH\""
    if [ ! -f "${profile}" ] || ! grep -Fq "${path_line}" "${profile}"; then
        printf '\n# VulpineOS\n%s\n' "${path_line}" >> "${profile}"
    fi

    log "Added ${bin_dir} to ${profile}. Open a new shell before running 'vulpineos' if this shell cannot find it."
}

download_to() {
    local url="$1"
    local dest="$2"
    if have curl; then
        curl --fail --location --retry 3 --output "${dest}" "${url}"
    elif have wget; then
        wget --output-document="${dest}" "${url}"
    else
        fatal "curl or wget is required to download release assets."
    fi
}

resolve_release_assets() {
    local goos="$1"
    local goarch="$2"
    local browser_os="$3"
    local browser_arch="$4"
    local tag="${5:-}"

    if [ -n "${VULPINEOS_CLI_URL:-}" ] && [ -n "${VULPINEOS_BROWSER_URL:-}" ]; then
        printf 'VULPINEOS_RELEASE_TAG=%q\n' "${VULPINEOS_RELEASE_TAG:-manual}"
        printf 'VULPINEOS_CLI_URL=%q\n' "${VULPINEOS_CLI_URL}"
        printf 'VULPINEOS_BROWSER_URL=%q\n' "${VULPINEOS_BROWSER_URL}"
        printf 'VULPINEOS_BROWSER_ASSET=%q\n' "${VULPINEOS_BROWSER_ASSET:-manual-camoufox.zip}"
        return 0
    fi

    python3 - "${VULPINEOS_REPO}" "${VULPINEOS_BROWSER_RELEASE:-}" "${goos}" "${goarch}" "${browser_os}" "${browser_arch}" "${tag}" <<'PY'
import json
import os
import shlex
import sys
import urllib.error
import urllib.request

repo, browser_fallback, goos, goarch, browser_os, browser_arch, tag = sys.argv[1:]
tag = tag.strip() or None
headers = {"Accept": "application/vnd.github+json", "User-Agent": "vulpineos-installer"}
if os.environ.get("GITHUB_TOKEN"):
    headers["Authorization"] = "Bearer " + os.environ["GITHUB_TOKEN"]

def fetch_release(tag=None):
    url = f"https://api.github.com/repos/{repo}/releases/latest" if tag is None else \
           f"https://api.github.com/repos/{repo}/releases/tags/{tag}"
    try:
        with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=30) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as exc:
        if exc.code == 404:
            sys.stderr.write(f"Release not found: {tag or 'latest'}\n")
        else:
            sys.stderr.write(f"GitHub release lookup failed (HTTP {exc.code}): {tag or 'latest'}\n")
        return None
    except Exception as exc:
        sys.stderr.write(f"GitHub release lookup failed: {exc}\n")
        return None

release = fetch_release(tag)
if release is None:
    sys.stderr.write("No release found for specified tag; falling back to latest release for browser assets.\n")
    release = fetch_release()
if release is None:
    sys.exit(1)

assets = release.get("assets") or []
cli_names = [f"vulpineos-{goos}-{goarch}"]
if goos == "windows":
    cli_names.append(f"vulpineos-{goos}-{goarch}.exe")
browser_suffix = f"-{browser_os}.{browser_arch}.zip"

cli_asset = None
browser_asset = None
for asset in assets:
    name = asset.get("name", "")
    if cli_asset is None and name in cli_names:
        cli_asset = asset
    if browser_asset is None and name.startswith("camoufox-") and name.endswith(browser_suffix):
        browser_asset = asset

# If Camoufox not in latest release, try the fallback browser release
if browser_asset is None and browser_fallback:
    fb = fetch_release(browser_fallback)
    if fb:
        for asset in fb.get("assets") or []:
            name = asset.get("name", "")
            if browser_asset is None and name.startswith("camoufox-") and name.endswith(browser_suffix):
                browser_asset = asset
                break

# If CLI not in latest release, try the fallback browser release
if cli_asset is None and browser_fallback:
    fb = fetch_release(browser_fallback)
    if fb:
        for asset in fb.get("assets") or []:
            name = asset.get("name", "")
            if cli_asset is None and name in cli_names:
                cli_asset = asset
                break

missing = []
if cli_asset is None:
    missing.append(" or ".join(cli_names))
if browser_asset is None:
    missing.append(f"camoufox-*{browser_suffix}")
if missing:
    tag = release.get("tag_name", "latest")
    sys.stderr.write(f"Release {tag} is missing required installer asset(s): {', '.join(missing)}\n")
    sys.exit(1)

values = {
    "VULPINEOS_RELEASE_TAG": release.get("tag_name", "latest"),
    "VULPINEOS_CLI_URL": cli_asset["browser_download_url"],
    "VULPINEOS_BROWSER_URL": browser_asset["browser_download_url"],
    "VULPINEOS_BROWSER_ASSET": browser_asset["name"],
}
for key, value in values.items():
    print(f"{key}={shlex.quote(str(value))}")
PY
}

resolve_camoufox_binary() {
    local root="$1"
    local candidates=(
        "${root}/camoufox-bin"
        "${root}/camoufox/camoufox-bin"
        "${root}/camoufox"
        "${root}/camoufox/camoufox"
        "${root}/Camoufox.app/Contents/MacOS/camoufox"
        "${root}/camoufox/Camoufox.app/Contents/MacOS/camoufox"
    )
    local candidate
    for candidate in "${candidates[@]}"; do
        if [ -f "${candidate}" ]; then
            chmod 0755 "${candidate}" >/dev/null 2>&1 || true
            if [ -x "${candidate}" ]; then
                echo "${candidate}"
                return 0
            fi
        fi
    done
    return 1
}

write_browser_config() {
    local browser_bin="$1"
    python3 - "${VULPINEOS_HOME}/config.json" "${browser_bin}" <<'PY'
import json
import os
import sys
from pathlib import Path

config_path = Path(sys.argv[1])
browser_bin = sys.argv[2]
config_path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)

config = {}
if config_path.exists():
    try:
        config = json.loads(config_path.read_text())
    except Exception:
        config = {}
config["binaryPath"] = browser_bin

tmp_path = config_path.with_suffix(config_path.suffix + ".tmp")
tmp_path.write_text(json.dumps(config, indent=2) + "\n")
os.chmod(tmp_path, 0o600)
tmp_path.replace(config_path)
os.chmod(config_path, 0o600)
PY
}

install_browser() {
    local bin_dir="$1"
    local zip_path="${VULPINEOS_HOME}/${VULPINEOS_BROWSER_ASSET}.tmp.$$"
    local extract_dir="${VULPINEOS_BROWSER_DIR}.tmp.$$"

    log "Downloading VulpineOS Camoufox bundle (${VULPINEOS_BROWSER_ASSET})..."
    rm -f "${zip_path}"
    rm -rf "${extract_dir}"
    download_to "${VULPINEOS_BROWSER_URL}" "${zip_path}"
    mkdir -p "${extract_dir}"
    unzip -q "${zip_path}" -d "${extract_dir}"
    rm -f "${zip_path}"

    local tmp_browser_bin
    tmp_browser_bin="$(resolve_camoufox_binary "${extract_dir}")" || {
        rm -rf "${extract_dir}"
        fatal "Camoufox bundle did not contain a supported executable."
    }

    rm -rf "${VULPINEOS_BROWSER_DIR}"
    mv "${extract_dir}" "${VULPINEOS_BROWSER_DIR}"
    local browser_bin="${VULPINEOS_BROWSER_DIR}${tmp_browser_bin#"${extract_dir}"}"
    chmod 0755 "${browser_bin}" >/dev/null 2>&1 || true
    if have xattr; then
        xattr -dr com.apple.quarantine "${VULPINEOS_BROWSER_DIR}" >/dev/null 2>&1 || true
    fi

    ln -sf "${browser_bin}" "${bin_dir}/camoufox" || true
    write_browser_config "${browser_bin}"
    log "Installed Camoufox: ${browser_bin}"
}

build_cli_from_source() {
    local bin_dir="$1"
    local src_dir="${VULPINEOS_HOME}/src.tmp.$$"
    local repo_url="https://github.com/${VULPINEOS_REPO}.git"
    local goos goarch
    goos="$(detect_goos)"
    goarch="$(detect_goarch)"

    log "Building vulpineos CLI from source..."
    rm -rf "${src_dir}"

    # Partial clone: transfer only commit metadata — file blobs are fetched
    # on demand. The repo contains ~930 MiB of Camoufox font bundles
    # (bundle/fonts/) that are not needed for compiling the Go CLI binary.
    local ref=""
    if [ -n "${VULPINEOS_RELEASE_TAG:-}" ]; then
        ref="${VULPINEOS_RELEASE_TAG}"
        log "Checking out release ${ref}..."
        # Cannot use --depth 1 with a specific tag; use full clone then checkout.
        git clone --single-branch "${repo_url}" "${src_dir}" || fatal "Failed to clone ${VULPINEOS_REPO}."
        (cd "${src_dir}" && git checkout "refs/tags/${ref}") || fatal "Tag ${ref} not found in ${VULPINEOS_REPO}."
        rm -rf "${src_dir}/bundle"
    elif git clone --depth 1 --filter=blob:none "${repo_url}" "${src_dir}" 2>/dev/null; then
        # Remove font bundle placeholders; their blobs were never downloaded.
        rm -rf "${src_dir}/bundle"
    else
        # Older git: full shallow clone, then delete font bundle.
        git clone --depth 1 --single-branch "${repo_url}" "${src_dir}" || fatal "Failed to clone ${VULPINEOS_REPO}."
        rm -rf "${src_dir}/bundle"
    fi

    (cd "${src_dir}" && go build -o "vulpineos-${goos}-${goarch}" ./cmd/vulpineos/) || {
        local exit_code=$?
        rm -rf "${src_dir}"
        return ${exit_code}
    }
    cp "${src_dir}/vulpineos-${goos}-${goarch}" "${bin_dir}/vulpineos"
    chmod 0755 "${bin_dir}/vulpineos"
    rm -rf "${src_dir}"
    log "Installed CLI (built from source): ${bin_dir}/vulpineos"
    return 0
}

usage() {
    cat <<'USAGE'
Usage: install.sh [OPTIONS]

Install VulpineOS CLI and Camoufox browser.

Options:
  --release <tag>  Install a specific release tag (e.g. v0.1.8-dev.6).
                   When set, the CLI is built from source at that tag.
                   When absent, the latest GitHub release is used.
  --help           Show this help message and exit.

Environment variables:
  VULPINEOS_RELEASE_TAG    Same as --release (takes precedence when both set).
  VULPINEOS_BROWSER_RELEASE  Fallback release for Camoufox browser assets.
  VULPINEOS_CLI_URL        Direct download URL for CLI binary (skips release lookup).
  VULPINEOS_BROWSER_URL    Direct download URL for browser bundle (skips release lookup).
USAGE
}

main() {
    log "Installing VulpineOS..."
    have python3 || fatal "python3 is required to resolve release assets and update local config."
    have unzip || fatal "unzip is required to extract the Camoufox browser bundle."
    have curl || have wget || fatal "curl or wget is required to download release assets."

    # Parse CLI flags
    local release_tag_arg=""
    while [ $# -gt 0 ]; do
        case "$1" in
            --release)
                shift
                if [ $# -eq 0 ]; then
                    fatal "--release requires a tag argument (e.g. v0.1.8-dev.6)"
                fi
                release_tag_arg="$1"
                shift
                ;;
            --help)
                usage
                exit 0
                ;;
            *)
                fatal "unknown option: $1. Use --help for usage."
                ;;
        esac
    done

    # Release tag resolution: CLI arg > env var > latest
    if [ -n "${VULPINEOS_RELEASE_TAG:-}" ]; then
        :  # env var takes highest precedence
    elif [ -n "${release_tag_arg}" ]; then
        VULPINEOS_RELEASE_TAG="${release_tag_arg}"
    fi

    # Fallback release for Camoufox browser assets (CLI may come from a newer
    # release that only has the Go binary). Override via VULPINEOS_BROWSER_RELEASE.
    : "${VULPINEOS_BROWSER_RELEASE:=v0.1.4}"

    local goos goarch browser_os browser_arch bin_dir asset_env
    goos="$(detect_goos)"
    goarch="$(detect_goarch)"
    browser_os="$(browser_os_for_goos "${goos}")"
    browser_arch="$(browser_arch_for_goarch "${goarch}")"
    bin_dir="$(select_bin_dir)"

    mkdir -p "${VULPINEOS_HOME}" "${bin_dir}"

    # Resolve release assets (needed for Camoufox browser, optionally for CLI fallback)
    asset_env="$(resolve_release_assets "${goos}" "${goarch}" "${browser_os}" "${browser_arch}" "${VULPINEOS_RELEASE_TAG:-}")"
    eval "${asset_env}"

    log "Installing VulpineOS release ${VULPINEOS_RELEASE_TAG}..."

    # Install CLI: prefer building from source (always latest), fall back to release binary
    if have go && have git; then
        build_cli_from_source "${bin_dir}" || {
            log "Source build failed, falling back to release binary..."
            local cli_tmp="${VULPINEOS_HOME}/vulpineos.tmp.$$"
            rm -f "${cli_tmp}"
            download_to "${VULPINEOS_CLI_URL}" "${cli_tmp}"
            chmod 0755 "${cli_tmp}"
            mv "${cli_tmp}" "${bin_dir}/vulpineos"
            log "Installed CLI (from release): ${bin_dir}/vulpineos"
        }
    else
        log "Go or git not found, downloading prebuilt CLI from release..."
        local cli_tmp="${VULPINEOS_HOME}/vulpineos.tmp.$$"
        rm -f "${cli_tmp}"
        download_to "${VULPINEOS_CLI_URL}" "${cli_tmp}"
        chmod 0755 "${cli_tmp}"
        mv "${cli_tmp}" "${bin_dir}/vulpineos"
        log "Installed CLI (from release): ${bin_dir}/vulpineos"
    fi

    install_browser "${bin_dir}"
    ensure_bin_dir_on_path "${bin_dir}"

    log ""
    log "Installation complete. Run: vulpineos"
    log ""
}

main "$@"
