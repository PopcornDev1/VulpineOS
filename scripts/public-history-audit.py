#!/usr/bin/env python3

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parent.parent
WORKSPACE_ROOT = REPO_ROOT.parent

PUBLIC_REPOS = [
    ("VulpineOS", REPO_ROOT),
    ("vulpine-mark", WORKSPACE_ROOT / "vulpine-mark"),
    ("mobilebridge", WORKSPACE_ROOT / "mobilebridge"),
    ("foxbridge", WORKSPACE_ROOT / "foxbridge"),
    ("vulpineos-docs", WORKSPACE_ROOT / "vulpineos-docs"),
]

EXCLUDE_SPECS = [
    ":(exclude)go.sum",
    ":(exclude)scripts/public-boundary-audit.sh",
    ":(exclude)scripts/public-history-audit.py",
    ":(glob,exclude)**/node_modules/**",
    ":(glob,exclude)**/vendor/**",
    ":(glob,exclude)**/dist/**",
    ":(glob,exclude)**/build/**",
    ":(glob,exclude)**/.next/**",
    ":(glob,exclude)**/coverage/**",
    ":(glob,exclude)**/.turbo/**",
    ":(glob,exclude)**/public/llms.txt",
    ":(glob,exclude)**/public/llms-full.txt",
    ":(glob,exclude)**/docs/public/llms.txt",
    ":(glob,exclude)**/docs/public/llms-full.txt",
]

PRIVATE_DOCS_DIR = "." + "claude" + "/private-docs/"
PRIVATE_DOCS_DIR_WINDOWS = "." + "claude" + "\\private-docs\\"
PUBLIC_REVSET = ["--remotes=origin", "--tags"]

MESSAGE_PATTERNS = [
    ("private plan docs", r"\.claude/private-docs(?:/|\\)"),
    ("private repos", r"github\.com/VulpineOS/(vulpine-private|vulpine-api)(?:\b|/)"),
    (
        "high-confidence secret token",
        r"ghp_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{20,}|lin_api_[A-Za-z0-9]{20,}|xox[pbar]-[A-Za-z0-9-]{20,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35}|sk-(proj-)?[A-Za-z0-9]{20,}",
    ),
]

DIFF_PATTERNS = [
    ("private plan docs", r"\.claude/private-docs", r"\.claude/private-docs(?:/|\\)"),
    (
        "private repos",
        r"github\.com/VulpineOS/(vulpine-private|vulpine-api)",
        r"github\.com/VulpineOS/(vulpine-private|vulpine-api)(?:\b|/)",
    ),
    (
        "macOS absolute path",
        r"/Users/[A-Za-z0-9._-]+/",
        r"(^|[^A-Za-z0-9_])/Users/(?!<user>|<username>|example/|name/|runner/)[A-Za-z0-9._-]+/",
    ),
    (
        "Linux absolute path",
        r"/home/[A-Za-z0-9._-]+/",
        r"(^|[^A-Za-z0-9_])/home/(?!<user>|<username>|example/|name/|appveyor/|runner/|runneradmin/|ubuntu/|vsts/)[A-Za-z0-9._-]+/",
    ),
    (
        "Windows absolute path",
        r"[A-Za-z]:\\\\Users\\\\[^\\\\]+\\\\",
        r"(^|[^A-Za-z0-9_])[A-Za-z]:\\\\Users\\\\(?!<user>|<username>|example\\\\|name\\\\)[^\\\\\\s]+\\\\",
    ),
    (
        "high-confidence secret token",
        r"ghp_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{20,}|lin_api_[A-Za-z0-9]{20,}|xox[pbar]-[A-Za-z0-9-]{20,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35}|sk-(proj-)?[A-Za-z0-9]{20,}",
        r"ghp_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{20,}|lin_api_[A-Za-z0-9]{20,}|xox[pbar]-[A-Za-z0-9-]{20,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35}|sk-(proj-)?[A-Za-z0-9]{20,}",
    ),
]

KNOWN_DIFF_HISTORY_FINDINGS = {
    (
        "VulpineOS",
        "macOS absolute path",
        "4fa51e3107bae4fab9c6e0339c445043f3536545",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "macOS absolute path",
        "8348293b4c9b0221e129b2d7e9258787f1856ba0",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "macOS absolute path",
        "22398d3476c796d91260fc6588c0c2a5ec942dc9",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "macOS absolute path",
        "33c0a7b51a105a5c868adfab037f0a2908b1a915",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "macOS absolute path",
        "c2aa90fa985f0c79f812062bea018d2542feb435",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "macOS absolute path",
        "e5d8b934c8461b8298020a648bd919eeb66fe035",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "macOS absolute path",
        "4c541522a749ba0f723be55cbce6400192ba8981",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "macOS absolute path",
        "2fc4c4f105fe22d32045907a8716f687cb320e2a",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "macOS absolute path",
        "88743c0b5f166def7554d3c8a6767e4d4b701f91",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "macOS absolute path",
        "f38e5eceb446fbee36d04dc4a258ffbebabb8633",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "macOS absolute path",
        "f2eed2a5946b68c63cfd3e681a3a71cfc3864a6a",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "macOS absolute path",
        "36212ffa5488fbb87eacc2c9eba4d3f74bc7e1a7",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "macOS absolute path",
        "3092fd279bd96043c072e4178e9e51b3fd1dbf15",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "macOS absolute path",
        "727f69480d2063d8b1b42d54518b261f03c0085d",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "macOS absolute path",
        "2f669b136646ae17ba398d53cab4fcb698213e5c",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "Linux absolute path",
        "6f56cebe42820e3ac7182357c00283f83066d107",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "Linux absolute path",
        "261bedd0eab892e351cb033714dc40042eab9c30",
    ): "reviewed historical absolute path removal",
    (
        "VulpineOS",
        "Linux absolute path",
        "2c9d59f8e0564022d79fdea34e86403d26f547a9",
    ): "reviewed historical absolute path removal",
}


