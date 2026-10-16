#!/usr/bin/env bash
# No local integration credentials/services are provisioned by this job.
set -uo pipefail

module=${1:-}
case "$module" in
  server|chart|route) ;;
  *) printf '%s\n' 'Expected exactly one module: server, chart or route.' >&2; exit 2 ;;
esac
repo_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
result_dir="$repo_dir/ci-results/$module"
mkdir -p "$result_dir"
export GOWORK=off GOTOOLCHAIN=local
cd "$repo_dir/$module" || exit 2

# Only the public, empty-secret example is uploaded. The ephemeral CI copy is
# created after the separate upload-safety job; no local runtime YAML is shipped.
if [[ $module == server || $module == chart ]]; then
  if [[ -e config/config.yaml || -L config/config.yaml ]]; then
    printf '%s\n' 'Refusing an existing runtime config in the upload snapshot.' >&2
    exit 2
  fi
  cp config/config.example.yaml config/config.yaml || exit 2
fi

# Keep both commands independent: a test failure must not suppress vet or logs.
go test -race -json -timeout=15m ./... >"$result_dir/tests.jsonl" 2>"$result_dir/tests.stderr.log"
test_status=$?
go vet ./... >"$result_dir/vet.log" 2>&1
vet_status=$?

python3 - "$result_dir" "$module" "$test_status" "$vet_status" <<'PY'
import collections
import json
from pathlib import Path
import re
import sys

output = Path(sys.argv[1])
module = sys.argv[2]
test_status, vet_status = map(int, sys.argv[3:5])
counts = collections.Counter()
skips = []
invalid_lines = 0
events = 0
failures = []
with (output / "tests.jsonl").open(encoding="utf-8") as stream:
    for line in stream:
        if not line.strip():
            continue
        try:
            event = json.loads(line)
        except (ValueError, UnicodeError):
            invalid_lines += 1
            continue
        if not isinstance(event, dict):
            invalid_lines += 1
            continue
        events += 1
        action, test = event.get("Action"), event.get("Test")
        if action not in {"pass", "fail", "skip"}:
            continue
        kind = "subtests" if isinstance(test, str) and "/" in test else "top_tests" if test else "packages"
        counts[kind + "_" + action] += 1
        if action == "fail":
            failures.append({"action": action, "package": event.get("Package"), "test": test})
        if action == "skip" and test:
            skips.append({"package": event.get("Package"), "test": test})
summary = {
    "module": module,
    "test_exit_code": test_status,
    "vet_exit_code": vet_status,
    "json_events": events,
    "invalid_json_lines": invalid_lines,
    "counts": dict(sorted(counts.items())),
    "actual_skipped_tests": skips,
    "integration_services_or_credentials_provisioned": False,
    "integration_coverage": "Skipped environment-dependent tests are not verified integration passes.",
}
(output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")

def safe_console(text):
    # CI has no private runtime configuration. Still redact diagnostic values and
    # prefix every line so test output cannot issue GitHub workflow commands.
    text = re.sub(r"-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----.*?(?:-----END (?:[A-Z0-9]+ )*PRIVATE KEY-----|\Z)",
                  "[REDACTED_PRIVATE_KEY]", text, flags=re.S)
    text = re.sub(r"(?<![A-Za-z0-9_-])eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{16,}",
                  "[REDACTED_JWT]", text)
    text = re.sub(r"\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9-]{12,}|(?:AKIA|ASIA)[A-Z0-9]{16}|AKID[A-Za-z0-9]{24,40})\b",
                  "[REDACTED_PROVIDER_CREDENTIAL]", text)
    text = re.sub(r"([A-Za-z][A-Za-z0-9+.-]*://)[^\s/@]+(?::[^\s/@]*)?@",
                  r"\1[REDACTED]@", text)
    text = re.sub(r"(?i)(\b[A-Za-z0-9_.-]*(?:password|passwd|secret|token|private.?key|encryption.?key|access.?key|api.?key|app.?key|authorization)[A-Za-z0-9_.-]*[\"']?\s*[:=]\s*)(?:\"[^\"]*\"|'[^']*'|[^\s,;]+)",
                  r"\1[REDACTED]", text)
    text = re.sub(r"[A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}",
                  "[REDACTED_EMAIL]", text)
    text = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", text)
    text = "".join(character for character in text if character in "\n\t" or ord(character) >= 32)
    return text


def console_block(label, text, max_lines=160):
    lines = safe_console(text).splitlines()
    print("[diagnostic] " + label)
    if len(lines) > max_lines:
        print("[diagnostic] [earlier lines omitted; full file is in the CI artifact]")
    for line in lines[-max_lines:]:
        print("[diagnostic] " + line[:2000])


console_block("Actual test/vet summary", json.dumps(summary, indent=2))
if test_status or vet_status or invalid_lines or not events:
    console_block("Failed JSON events", json.dumps(failures, indent=2))
    failed_packages = {item["package"] for item in failures}
    failed_tests = {(item["package"], item["test"]) for item in failures if item["test"]}
    relevant_output = collections.deque(maxlen=160)
    with (output / "tests.jsonl").open(encoding="utf-8", errors="replace") as stream:
        for line in stream:
            try:
                event = json.loads(line)
            except (ValueError, UnicodeError):
                continue
            if not isinstance(event, dict) or event.get("Action") != "output":
                continue
            key = (event.get("Package"), event.get("Test"))
            if key in failed_tests or (not key[1] and key[0] in failed_packages):
                relevant_output.append(str(event.get("Output", "")))
    console_block("Failing test/package output", "".join(relevant_output))
    for filename in ("tests.stderr.log", "vet.log"):
        # Limit console volume while retaining the unchanged complete artifact.
        with (output / filename).open(encoding="utf-8", errors="replace") as stream:
            tail = collections.deque(stream, maxlen=160)
        console_block(filename, "".join(tail))
sys.exit(0 if test_status == vet_status == invalid_lines == 0 and events > 0 else 1)
PY
summary_status=$?
if [[ $test_status -ne 0 || $vet_status -ne 0 || $summary_status -ne 0 ]]; then
  exit 1
fi
