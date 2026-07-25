#!/usr/bin/env python3
"""Build a deployment PR body from application and GitOps diffs."""

from __future__ import annotations

import argparse
import difflib
import re
import subprocess
import sys
from pathlib import Path


ENV_PATTERNS = (
    re.compile(r"os\.(?:Getenv|LookupEnv)\(\s*[\"']([A-Z][A-Z0-9_]*)[\"']"),
    re.compile(r"process\.env\.([A-Z][A-Z0-9_]*)"),
    re.compile(r"(?:Deno\.env\.get|System\.getenv)\(\s*[\"']([A-Z][A-Z0-9_]*)[\"']"),
    re.compile(r"std::env::var\(\s*[\"']([A-Z][A-Z0-9_]*)[\"']"),
    re.compile(r"ENV\[\s*[\"']([A-Z][A-Z0-9_]*)[\"']\s*\]"),
)
SOURCE_SUFFIXES = {
    ".c", ".cc", ".cpp", ".go", ".java", ".js", ".jsx", ".kt",
    ".py", ".rb", ".rs", ".sh", ".ts", ".tsx",
}


def run(repo: Path, args: list[str], *, binary: bool = False) -> str | bytes:
    result = subprocess.run(
        ["git", "-C", str(repo), *args],
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    return result.stdout if binary else result.stdout.decode("utf-8", errors="replace")


def resolve(repo: Path, revision: str) -> str:
    return str(run(repo, ["rev-parse", f"{revision}^{{commit}}"])).strip()


def github_slug(repo: Path) -> str | None:
    try:
        remote = str(run(repo, ["config", "--get", "remote.origin.url"])).strip()
    except subprocess.CalledProcessError:
        return None
    patterns = (
        r"^git@github\.com:([^/]+/[^/]+?)(?:\.git)?$",
        r"^https?://github\.com/([^/]+/[^/]+?)(?:\.git)?/?$",
        r"^ssh://git@github\.com/([^/]+/[^/]+?)(?:\.git)?/?$",
    )
    for pattern in patterns:
        match = re.match(pattern, remote)
        if match:
            return match.group(1)
    return None


def truncate_at_line(text: str, limit: int) -> tuple[str, bool]:
    if len(text) <= limit:
        return text, False
    kept: list[str] = []
    size = 0
    for line in text.splitlines(keepends=True):
        if size + len(line) > limit:
            break
        kept.append(line)
        size += len(line)
    return "".join(kept), True


def tree_env_names(repo: Path, revision: str) -> set[str]:
    paths_raw = run(repo, ["ls-tree", "-r", "--name-only", "-z", revision], binary=True)
    assert isinstance(paths_raw, bytes)
    names: set[str] = set()
    for raw_path in paths_raw.split(b"\0"):
        if not raw_path:
            continue
        relative = raw_path.decode("utf-8", errors="surrogateescape")
        if Path(relative).suffix.lower() not in SOURCE_SUFFIXES:
            continue
        try:
            content = str(run(repo, ["show", f"{revision}:{relative}"]))
        except subprocess.CalledProcessError:
            continue
        for pattern in ENV_PATTERNS:
            names.update(pattern.findall(content))
    return names


def untracked_diff(repo: Path) -> str:
    raw_paths = run(repo, ["ls-files", "--others", "--exclude-standard", "-z"], binary=True)
    assert isinstance(raw_paths, bytes)
    chunks: list[str] = []
    for raw_path in raw_paths.split(b"\0"):
        if not raw_path:
            continue
        relative = raw_path.decode("utf-8", errors="surrogateescape")
        path = repo / relative
        try:
            raw = path.read_bytes()
        except OSError:
            continue
        if b"\0" in raw:
            chunks.append(f"diff --git a/{relative} b/{relative}\nBinary file added\n")
            continue
        lines = raw.decode("utf-8", errors="replace").splitlines()
        patch = difflib.unified_diff(
            [], lines, fromfile="/dev/null", tofile=f"b/{relative}", lineterm=""
        )
        chunks.append(
            f"diff --git a/{relative} b/{relative}\nnew file mode 100644\n"
            + "\n".join(patch)
            + "\n"
        )
    return "".join(chunks)


def deployment_worktree_diff(repo: Path) -> str:
    tracked = str(run(repo, ["diff", "--no-ext-diff", "--unified=3", "HEAD"]))
    return tracked + untracked_diff(repo)


def deployment_env_changes(diff: str) -> tuple[set[str], set[str]]:
    added: set[str] = set()
    removed: set[str] = set()
    pattern = re.compile(r"^[+-]\s*-\s*name:\s*([A-Z][A-Z0-9_]*)\s*$")
    for line in diff.splitlines():
        match = pattern.match(line)
        if match:
            (added if line.startswith("+") else removed).add(match.group(1))
    return added - removed, removed - added


def bullets(values: list[str]) -> str:
    return "\n".join(f"- {value}" for value in values)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--app-repo", required=True, type=Path)
    parser.add_argument("--base", required=True)
    parser.add_argument("--target", required=True)
    parser.add_argument("--old-image", required=True)
    parser.add_argument("--new-image", required=True)
    parser.add_argument("--deployment-repo", required=True, type=Path)
    parser.add_argument("--build-url", required=True)
    parser.add_argument("--environment-note", action="append", required=True)
    parser.add_argument("--note", action="append", required=True)
    parser.add_argument("--verification", action="append", required=True)
    parser.add_argument("--max-app-diff-chars", type=int, default=32_000)
    parser.add_argument("--max-deployment-diff-chars", type=int, default=12_000)
    parser.add_argument("--output", type=Path)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    app_repo = args.app_repo.resolve()
    deployment_repo = args.deployment_repo.resolve()
    try:
        base = resolve(app_repo, args.base)
        target = resolve(app_repo, args.target)
        commits = str(run(
            app_repo,
            ["log", "--reverse", "--pretty=format:- `%h` %s", f"{base}..{target}"],
        )).strip()
        diffstat = str(run(app_repo, ["diff", "--stat", base, target])).strip()
        app_diff = str(run(
            app_repo, ["diff", "--no-ext-diff", "--unified=3", base, target]
        ))
        deployment_diff = deployment_worktree_diff(deployment_repo)
        old_env = tree_env_names(app_repo, base)
        new_env = tree_env_names(app_repo, target)
    except subprocess.CalledProcessError as exc:
        detail = exc.stderr.decode("utf-8", errors="replace").strip()
        print(f"error: git command failed: {detail or exc}", file=sys.stderr)
        return 2

    if not deployment_diff.strip():
        print("error: deployment repository has no worktree diff", file=sys.stderr)
        return 2

    app_excerpt, app_truncated = truncate_at_line(app_diff, args.max_app_diff_chars)
    deployment_excerpt, deployment_truncated = truncate_at_line(
        deployment_diff, args.max_deployment_diff_chars
    )
    source_slug = github_slug(app_repo)
    compare_url = (
        f"https://github.com/{source_slug}/compare/{base}...{target}"
        if source_slug else None
    )
    app_added_env = sorted(new_env - old_env)
    app_removed_env = sorted(old_env - new_env)
    deploy_added_env, deploy_removed_env = deployment_env_changes(deployment_diff)
    detected_env_notes = [
        "Application runtime env names added: "
        + (", ".join(f"`{name}`" for name in app_added_env) or "none detected"),
        "Application runtime env names removed: "
        + (", ".join(f"`{name}`" for name in app_removed_env) or "none detected"),
        "Kubernetes env entries added: "
        + (", ".join(f"`{name}`" for name in sorted(deploy_added_env)) or "none"),
        "Kubernetes env entries removed: "
        + (", ".join(f"`{name}`" for name in sorted(deploy_removed_env)) or "none"),
    ]
    compare_item = (
        f"- Full application diff: [{base[:7]}...{target[:7]}]({compare_url})"
        if compare_url
        else "- Full application diff: source remote is not a recognized GitHub URL"
    )
    app_truncation = (
        "\n\n> Embedded diff truncated to fit the PR body; use the compare link."
        if app_truncated else ""
    )
    deployment_truncation = (
        "\n\n> Embedded Kubernetes diff truncated; inspect the PR Files tab."
        if deployment_truncated else ""
    )

    body = f"""## Summary

- Promote `{args.old_image}` to `{args.new_image}`.
- Application commits: `{base}` → `{target}`.
- Successful image build: {args.build_url}
{compare_item}

## Application changes

### Commits

{commits or "- No commits found in the selected range"}

### Diffstat

```text
{diffstat or "No application diffstat available"}
```

<details>
<summary>Application commit diff</summary>

````diff
{app_excerpt.rstrip()}
````
{app_truncation}
</details>

## Configuration and deployment notes

### Environment variables

{bullets(detected_env_notes + args.environment_note)}

### Operational impact

{bullets(args.note)}

### Kubernetes manifest diff

<details>
<summary>Exact deployment worktree diff</summary>

````diff
{deployment_excerpt.rstrip()}
````
{deployment_truncation}
</details>

## Verification

{bullets(args.verification)}

## Rollback

Revert this PR (or restore `{args.old_image}`) and allow the existing GitOps
reconciliation process to roll the workload back.
"""
    if len(body) > 64_000:
        print(f"error: generated body is {len(body)} characters", file=sys.stderr)
        return 2
    if args.output:
        args.output.write_text(body, encoding="utf-8")
    else:
        print(body, end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
