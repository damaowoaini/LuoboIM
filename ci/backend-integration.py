#!/usr/bin/env python3
"""Evidence for the disposable CI fixture; never connects to a database itself."""
import collections
import hashlib
import json
import os
from pathlib import Path
import re
import sys


HERE = Path(__file__).resolve().parent
CONTRACT = json.loads((HERE / "backend-integration.json").read_text())
SCOPES = {row["package"]: row for row in CONTRACT["scopes"]}


def write_json(path, value):
    data = (json.dumps(value, indent=2) + "\n").encode()
    fd = os.open(str(path), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as stream:
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())
    fd = os.open(str(path.parent), os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def pins():
    return {name: hashlib.sha256((HERE / name).read_bytes()).hexdigest()
            for name in ("backend-integration.sh", "backend-integration.py", "backend-integration.json")}


def safe(text):
    text = re.sub(r"-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----.*?(?:-----END (?:[A-Z0-9]+ )*PRIVATE KEY-----|\Z)",
                  "[REDACTED_PRIVATE_KEY]", text, flags=re.S)
    text = re.sub(r"(?<![A-Za-z0-9_-])eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{16,}", "[REDACTED_JWT]", text)
    text = re.sub(r"\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9-]{12,}|(?:AKIA|ASIA)[A-Z0-9]{16}|AKID[A-Za-z0-9]{24,40})\b", "[REDACTED_CREDENTIAL]", text)
    text = re.sub(r"([A-Za-z][A-Za-z0-9+.-]*://)[^\s/@]+(?::[^\s/@]*)?@", r"\1[REDACTED]@", text)
    text = re.sub(r"(?i)(\bauthorization[\"']?\s*[:=]\s*)(?:Bearer|Basic)\s+[^\s,;]+", r"\1[REDACTED]", text)
    text = re.sub(r"(?i)(\b[A-Za-z0-9_.-]*(?:password|passwd|secret|token|private.?key|encryption.?key|access.?key|api.?key|app.?key)[A-Za-z0-9_.-]*[\"']?\s*[:=]\s*)(?:\"[^\"]*\"|'[^']*'|[^\s,;]+)", r"\1[REDACTED]", text)
    text = re.sub(r"[A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}", "[REDACTED_EMAIL]", text)
    text = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", text)
    return "".join(c for c in text if c in "\n\t" or ord(c) >= 32)


def console(label, text):
    print("[integration] " + label)
    lines = safe(text).splitlines()
    if len(lines) > 160:
        print("[integration] [earlier lines omitted; complete CI artifact retained]")
    for line in lines[-160:]:
        print("[integration] " + line[:2000])


def summarize(output, package, test_exit, vet_exit):
    counts, terminal = collections.Counter(), {}
    skips, failures, invalid, event_count = [], [], 0, 0
    expected_package = "github.com/openimsdk/open-im-server/v3/" + package[2:]
    package_matches, package_terminal = True, None
    with (output / "tests.jsonl").open(encoding="utf-8", errors="replace") as stream:
        for line in stream:
            if not line.strip():
                continue
            try:
                event = json.loads(line)
                if not isinstance(event, dict):
                    raise ValueError()
            except ValueError:
                invalid += 1
                continue
            event_count += 1
            action, test = event.get("Action"), event.get("Test")
            if event.get("Package") and event["Package"] != expected_package:
                package_matches = False
            if action not in {"pass", "skip", "fail"}:
                continue
            kind = "subtests" if test and "/" in test else "top_tests" if test else "packages"
            counts[kind + "_" + action] += 1
            if test:
                terminal[test] = action
            else:
                package_terminal = action
            row = {"package": event.get("Package"), "test": test}
            if action == "skip" and test:
                skips.append(row)
            if action == "fail":
                failures.append(row)
    required = SCOPES[package]["required_integration_top_tests"]
    unmet = [{"test": name, "actual_terminal_action": terminal.get(name, "missing")}
             for name in required if terminal.get(name) != "pass"]
    required_sub_skips = [row for row in skips if row["test"].split("/", 1)[0] in required]
    accepted = (test_exit == vet_exit == invalid == 0 and event_count > 0 and package_matches and
                package_terminal == "pass" and counts["packages_pass"] == 1 and
                not unmet and not required_sub_skips and not failures)
    report = {"state": "accepted" if accepted else "failed_or_incomplete", "package": package,
              "test_exit_code": test_exit, "vet_exit_code": vet_exit, "json_events": event_count,
              "invalid_json_lines": invalid, "counts": dict(sorted(counts.items())),
              "package_import_matches": package_matches, "package_terminal_action": package_terminal,
              "required_integration_top_tests": len(required), "required_unmet": unmet,
              "required_subtest_skips": required_sub_skips, "actual_skipped_tests": skips,
              "failures": failures, "skips_are_integration_passes": False}
    write_json(output / "summary.json", report)
    console("actual package summary", json.dumps(report, indent=2))
    if not accepted:
        failed_packages = {row["package"] for row in failures}
        failed_tests = {(row["package"], row["test"]) for row in failures if row["test"]}
        tail = collections.deque(maxlen=160)
        with (output / "tests.jsonl").open(encoding="utf-8", errors="replace") as stream:
            for line in stream:
                try:
                    event = json.loads(line)
                except ValueError:
                    continue
                if not isinstance(event, dict) or event.get("Action") != "output":
                    continue
                key = (event.get("Package"), event.get("Test"))
                if key in failed_tests or (not key[1] and key[0] in failed_packages):
                    tail.append(str(event.get("Output", "")))
        console("failing test/package output", "".join(tail))
        for name in ("tests.stderr.log", "vet.log"):
            with (output / name).open(encoding="utf-8", errors="replace") as stream:
                console(name, "".join(collections.deque(stream, maxlen=160)))
    return 0 if accepted else 1


def main():
    action = sys.argv[1]
    if action == "packages":
        print("\n".join(SCOPES))
        return 0
    output = Path(sys.argv[2])
    if action == "ready":
        initial = json.loads((output / "mongo-initial.json").read_text())
        if initial != {"setName": "luoboIntegration", "isWritablePrimary": True,
                       "version": CONTRACT["mongo"]["version"], "userDatabases": 0}:
            raise ValueError()
        for engine in ("mongo", "redis"):
            digest = CONTRACT[engine]["image"].split("@", 1)[1]
            repositories = json.loads((output / (engine + "-image.json")).read_text())
            if not any(value.endswith("@" + digest) for value in repositories):
                raise ValueError()
        write_json(output / "fixture-ready.json", {"state": "fresh_primary_and_redis_ready",
                   "run_label": sys.argv[3], "commit": sys.argv[4], "script_pins": pins(),
                   "mongo_image": CONTRACT["mongo"]["image"], "redis_image": CONTRACT["redis"]["image"],
                   "replica_set": "luoboIntegration", "loopback_only": True,
                   "real_credentials_or_imported_data": False})
        return 0
    if action == "check-ready":
        ready = json.loads((output / "fixture-ready.json").read_text())
        if (ready["state"] != "fresh_primary_and_redis_ready" or ready["run_label"] != sys.argv[3] or
                ready["commit"] != sys.argv[4] or ready["script_pins"] != pins()):
            raise ValueError()
        return 0
    if action == "summary":
        return summarize(output, sys.argv[3], int(sys.argv[4]), int(sys.argv[5]))
    if action == "aggregate":
        reports, counts = [], collections.Counter()
        for package in SCOPES:
            report = json.loads((output / package[2:].replace("/", "_") / "summary.json").read_text())
            if report["package"] != package:
                raise ValueError()
            counts.update(report["counts"])
            reports.append(report)
        accepted = all(report["state"] == "accepted" for report in reports)
        result = {"state": "tests_and_vet_accepted" if accepted else "failed_or_incomplete",
                  "packages": len(reports), "counts": dict(sorted(counts.items())),
                  "required_integration_top_tests": sum(len(row["required_integration_top_tests"]) for row in SCOPES.values()),
                  "package_summaries": reports, "skips_are_integration_passes": False,
                  "residual_and_container_cleanup_checks_are_separate": True}
        write_json(output / "summary.json", result)
        console("actual aggregate summary", json.dumps({k: v for k, v in result.items() if k != "package_summaries"}, indent=2))
        return 0 if accepted else 1
    if action == "residuals":
        databases = json.loads((output / "mongo-final.json").read_text())["userDatabases"]
        observed_size = int((output / "redis-final-count.txt").read_text().strip())
        keyspace = {}
        header_count = 0
        for line in (output / "redis-final-keyspace.txt").read_text().splitlines():
            line = line.strip()
            if line == "# Keyspace":
                header_count += 1
            if not line or line.startswith("#"):
                continue
            match = re.fullmatch(r"db([0-9]+):(.+)", line)
            if not match or int(match[1]) in keyspace:
                raise ValueError()
            fields = {}
            for pair in match[2].split(","):
                key, value = pair.split("=", 1)
                if key in fields or not re.fullmatch(r"[0-9]+", value):
                    raise ValueError()
                fields[key] = int(value)
            if not {"keys", "expires", "avg_ttl"}.issubset(fields):
                raise ValueError()
            keyspace[int(match[1])] = fields
        if header_count != 1:
            raise ValueError()
        current = keyspace.get(15, {"keys": 0, "expires": 0, "avg_ttl": 0})
        # Both keys/expires come from one INFO snapshot. DBSIZE is diagnostic:
        # expiration may lower the count before INFO, but no new writer is allowed.
        accepted = (databases == [] and observed_size >= current["keys"] >= 0 and
                    current["keys"] == current["expires"] and set(keyspace).issubset({15}))
        state = "no_fixture_residue" if current["keys"] == 0 else "expiring_cache_residue_pending_container_disposal"
        result = {"state": state if accepted else "fixture_cleanup_incomplete",
                  "mongo_user_database_count": len(databases),
                  "redis_db15_dbsize_before_info": observed_size,
                  "redis_db15_key_count": current["keys"], "redis_db15_expiring_keys": current["expires"],
                  "redis_db15_average_ttl_ms": current["avg_ttl"],
                  "unexpected_redis_database_count": len(set(keyspace) - {15}),
                  "values_or_documents_read": False,
                  "exact_per_key_cleanup_claimed": False,
                  "cleanup_authority": "exact_owned_containers_only_in_separate_cleanup_step",
                  "global_flush_drop_or_retry_attempted": False}
        write_json(output / "residual-check.json", result)
        console("post-test fixture residual counts", json.dumps(result))
        return 0 if accepted else 1
    raise ValueError()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, KeyError, IndexError, TypeError):
        # No raw exception text: it could contain a test credential or document.
        print("[integration] evidence_or_fixture_contract_failed", file=sys.stderr)
        sys.exit(2)
