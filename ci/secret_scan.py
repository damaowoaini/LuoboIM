#!/usr/bin/env python3
"""Read-only upload gate. Output contains paths, categories and line numbers only.

Run against the fresh upload snapshot, not the private working tree. This is a
bounded rule scanner; it does not certify binary payloads or the absence of every
possible secret. Existing local Git history must not be copied into the snapshot.
"""
import argparse
import json
from pathlib import Path
import re
import stat
import sys


TEXT_LIMIT = 8 * 1024 * 1024
PRIVATE_ROOTS = (
    "docs/handoff/source/", "docs/security/", "docs/verification/",
    "references/integration/stack-config/", "references/sdk-recovery/private-",
)
PRIVATE_COMPONENTS = {
    ".qa", "verification-root", "node_modules", ".dart_tool", ".gradle",
    ".cxx", "Pods", "SourcePackages", "runtime", "stack-config",
}
PRIVATE_SUFFIXES = {
    ".db", ".sqlite", ".sqlite3", ".jks", ".keystore", ".p12", ".pfx",
    ".log", ".dump", ".bundle", ".apk", ".ipa", ".aar", ".so", ".dylib",
    ".zip", ".gz", ".tgz", ".tar",
}
ACTUAL_CONFIGS = {"server/config/config.yaml", "chart/config/config.yaml"}
PATTERNS = (
    ("private_key_material", re.compile(r"-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----")),
    ("jwt_bearer_literal", re.compile(
        r"(?<![A-Za-z0-9_-])eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{16,}")),
    ("cloud_access_key_id", re.compile(
        r"\b(?:AKIA|ASIA)[A-Z0-9]{16}\b|\bAKID[A-Za-z0-9]{24,40}\b")),
    ("provider_auth_token", re.compile(
        r"\bgh[pousr]_[A-Za-z0-9]{30,}\b|\bgithub_pat_[A-Za-z0-9_]{40,}\b|\bxox[baprs]-[A-Za-z0-9-]{20,}")),
)
FIELD = re.compile(r"^\s*[\"']?([A-Za-z_][A-Za-z0-9_.-]*)[\"']?\s*[:=]\s*(.*?)\s*,?\s*$")
SENSITIVE_FIELD = re.compile(
    r"(?:secret|password|passwd|private.?key|aes.*key|encrypt.*key|sign.*key|access.?key|app.?key|api.?key|auth.?key|hmac.*key|token)", re.I)
REFERENCE_FIELD = re.compile(r"(?:path|file|enabled|expire|refresh|timeout|length|seconds)$", re.I)
CONFIG_NAME = re.compile(
    r"(?:^|/)(?:[^/]*config[^/]*\.(?:ya?ml|json|toml)|\.env(?:\.[^/]*)?|[^/]+\.properties)$", re.I)


def configured_literal(value):
    value = value.split(" #", 1)[0].strip().rstrip(",").strip().strip("\"'").strip()
    if value.lower() in {"", "null", "~", "false", "true", "[]", "{}"}:
        return False
    if "${" in value or "{{" in value:
        return False
    return not bool(re.fullmatch(
        r"(?:REPLACE|CHANGE|YOUR|EXAMPLE|PLACEHOLDER|TODO|<)[A-Z_0-9<> -]*", value, re.I))


def path_findings(relative):
    parts = relative.parts
    name = relative.as_posix()
    if any(part.startswith("dist-rollback") for part in parts):
        yield "historical_generated_rollback"
    if any(part in PRIVATE_COMPONENTS for part in parts) or name.startswith(PRIVATE_ROOTS):
        yield "private_or_generated_evidence_path"
    if relative.suffix.lower() in PRIVATE_SUFFIXES:
        yield "private_data_archive_or_unreviewed_native_binary"
    if relative.name == ".env" or (
            relative.name.startswith(".env.") and relative.name not in {".env.example", ".env.template"}):
        yield "local_environment_file"
    if name in ACTUAL_CONFIGS:
        yield "runtime_config_requires_separate_public_example"
    if "archived-before-" in relative.name:
        yield "historical_native_patch_requires_exclusion_or_redaction"


def scan(root):
    findings = set()
    files = 0
    for path in sorted(root.rglob("*")):
        relative = path.relative_to(root)
        if ".git" in relative.parts:
            continue
        info = path.lstat()
        if stat.S_ISLNK(info.st_mode):
            findings.add((relative.as_posix(), 0, "symlink_not_upload_reviewed"))
            continue
        if stat.S_ISDIR(info.st_mode):
            continue
        if not stat.S_ISREG(info.st_mode):
            findings.add((relative.as_posix(), 0, "non_regular_upload_member"))
            continue
        files += 1
        for category in path_findings(relative):
            findings.add((relative.as_posix(), 0, category))
        if info.st_size > TEXT_LIMIT:
            # Large public WASM/assets are not scanned as text. Their provenance
            # and byte pins need a separate reviewed artifact inventory.
            if relative.suffix.lower() not in {".wasm", ".png", ".jpg", ".jpeg", ".webp", ".ico", ".woff2"}:
                findings.add((relative.as_posix(), 0, "oversize_text_or_unreviewed_payload"))
            continue
        raw = path.read_bytes()
        if b"\0" in raw[:8192]:
            continue
        try:
            text = raw.decode("utf-8")
        except UnicodeDecodeError:
            continue
        is_config = bool(CONFIG_NAME.search(relative.as_posix()))
        for number, line in enumerate(text.splitlines(), 1):
            for category, pattern in PATTERNS:
                if pattern.search(line):
                    findings.add((relative.as_posix(), number, category))
            if is_config:
                match = FIELD.match(line)
                if (match and SENSITIVE_FIELD.search(match.group(1)) and
                        not REFERENCE_FIELD.search(match.group(1)) and configured_literal(match.group(2))):
                    findings.add((relative.as_posix(), number, "config_credential_or_crypto_literal_requires_review"))
    return files, [{"path": path, "line": line, "category": category}
                   for path, line, category in sorted(findings)]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path.cwd())
    args = parser.parse_args()
    try:
        root = args.root.absolute()
        if not root.is_dir() or root.is_symlink() or root.resolve() != root:
            raise ValueError("invalid root")
        count, findings = scan(root)
        print(json.dumps({"state": "blocked" if findings else "rule_scan_clean",
                          "files": count, "findings": findings,
                          "secret_values_in_output": False,
                          "history_scanned": False,
                          "binary_secret_absence_certified": False}, ensure_ascii=False, indent=2))
        return 1 if findings else 0
    except (OSError, ValueError, UnicodeError):
        # Never stringify exception messages, which may carry file content.
        print(json.dumps({"state": "scan_failed", "category": "filesystem_or_decode_error"}))
        return 2


if __name__ == "__main__":
    sys.exit(main())
