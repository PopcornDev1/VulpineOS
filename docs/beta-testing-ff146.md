# Firefox 146 Vulpine Browser Build and Runtime Testing

VulpineOS is based on Firefox 146.0.1 with Vulpine browser patches inherited from the Camoufox line. Use this guide to rebuild the browser, point the Go runtime at the rebuilt binary, and package the current launch artifact.

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
camoufox-146.0.1-beta.25/obj-aarch64-apple-darwin/dist/Vulpine.app/Contents/MacOS/vulpine
```

## Runtime Smoke Test

Build the VulpineOS runtime and pass the browser binary explicitly:

```bash
go build -o vulpineos ./cmd/vulpineos
./vulpineos --binary /path/to/vulpine
```

Useful local commands:

```bash
./vulpineos --listen --port 8443 --api-key devtest --binary /path/to/vulpine
./vulpineos serve --no-tls --port 8443 --api-key devtest --binary /path/to/vulpine
./vulpineos remote --url http://127.0.0.1:8443 --api-key devtest
```

When no `--binary` flag is provided, VulpineOS prefers a repo-local `camoufox-*/obj-*/dist` source build before falling back to configured or installed browser paths. The extracted source directory still uses the upstream Camoufox naming convention, but packaged executables are branded as Vulpine. Passing `--binary` is still recommended for launch validation because it removes ambiguity.

## Packaging

For a local macOS package:

```bash
make package-macos arch=arm64
```

The macOS package step uses native `hdiutil`/`ditto` on macOS and falls back to `7z` where appropriate for other targets.

For Vulpine-Box Docker builds, provide a Linux browser artifact before building the container:

```text
dist/vulpine-linux/vulpine
```

The stock container launches:

```bash
vulpineos serve --binary ./browser/vulpine --port 8443 --no-tls
```

Set `VULPINE_API_KEY` before using `docker compose up -d`.

## Do Not Replace Python Cache Binaries

Older Camoufox testing docs described replacing binaries inside the Python package cache. For VulpineOS validation, do not mutate the cache. Pass the exact browser path to `vulpineos --binary`, or place the Linux artifact in `dist/vulpine-linux/` for Docker packaging.
