# EC2 Mac Builder Runbook

This runbook standardizes an off-laptop AWS builder for Camoufox rebuilds and
packaging.

## Default Recommendation

Use:

- Region: `ap-southeast-2` (Sydney)
- Instance type: `mac2-m2pro.metal`

Why this default:

- It keeps the builder close to the New Zealand/Australia operator path.
- Sydney currently offers Apple silicon `Mac2-m2pro` capacity.
- AWS Mac instances require a Dedicated Host with a 24-hour minimum allocation,
  so a stable builder is more practical than ad hoc short-lived launches.

Always verify the current region and instance-family matrix before provisioning.
AWS changes Mac availability over time, and EC2 Mac capacity is not consistent
across regions.

References:

- AWS EC2 Mac instances: `docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-mac-instances.html`
- AWS instance availability by Region: `docs.aws.amazon.com/ec2/latest/instancetypes/ec2-instance-regions.html`

At the time of writing, AWS documents:

- `ap-southeast-2` exposes `Mac2-m2` and `Mac2-m2pro`
- `mac-m4` and `mac-m4pro` are available in regions such as `us-east-1`, but
  not in Sydney

If you want the newest AWS Mac hardware instead of the closest region, use:

- Region: `us-east-1`
- Instance type: `mac-m4.metal`

## AWS Constraints

- EC2 Mac instances run on Dedicated Hosts only.
- Billing is per-second with a 24-hour minimum host allocation.
- One Mac instance runs on one Dedicated Host.
- AWS recommends EBS-backed storage for Mac instances; do not depend on the
  local internal SSD.

## Builder Layout

Use a fixed root on the builder:

```text
/opt/vulpineos/
  artifacts/
  logs/
  src/
```

Recommended checkout path:

```text
/opt/vulpineos/src/VulpineOS
```

Recommended runtime outputs:

```text
/opt/vulpineos/logs/build-<timestamp>.log
/opt/vulpineos/logs/package-<timestamp>.log
/opt/vulpineos/artifacts/build-<timestamp>.json
```

## Bootstrap

On a fresh EC2 Mac instance:

```bash
sudo mkdir -p /opt/vulpineos/{artifacts,logs,src}
sudo chown -R "$USER":staff /opt/vulpineos
git clone https://github.com/VulpineOS/VulpineOS.git /opt/vulpineos/src/VulpineOS
cd /opt/vulpineos/src/VulpineOS
./scripts/bootstrap-ec2-mac-builder.sh
```

The bootstrap script:

- verifies Xcode/Command Line Tools presence
- installs Homebrew if needed
- installs builder dependencies (`git`, `gh`, `jq`, `node@22`, `python@3.12`,
  `sccache`, `watchman`)
- creates the standard log/artifact directories

## Rebuild Command

Run the standardized builder wrapper from the repo root:

```bash
cd /opt/vulpineos/src/VulpineOS
VULPINE_BUILDER_ROOT=/opt/vulpineos ./scripts/run-ec2-mac-build.sh
```

Optional packaging pass:

```bash
cd /opt/vulpineos/src/VulpineOS
VULPINE_BUILDER_ROOT=/opt/vulpineos VULPINE_RUN_PACKAGE=1 ./scripts/run-ec2-mac-build.sh
```

What it does:

- records the current git SHA
- runs `make build` with a timestamped build log
- optionally runs `make package-macos`
- rebuilds the Go runtime binary
- writes a JSON metadata file pointing to the exact log and artifact paths

## Artifact Handoff

Use the builder for browser artifacts; keep local validation on the laptop.

Recommended flow:

1. Make and review code changes locally.
2. Push the branch or `main`.
3. Pull the exact commit on the EC2 builder.
4. Run `./scripts/run-ec2-mac-build.sh`.
5. Pull back:
   - build log
   - JSON metadata
   - packaged app or tarball
6. Validate locally against the produced browser binary with:

```bash
VULPINE_BROWSER_BINARY=/absolute/path/to/vulpine VULPINEOS_RUN_LIVE=1 go test ./internal -run TestIntegration_AnnotatedScreenshotReturnsClickableObject -count=1 -v
```

7. Record the builder host, git SHA, artifact path, and validation result in the
   related Linear issue or release notes.

## Operational Notes

- Prefer long-lived builders over ad hoc detached shell rebuilds.
- Keep all build logs under `/opt/vulpineos/logs`; do not rely on shell scrollback.
- Keep builder checkouts clean between runs:

```bash
git status --short
```

- Release the Dedicated Host only after the 24-hour minimum if the builder is
  no longer needed.
