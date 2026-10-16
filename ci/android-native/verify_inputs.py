#!/usr/bin/env python3
"""Read-only source verification for one explicit Android cloud build cohort.

This does not fetch tools, materialize sources, run a build, or accept old QA
cohort aliases. A later reviewed workflow must implement the separate plan.
"""
import argparse
import hashlib
import json
from pathlib import Path
import stat

COHORT = "current257-auditable-arm64-acquired-lock-v1"


def digest(rows):
    return hashlib.sha256(json.dumps(sorted(rows, key=lambda r: r["path"]),
        ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def checked_path(root, name):
    if (not isinstance(name, str) or not name or "\\" in name
            or any(p in ("", ".", "..") for p in name.split("/"))
            or name.startswith("/") or any(ord(c) < 32 for c in name)):
        raise ValueError("invalid relative input path")
    path = root
    for part in name.split("/"):
        path = path / part
        if path.is_symlink():
            raise ValueError("symlink input refused: " + name)
    info = path.stat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
        raise ValueError("ordinary single-link source required: " + name)
    return path


def verify(repo, inputs):
    if inputs.get("schema") != "luobo.cloud-android.source-inputs.v1" or inputs.get("cohort") != COHORT:
        raise ValueError("explicit current257 Android cohort required")
    rows = inputs["files"]
    if len(rows) != inputs["members"] or len({r["path"] for r in rows}) != len(rows):
        raise ValueError("duplicate/missing source rows")
    if any(set(r) != {"path", "bytes", "sha256"} for r in rows) or digest(rows) != inputs["source_digest"]:
        raise ValueError("source digest mismatch")
    for row in rows:
        path = checked_path(repo, row["path"])
        if path.stat().st_size != row["bytes"]:
            raise ValueError("source size mismatch: " + row["path"])
        h = hashlib.sha256()
        with path.open("rb") as f:
            for block in iter(lambda: f.read(65536), b""):
                h.update(block)
        if h.hexdigest() != row["sha256"]:
            raise ValueError("source SHA256 mismatch: " + row["path"])
    return {"cohort": COHORT, "state": "readonly_source_verified_not_a_build",
            "members": len(rows), "source_digest": inputs["source_digest"],
            "native_builds": 0, "runtime_operations": 0}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--cohort", required=True, choices=[COHORT])
    p.add_argument("--repo", type=Path, required=True)
    args = p.parse_args()
    root = args.repo.absolute()
    if root.is_symlink() or root.resolve() != root or not root.is_dir():
        p.error("canonical repository directory required")
    data = json.loads(Path(__file__).with_name("inputs.json").read_text())
    print(json.dumps(verify(root, data), sort_keys=True))


if __name__ == "__main__":
    main()
