#!/usr/bin/env bash
# Called by `sudo unshare --net`; this namespace contains no host fixture.
set -euo pipefail
[[ ( $# == 5 || $# == 6 ) && $EUID == 0 ]] || exit 2
run_as=$1
repo=$2
work=$3
cohort=$4
validation_mode=$5
[[ "$validation_mode" == group-revoke-58 || "$validation_mode" == full-regression ]]
reference_args=()
if [[ $# == 6 ]]; then
  [[ $6 == reference-before-entry && "$validation_mode" == full-regression ]] || exit 2
  reference_args=(--reference-before-entry)
fi
[[ "$run_as" != root && "$repo" == /* && "$work" == /* ]]
real_user_home=$(python3 - "$repo" "$run_as" <<'PYHOME'
import importlib.util,pathlib,sys
spec=importlib.util.spec_from_file_location('mobile_check',pathlib.Path(sys.argv[1])/'ci/mobile-check.py')
check=importlib.util.module_from_spec(spec);spec.loader.exec_module(check)
print(check.run_user_home(sys.argv[2]))
PYHOME
)
ip link set lo up
[[ $(ip -o link show | wc -l) -eq 1 ]]
[[ -z $(ip -4 route show default) && -z $(ip -6 route show default) ]]
# Keep the real account HOME; task config/cache/TMP paths contain no prior identity.
# No proxy, GitHub token, application URL or inherited credential env is passed.
exec runuser -u "$run_as" -- env -i \
  HOME="$real_user_home" USER="$run_as" LOGNAME="$run_as" \
  PATH=/usr/local/bin:/usr/bin:/bin LANG=C.UTF-8 LC_ALL=C.UTF-8 \
  PUB_CACHE="$work/pub-cache" TMPDIR="$work/tmp" TMP="$work/tmp" TEMP="$work/tmp" \
  XDG_CONFIG_HOME="$work/config" XDG_CACHE_HOME="$work/config/cache" \
  FLUTTER_ROOT="$work/flutter" FLUTTER_SUPPRESS_ANALYTICS=true \
  DART_SUPPRESS_ANALYTICS=true CI=true TERM=dumb \
  python3 "$repo/ci/mobile-check.py" run --cohort "$cohort" --work "$work" --validation-mode "$validation_mode" "${reference_args[@]}"
