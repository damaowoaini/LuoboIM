#!/usr/bin/env bash
set -euo pipefail
test "$#" -eq 3
actor="$1"
repo="$2"
work="$3"
test "$(id -u)" = 0
test -d "$repo/ci/android-version-probe"
test -d "$work/evidence"
ip link set lo up
exec setpriv --reuid="$(id -u "$actor")" --regid="$(id -g "$actor")" --init-groups \
  env -i PATH=/usr/bin:/bin LANG=C.UTF-8 LC_ALL=C.UTF-8 HOME="$work/home" \
  python3 -B "$repo/ci/android-version-probe/run.py" \
  --cohort current257-rich-version-provenance-v1 --stage offline --work "$work"
