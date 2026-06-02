#!/usr/bin/env python3
"""Emit a single per-context camoufox fingerprint config as JSON on stdout.

This is the public BrowserForge generator bridge used by the Go runtime
(internal/vault/fingerprint_browserforge.go). It is intentionally thin: it
calls camoufox's generate_context_fingerprint() and prints the dotted-key
`config` dict so Go can store/apply it exactly like the offline fallback.

Requires the full python pipeline (browserforge, orjson, camoufox package).
On any failure it exits non-zero with a short message on stderr; the Go caller
treats that as "BrowserForge unavailable" and falls back to the offline
generator. Run with cwd or PYTHONPATH set to the pythonlib directory.
"""
import argparse
import json
import random
import sys


def _os_name(host_os: str) -> str:
    # Map the camoufox host identifier (mac/win/lin) to the browserforge OS name.
    return {
        "mac": "macos",
        "win": "windows",
        "lin": "linux",
    }.get((host_os or "").strip().lower(), None)


def main() -> int:
    ap = argparse.ArgumentParser(description="Emit a camoufox fingerprint config as JSON.")
    ap.add_argument("--os", dest="host_os", default="", help="host OS: mac|win|lin")
    ap.add_argument("--seed", dest="seed", default="", help="stable per-identity seed (UUID)")
    ap.add_argument("--ff-version", dest="ff_version", default="", help="Firefox version override")
    ap.add_argument("--webrtc-ip", dest="webrtc_ip", default="", help="WebRTC IP to embed")
    args = ap.parse_args()

    # Seed the RNG from the per-identity UUID so the same agent regenerates a
    # stable fingerprint. Falls back to OS entropy when no seed is supplied.
    if args.seed:
        random.seed(args.seed)

    try:
        from camoufox.fingerprints import generate_context_fingerprint
    except Exception as exc:  # noqa: BLE001 - any import failure means "unavailable"
        print(f"genfp: camoufox pipeline unavailable: {exc}", file=sys.stderr)
        return 2

    try:
        result = generate_context_fingerprint(
            os=_os_name(args.host_os),
            ff_version=args.ff_version or None,
            webrtc_ip=args.webrtc_ip or None,
        )
    except Exception as exc:  # noqa: BLE001
        print(f"genfp: generation failed: {exc}", file=sys.stderr)
        return 3

    config = result.get("config") if isinstance(result, dict) else None
    if not config:
        print("genfp: empty config", file=sys.stderr)
        return 4

    json.dump(config, sys.stdout)
    return 0


if __name__ == "__main__":
    sys.exit(main())
