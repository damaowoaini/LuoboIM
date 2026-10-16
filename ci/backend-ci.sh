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
import sys

output = Path(sys.argv[1])
module = sys.argv[2]
test_status, vet_status = map(int, sys.argv[3:5])
counts = collections.Counter()
skips = []
invalid_lines = 0
events = 0
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
print(json.dumps(summary, indent=2))
sys.exit(0 if test_status == vet_status == invalid_lines == 0 and events > 0 else 1)
PY
summary_status=$?
if [[ $test_status -ne 0 || $vet_status -ne 0 || $summary_status -ne 0 ]]; then
  exit 1
fi
