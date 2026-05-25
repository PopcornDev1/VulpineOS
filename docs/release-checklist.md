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

Record whether the release depends on a fresh Camoufox rebuild.

- If only Go/runtime/docs changed, the rebuilt `./vulpineos` binary is
  enough.
- If Firefox/Juggler patches changed, the release is not complete until a
  new Camoufox build has been produced on the trusted builder path.

For deferred browser rebuild work, link the release notes to the
tracking issue rather than implying the browser binary already contains
the patch set.

For a repeatable off-laptop rebuild path, use
[`docs/ec2-mac-builder.md`](ec2-mac-builder.md).

## Artifact Matrix

Record each expected artifact before drafting a public release:

| Artifact | Required when | Expected source |
|---|---|---|
| `vulpineos-darwin-arm64` | every macOS release | `go build -o vulpineos ./cmd/vulpineos` on the trusted builder or local release machine |
| `vulpineos-linux-amd64` | every Linux/Docker release | `GOOS=linux GOARCH=amd64 go build -o vulpineos-linux ./cmd/vulpineos` |
| macOS browser package | Firefox/Juggler/browser patches changed | `make package-macos arch=arm64`, producing `camoufox-<version>-<release>-mac.arm64.zip` |
| Linux Docker browser directory | Docker/Vulpine-Box release | extracted Linux browser artifact at `dist/camoufox-linux/camoufox` before `docker build` |
| soak JSON/log | release candidate | `.artifacts/soak/soak-*.json` and `.artifacts/soak/soak-*.log` from `./scripts/run-soak.sh 3` |
| builder metadata | browser rebuild | `/opt/vulpineos/artifacts/build-*.json` from `scripts/run-ec2-mac-build.sh` |
| checksums | every shipped binary/archive | `SHA256SUMS` covering every file attached to the release |

## Packaging

Before publishing artifacts:

1. rebuild `./vulpineos`
2. verify the artifacts match the matrix above for the release scope
3. run `./scripts/check-vulpinebox.sh` before any Docker/Vulpine-Box release
4. compute checksums for shipped archives or binaries
5. verify that release notes and docs do not describe private
   implementation details
6. verify no local/private files are included in the package contents

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
