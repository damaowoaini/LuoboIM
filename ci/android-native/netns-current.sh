#!/usr/bin/env bash
set -euo pipefail
[[ $# == 4 && $EUID == 0 ]] || exit 2
owner=$1
repo=$2
work=$3
stage=$4
[[ "$repo" == /* && "$work" == /* && -d "$repo" && -d "$work" ]]
[[ "$stage" == core || "$stage" == preseal || "$stage" == apk ]]
ip link set lo up
[[ $(ip -o link show | wc -l) == 1 ]]
[[ -z $(ip -4 route show default) && -z $(ip -6 route show default) ]]
exec runuser -u "$owner" -- python3 -B "$repo/ci/android-native/current_app.py" \
  --cohort current354-auditable-arm64-production-v1 --stage "$stage" --work "$work"
