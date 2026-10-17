#!/usr/bin/env bash
set -euo pipefail
[[ $# == 5 && $EUID == 0 ]] || exit 2
owner=$1
repo=$2
work=$3
stage=$4
approved=$5
[[ "$approved" =~ ^[0-9a-f]{64}$ ]]
[[ "$repo" == /* && "$work" == /* && -d "$repo" && -d "$work" ]]
[[ "$stage" == preseal || "$stage" == apk ]]
ip link set lo up
[[ $(ip -o link show | wc -l) == 1 ]]
[[ -z $(ip -4 route show default) && -z $(ip -6 route show default) ]]
exec runuser -u "$owner" -- python3 -B "$repo/ci/android-current359/run.py" \
  --cohort current359-immutable-core-reuse-v1 --stage "$stage" --work "$work" --approved-context-sha256 "$approved"
