#!/usr/bin/env bash
set -euo pipefail
test "$#" -eq 3
actor="$1"
repo="$2"
work="$3"
test "$(id -u)" = 0
test -d "$repo/ci/android-jvm-projection"
test -f "$work/evidence/acquired-catalog.json"
real_user_home="$(getent passwd "$actor" | cut -d: -f6)"
test -n "$real_user_home"
test -d "$real_user_home"
ip link set lo up
exec setpriv --reuid="$(id -u "$actor")" --regid="$(id -g "$actor")" --init-groups \
  env -i PATH=/usr/bin:/bin LANG=C.UTF-8 LC_ALL=C.UTF-8 HOME="$real_user_home" \
  python3 -B "$repo/ci/android-jvm-projection/run.py" \
  --cohort android-rich-jvm-context-projection-r1 --stage offline --work "$work"
