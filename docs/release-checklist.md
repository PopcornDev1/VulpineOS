# VulpineOS Release Checklist

This checklist is for public `VulpineOS` release tags and release
candidates.

## Pre-release state

1. Confirm `main` is clean:

   ```bash
   git status --short --branch
   ```

2. Confirm the public remote configuration is still safe:

   ```bash
   git config --get remote.upstream.pushurl
   ```

   Expected value:

   ```text
   DISABLED
   ```

3. Confirm release docs are current:

   - [README.md](../README.md)
   - [docs/release-hygiene.md](release-hygiene.md)
   - any public docs pages or examples changed by the release

## Verification

For Go/runtime changes:

```bash
go build ./internal/... ./cmd/...
go vet ./internal/... ./cmd/...
go test ./internal/... ./cmd/... -race
go build -o vulpineos ./cmd/vulpineos
```

For scoped-session release-candidate coverage, run the soak harness and
keep the JSON artifact with the release notes:

```bash
./scripts/run-soak.sh 3
```

This wrapper sets both `VULPINEOS_RUN_SOAK=1` and `VULPINEOS_RUN_LIVE=1`,
then writes a log plus a small JSON result artifact under `.artifacts/soak/`.

For Juggler JavaScript changes:

```bash
node --check additions/juggler/protocol/*.js
node --check additions/juggler/content/*.js
```

## Public-boundary checks

Run both public leak audits before tagging:

```bash
./scripts/public-boundary-audit.sh
./scripts/public-history-audit.py
```

These must pass before a release candidate or public tag.

## Browser build status

Record whether the release depends on a fresh Vulpine browser rebuild.

- If only Go/runtime/docs changed, the rebuilt `./vulpineos` binary is
  enough.
- If Firefox/Juggler patches changed, the release is not complete until a
  new Vulpine browser build has been produced on the trusted builder path.

For deferred browser rebuild work, link the release notes to the
tracking issue rather than implying the browser binary already contains
the patch set.

For a repeatable off-laptop rebuild path, use
[`docs/ec2-mac-builder.md`](ec2-mac-builder.md).

## Artifact Matrix

Record each expected artifact before drafting a public release:

| Artifact | Required when | Expected source |
|---|---|---|
| `vulpineos-darwin-arm64` / `vulpineos-darwin-amd64` | every macOS installer release | `GOOS=darwin GOARCH=<arch> go build -o vulpineos-darwin-<arch> ./cmd/vulpineos` |
| `vulpineos-linux-amd64` / `vulpineos-linux-arm64` | every Linux installer release | `GOOS=linux GOARCH=<arch> go build -o vulpineos-linux-<arch> ./cmd/vulpineos` |
| macOS browser packages | every macOS installer release | `make package-macos arch=<arch>`, producing `vulpine-<version>-<release>-mac.<x86_64|arm64>.zip` |
| Linux browser packages | every Linux installer release | `make package-linux arch=<arch>`, producing `vulpine-<version>-<release>-lin.<x86_64|arm64>.zip` |
| Linux Docker browser directory | Docker/Vulpine-Box release | extracted Linux browser artifact at `dist/vulpine-linux/vulpine` before `docker build` |
| soak JSON/log | release candidate | `.artifacts/soak/soak-*.json` and `.artifacts/soak/soak-*.log` from `./scripts/run-soak.sh 3` |
| builder metadata | browser rebuild | `/opt/vulpineos/artifacts/build-*.json` from `scripts/run-ec2-mac-build.sh` |
| checksums | every shipped binary/archive | `SHA256SUMS` covering every file attached to the release |

`install.sh` resolves assets from the latest GitHub release. The short install
URL `https://vulpineos.com/install` should serve or redirect to this script. It
requires the CLI asset name `vulpineos-<goos>-<goarch>` and a browser asset matching
`vulpine-*-<lin|mac>.<x86_64|arm64>.zip` for the installer's current platform.
The `Build and Release` workflow builds the CLI matrix, uploads the browser
packages from `multibuild.py`, creates `SHA256SUMS`, and drafts the tagged
GitHub release with all installer assets attached.

## Packaging

Before publishing the drafted release:

1. verify the artifacts match the matrix above for the release scope
2. verify `SHA256SUMS` covers every shipped archive and binary
3. run `./scripts/check-vulpinebox.sh` before any Docker/Vulpine-Box release
4. verify that release notes and docs do not describe private
   implementation details
5. verify no local/private files are included in the package contents
6. after publishing, verify `curl -fsSL https://vulpineos.com/install | bash`
   installs from the latest release

## Tagging

Create the release tag from `main`:

```bash
git tag vX.Y.Z
git push origin vX.Y.Z
```

## Post-tag checks

Verify:

1. the GitHub tag resolves to the expected commit
2. attached binaries or archives match the published checksums
3. the docs links in the release notes work
4. the public boundary audits still pass from the tagged tree
