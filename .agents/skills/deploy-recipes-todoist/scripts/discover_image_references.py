#!/usr/bin/env python3
"""Find tracked references to a container image without assuming repository layout."""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from pathlib import Path


MAX_FILE_BYTES = 4 * 1024 * 1024
CONTEXT_LINES = 3


def git_tracked_files(repo_root: Path) -> list[str]:
    result = subprocess.run(
        ["git", "-C", str(repo_root), "ls-files", "-z"],
        check=True,
        stdout=subprocess.PIPE,
    )
    return [
        item.decode("utf-8", errors="surrogateescape")
        for item in result.stdout.split(b"\0")
        if item
    ]


def classify(line: str) -> str:
    lowered = line.lower()
    if "image:" in lowered or "image =" in lowered:
        return "image-value"
    if "repository:" in lowered or "repository =" in lowered:
        return "repository-value"
    if "newname:" in lowered or "name:" in lowered:
        return "image-name-or-kustomize"
    return "generic-reference"


def find_matches(repo_root: Path, image: str) -> list[dict[str, object]]:
    needle = image.casefold()
    matches: list[dict[str, object]] = []
    for relative in git_tracked_files(repo_root):
        path = repo_root / relative
        try:
            if not path.is_file() or path.stat().st_size > MAX_FILE_BYTES:
                continue
            raw = path.read_bytes()
        except OSError:
            continue
        if b"\0" in raw:
            continue
        lines = raw.decode("utf-8", errors="replace").splitlines()
        for index, line in enumerate(lines):
            if needle not in line.casefold():
                continue
            start = max(0, index - CONTEXT_LINES)
            end = min(len(lines), index + CONTEXT_LINES + 1)
            matches.append(
                {
                    "path": relative,
                    "line": index + 1,
                    "kind": classify(line),
                    "text": line.strip(),
                    "context": [
                        {"line": number + 1, "text": lines[number]}
                        for number in range(start, end)
                    ],
                }
            )
    return matches


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Search git-tracked text files for a container image repository."
    )
    parser.add_argument("--repo-root", required=True, type=Path)
    parser.add_argument("--image", required=True)
    parser.add_argument("--json", action="store_true")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    repo_root = args.repo_root.resolve()
    if not (repo_root / ".git").exists():
        print(f"error: not a git checkout: {repo_root}", file=sys.stderr)
        return 2
    try:
        matches = find_matches(repo_root, args.image)
    except subprocess.CalledProcessError as exc:
        print(f"error: git ls-files failed: {exc}", file=sys.stderr)
        return 2

    if args.json:
        print(json.dumps({"image": args.image, "matches": matches}, indent=2))
    else:
        print(f"Image repository: {args.image}")
        print(f"Tracked matches: {len(matches)}")
        for match in matches:
            print(f"\n{match['path']}:{match['line']} [{match['kind']}]")
            for context in match["context"]:
                marker = ">" if context["line"] == match["line"] else " "
                print(f"{marker} {context['line']:>5}: {context['text']}")
    return 0 if matches else 1


if __name__ == "__main__":
    raise SystemExit(main())