def run(repo: Path, args: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", "-C", str(repo), *args],
        text=True,
        capture_output=True,
        check=False,
    )


def print_fail(message: str, details: str | None = None) -> None:
    print(f"FAIL: {message}")
    if details:
        print(details.rstrip())


def known_diff_history_finding(name: str, description: str, commit: str) -> str | None:
    return KNOWN_DIFF_HISTORY_FINDINGS.get((name, description, commit))


def audit_commit_messages(name: str, repo: Path) -> int:
    proc = run(repo, ["log", *PUBLIC_REVSET, "--format=%H%x00%B%x00==END=="])
    if proc.returncode != 0:
        print_fail(f"{name}: unable to read commit history", proc.stderr)
        return 1

    failures = 0
    text = proc.stdout
    for description, pattern in MESSAGE_PATTERNS:
        regex = re.compile(pattern, re.MULTILINE)
        match = regex.search(text)
        if not match:
            continue
        prefix = text[: match.start()]
        parts = prefix.split("\x00")
        commit = parts[-2].strip() if len(parts) >= 2 else "<unknown>"
        snippet = text[match.start() : match.start() + 160].splitlines()[0]
        print_fail(f"{name}: commit message matched {description} in {commit}", snippet)
        failures += 1
    return failures


def audit_path_history(name: str, repo: Path) -> int:
    proc = run(repo, ["log", *PUBLIC_REVSET, "--name-only", "--format=commit:%H"])
    if proc.returncode != 0:
        print_fail(f"{name}: unable to read path history", proc.stderr)
        return 1

    failures = 0
    lines = proc.stdout.splitlines()
    current_commit = "<unknown>"
    for line in lines:
        if line.startswith("commit:"):
            current_commit = line.split(":", 1)[1]
            continue
        if not line:
            continue
        if PRIVATE_DOCS_DIR in line or PRIVATE_DOCS_DIR_WINDOWS in line:
            print_fail(f"{name}: history contains private plan doc path in {current_commit}", line)
            failures += 1
            break
    return failures


def audit_diff_history(name: str, repo: Path) -> int:
    failures = 0
    for description, pickaxe_pattern, strict_pattern in DIFF_PATTERNS:
        args = [
            "log",
            *PUBLIC_REVSET,
            "--pickaxe-regex",
            "-S",
            pickaxe_pattern,
            "--format=%H",
            "--",
            ".",
            *EXCLUDE_SPECS,
        ]
        proc = run(repo, args)
        if proc.returncode not in (0, 1):
            print_fail(f"{name}: unable to scan diff history for {description}", proc.stderr)
            failures += 1
            continue
        commits = [line.strip() for line in proc.stdout.splitlines() if line.strip()]
        if not commits:
            continue
        strict_regex = re.compile(strict_pattern, re.MULTILINE)
        for commit in commits:
            show_args = [
                "show",
                "--format=",
                commit,
                "--",
                ".",
                *EXCLUDE_SPECS,
            ]
            show_proc = run(repo, show_args)
            if show_proc.returncode != 0:
                print_fail(f"{name}: unable to inspect commit {commit} for {description}", show_proc.stderr)
                failures += 1
                break
            if not strict_regex.search(show_proc.stdout):
                continue
            if reason := known_diff_history_finding(name, description, commit):
                print(f"INFO: {name}: allowing {description} in {commit} ({reason})")
                continue
            print_fail(f"{name}: diff history matched {description}", commit)
            failures += 1
            continue
    return failures


def main() -> int:
    failures = 0
    for name, repo in PUBLIC_REPOS:
        print(f"INFO: Auditing history for {name}")
        if not (repo / ".git").exists():
            print_fail(f"{name}: repo not found at {repo}")
            failures += 1
            continue
        failures += audit_commit_messages(name, repo)
        failures += audit_path_history(name, repo)
        failures += audit_diff_history(name, repo)

    if failures:
        print(f"\nHistory audit failed with {failures} finding(s).")
        return 1

    print("\nHistory audit passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
