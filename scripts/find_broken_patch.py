#!/usr/bin/env python3

"""
Binary search for the patch that leaks automation signals.

Builds Camoufox with progressively narrower subsets of patches,
testing each build against user-defined criteria until the
offending patch is isolated.

Prerequisites:
    - Firefox source already fetched and set up (make fetch && make setup)
    - Build dependencies installed (make bootstrap)

Usage:
    python3 scripts/find_broken_patch.py --target macos --arch arm64
"""

import argparse
import math
import os
import shutil
import subprocess
import sys

from _mixin import find_src_dir, get_moz_target, list_patches, run, temp_cd


# ---------------------------------------------------------------------------
# USER-DEFINED TEST CRITERIA — edit this function
# ---------------------------------------------------------------------------

def test_criteria(binary_path: str) -> bool:
    """
    User-defined test function.

    Receives the absolute path to the built camoufox binary.
    Return True  if the browser PASSES (no leak detected).
    Return False if the browser FAILS (leak/automation signal detected).

    Example:
        from playwright.sync_api import sync_playwright
        with sync_playwright() as p:
            browser = p.firefox.launch(executable_path=binary_path)
            page = browser.new_page()
            page.goto("https://bot.sannysoft.com/")
            # ... inspect results ...
            browser.close()
        return passed
    """
    raise NotImplementedError(
        "Write your test criteria in test_criteria() before running this script."
    )


# ---------------------------------------------------------------------------
# Internals
# ---------------------------------------------------------------------------

AVAILABLE_TARGETS = ["linux", "windows", "macos"]
AVAILABLE_ARCHS = ["x86_64", "arm64", "i686"]


def parse_upstream():
    """Read version and release from upstream.sh."""
    vals = {}
    with open("upstream.sh") as f:
        for line in f:
            line = line.strip()
            if "=" in line and not line.startswith("#"):
                key, value = line.split("=", 1)
                vals[key.strip()] = value.strip()
    return vals["version"], vals["release"]


def ordered_patches():
    """Return patches in the same order patch.py applies them:
    non-roverfox first, then roverfox."""
    all_patches = list_patches()
    non_roverfox = []
    roverfox = []
    for p in all_patches:
        parts = os.path.normpath(p).split(os.sep)
        if "roverfox" in parts:
            roverfox.append(p)
        else:
            non_roverfox.append(p)
    return non_roverfox + roverfox


def get_binary_path(src_dir, target, arch):
    """Resolve the built binary path based on platform."""
    moz_target = get_moz_target(target, arch)
    if target == "macos":
        return os.path.join(
            src_dir,
            f"obj-{moz_target}",
            "dist",
            "Camoufox.app",
            "Contents",
            "MacOS",
            "camoufox",
        )
    return os.path.join(src_dir, f"obj-{moz_target}", "dist", "bin", "camoufox-bin")


def write_mozconfig(target, arch):
    """Create mozconfig with the correct build target."""
    moz_target = get_moz_target(target, arch)
    shutil.copy("../assets/base.mozconfig", "mozconfig")

    with open("mozconfig", "a") as f:
        f.write(f"\nac_add_options --target={moz_target}\n")
        target_extra = os.path.join("..", "assets", f"{target}.mozconfig")
        if os.path.exists(target_extra):
            with open(target_extra) as tf:
                f.write(tf.read())


def reset_source(version, release):
    """Reset the source tree to unpatched state and re-copy additions."""
    run("git clean -fdx && git reset --hard unpatched", exit_on_fail=True)
    run(f"bash ../scripts/copy-additions.sh {version} {release}", exit_on_fail=True)


def apply_patches(patches):
    """Apply a list of patch files. Returns True on success."""
    for p in patches:
        ret = os.system(f"patch -p1 --forward -l --binary -i {p}")
        if ret != 0:
            print(f"WARNING: patch failed to apply cleanly: {p}")
            return False
    return True


def build():
    """Run the build. Returns True on success."""
    ret = os.system("./mach build")
    return ret == 0


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(
        description="Binary search for the patch that leaks automation signals."
    )
    parser.add_argument(
        "--target",
        choices=AVAILABLE_TARGETS,
        required=True,
        help="Build target platform",
    )
    parser.add_argument(
        "--arch",
        choices=AVAILABLE_ARCHS,
        required=True,
        help="Build architecture",
    )
    args = parser.parse_args()

    os.environ["BUILD_TARGET"] = f"{args.target},{args.arch}"

    version, release = parse_upstream()
    src_dir = find_src_dir(".", version, release)
    abs_src_dir = os.path.abspath(src_dir)
    binary_path = os.path.abspath(get_binary_path(src_dir, args.target, args.arch))
    patches = ordered_patches()

    total = len(patches)
    iterations = math.ceil(math.log2(total)) if total > 1 else 1

    print(f"Found {total} patches. Estimated {iterations} iterations.\n")
    for i, p in enumerate(patches):
        print(f"  [{i:>2}] {p}")
    print()

    # Step 0: Build with NO patches to verify the base build passes.
    # If this fails, it's a system/environment error, not a patch issue.
    print("=" * 70)
    print("Step 0: Building UNPATCHED Firefox as a sanity check")
    print("=" * 70)

    with temp_cd(abs_src_dir):
        reset_source(version, release)
        write_mozconfig(args.target, args.arch)

        if not build():
            print("ERROR: Unpatched build failed. This is a system/environment error.")
            sys.exit(1)

    print(f"\nRunning test criteria against unpatched build: {binary_path}")
    passed = test_criteria(binary_path)

    if not passed:
        print("FAIL — test criteria failed on UNPATCHED Firefox.")
        print("This is a system/environment error, not a patch issue.")
        sys.exit(1)

    print("PASS — unpatched Firefox passes test criteria. Proceeding with binary search.\n")

    lo, hi = 0, total - 1
    iteration = 0

    while lo < hi:
        iteration += 1
        mid = (lo + hi) // 2

        print("=" * 70)
        print(f"Iteration {iteration}: testing patches [0..{mid}] (range [{lo}..{hi}])")
        print(f"Applying {mid + 1}/{total} patches, skipping [{mid + 1}..{hi}]")
        print("=" * 70)

        with temp_cd(abs_src_dir):
            reset_source(version, release)
            write_mozconfig(args.target, args.arch)

            if not apply_patches(patches[: mid + 1]):
                print("ERROR: Patches failed to apply. Cannot continue binary search.")
                sys.exit(1)

            if not build():
                print("ERROR: Build failed. Cannot continue binary search.")
                sys.exit(1)

        print(f"\nRunning test criteria against: {binary_path}")
        passed = test_criteria(binary_path)

        if passed:
            print(f"PASS — leak not present with patches [0..{mid}]")
            print(f"  → Culprit is in [{mid + 1}..{hi}]")
            lo = mid + 1
        else:
            print(f"FAIL — leak detected with patches [0..{mid}]")
            print(f"  → Culprit is in [{lo}..{mid}]")
            hi = mid

    print("\n" + "=" * 70)
    print(f"RESULT: The patch introducing the leak is [{lo}]:")
    print(f"  {patches[lo]}")
    print("=" * 70)


if __name__ == "__main__":
    main()
