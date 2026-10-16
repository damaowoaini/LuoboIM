#!/usr/bin/env bash
# Called by `sudo unshare --net`; this namespace contains no host fixture.
set -euo pipefail
[[ ( $# == 4 || $# == 5 ) && $EUID == 0 ]] || exit 2
run_as=$1
repo=$2
work=$3
cohort=$4
reference_args=()
if [[ $# == 5 ]]; then
  [[ $5 == reference-before-entry ]] || exit 2
  reference_args=(--reference-before-entry)
fi
[[ "$run_as" != root && "$repo" == /* && "$work" == /* ]]
ip link set lo up
[[ $(ip -o link show | wc -l) -eq 1 ]]
[[ -z $(ip -4 route show default) && -z $(ip -6 route show default) ]]
# New HOME/config/cache paths contain no previous identity or native data.
# No proxy, GitHub token, application URL or inherited credential env is passed.
exec runuser -u "$run_as" -- env -i \
  HOME="$work/home" USER="$run_as" LOGNAME="$run_as" \
  PATH=/usr/local/bin:/usr/bin:/bin LANG=C.UTF-8 LC_ALL=C.UTF-8 \
  PUB_CACHE="$work/pub-cache" TMPDIR="$work/tmp" TMP="$work/tmp" TEMP="$work/tmp" \
  XDG_CONFIG_HOME="$work/config" XDG_CACHE_HOME="$work/config/cache" \
  FLUTTER_ROOT="$work/flutter" FLUTTER_SUPPRESS_ANALYTICS=true \
  DART_SUPPRESS_ANALYTICS=true CI=true TERM=dumb \
  python3 "$repo/ci/mobile-check.py" run --cohort "$cohort" --work "$work" "${reference_args[@]}"
