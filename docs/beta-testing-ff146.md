# Firefox 146 Build and Runtime Testing

VulpineOS is based on Firefox 146.0.1 with Camoufox patches. Use this guide to rebuild the browser, point the Go runtime at the rebuilt binary, and package the current launch artifact.

## Build From Source

From the repository root:

```bash
make fetch
make setup
make dir
BUILD_TARGET=macos,arm64 make build
```

Use the target that matches the machine doing the build:

| Target | Example |
|---|---|
| macOS arm64 | `BUILD_TARGET=macos,arm64 make build` |
| macOS x86_64 | `BUILD_TARGET=macos,x86_64 make build` |
| Linux x86_64 | `BUILD_TARGET=linux,x86_64 make build` |

Build artifacts are written under the extracted Firefox source tree, for example:

```text
camoufox-146.0.1-beta.25/obj-aarch64-apple-darwin/dist/Camoufox.app/Contents/MacOS/camoufox
```

## Runtime Smoke Test

Build the VulpineOS runtime and pass the browser binary explicitly:

```bash
go build -o vulpineos ./cmd/vulpineos
./vulpineos --binary /path/to/camoufox
```

Useful local commands:

```bash
./vulpineos --listen --port 8443 --api-key devtest --binary /path/to/camoufox
./vulpineos serve --no-tls --port 8443 --api-key devtest --binary /path/to/camoufox
./vulpineos remote --url http://127.0.0.1:8443 --api-key devtest
```

When no `--binary` flag is provided, VulpineOS prefers a repo-local `camoufox-*/obj-*/dist` build before falling back to configured or installed browser paths. Passing `--binary` is still recommended for launch validation because it removes ambiguity.

## Packaging

For a local macOS package:

```bash
make package-macos arch=arm64
```

The macOS package step uses native `hdiutil`/`ditto` on macOS and falls back to `7z` where appropriate for other targets.

For Vulpine-Box Docker builds, provide a Linux browser artifact before building the container:

```text
dist/camoufox-linux/camoufox
```

The stock container launches:

```bash
vulpineos serve --binary ./browser/camoufox --port 8443 --no-tls
```

Set `VULPINE_API_KEY` before using `docker compose up -d`.

## Do Not Replace Python Cache Binaries

Older Camoufox testing docs described replacing binaries inside the Python package cache. For VulpineOS validation, do not mutate the cache. Pass the exact browser path to `vulpineos --binary`, or place the Linux artifact in `dist/camoufox-linux/` for Docker packaging.
