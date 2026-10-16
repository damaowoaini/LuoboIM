#!/usr/bin/env bash
# Runs only in a disposable GitHub-hosted Linux job. Network is setup-only.
set -euo pipefail
[[ $# == 2 ]] || { echo 'usage: mobile-setup.sh COHORT FRESH_ROOT' >&2; exit 2; }
cohort=$1
work=$2
repo=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
[[ "$cohort" == current230 || "$cohort" == booking-custom249 ]]
[[ "$work" == /* && ! -e "$work" && ! -L "$work" ]]
umask 077
mkdir -- "$work"
mkdir -- "$work/evidence" "$work/home" "$work/tmp" "$work/pub-cache" "$work/config"
python3 "$repo/ci/mobile-source.py" --cohort "$cohort" --output "$work/source" > "$work/evidence/source.json"

# Official tag is fetched from the official repository, then verified by commit.
# No floating channel, third-party setup action or local tool snapshot is used.
python3 - "$repo/ci/mobile/inputs.json" "$work" <<'PY'
import json, pathlib, subprocess, sys
p=json.loads(pathlib.Path(sys.argv[1]).read_text())['toolchain']
w=pathlib.Path(sys.argv[2]); sdk=w/'flutter'
subprocess.run(['git','init','--quiet',str(sdk)],check=True)
subprocess.run(['git','-C',str(sdk),'remote','add','origin',p['repository']],check=True)
subprocess.run(['git','-C',str(sdk),'fetch','--depth=1','origin','refs/tags/'+p['tag']],check=True)
subprocess.run(['git','-C',str(sdk),'checkout','--quiet','--detach','FETCH_HEAD'],check=True)
head=subprocess.check_output(['git','-C',str(sdk),'rev-parse','HEAD'],text=True).strip()
if head != p['framework_revision']: raise SystemExit('official framework commit mismatch')
if (sdk/'bin/internal/engine.version').read_text().strip()!=p['engine_revision']:
 raise SystemExit('pinned engine mismatch')
PY
export HOME="$work/home" PUB_CACHE="$work/pub-cache" TMPDIR="$work/tmp"
export XDG_CONFIG_HOME="$work/config" XDG_CACHE_HOME="$work/config/cache"
export FLUTTER_SUPPRESS_ANALYTICS=true DART_SUPPRESS_ANALYTICS=true CI=true
flutter="$work/flutter/bin/flutter"
"$flutter" config --no-analytics > "$work/evidence/flutter-config.log" 2>&1
"$flutter" --version --machine > "$work/evidence/flutter-version.json"
python3 - "$repo/ci/mobile/inputs.json" "$work/evidence/flutter-version.json" <<'PY'
import json,sys
p=json.load(open(sys.argv[1]))['toolchain']; v=json.load(open(sys.argv[2]))
for key,want in [('frameworkRevision',p['framework_revision']),('engineRevision',p['engine_revision']),('dartSdkVersion',p['dart_version'])]:
 if v.get(key)!=want: raise SystemExit('official SDK version mismatch: '+key)
PY
"$flutter" precache --linux > "$work/evidence/precache.log" 2>&1
(cd "$work/source/app" && "$flutter" pub get --enforce-lockfile) > "$work/evidence/pub-get.log" 2>&1

# The official sqlite3 hook checks the exact upstream binary hash itself.
# Seed only its fresh shared output; never copy hook snapshots or stamps.
python3 - "$repo/ci/mobile/inputs.json" "$work" <<'PY'
import hashlib,json,pathlib,sys,urllib.request
p=json.loads(pathlib.Path(sys.argv[1]).read_text()); w=pathlib.Path(sys.argv[2]); a=p['sqlite_linux']
package=w/'pub-cache/hosted/pub.dev/sqlite3-3.5.2'
hashes=(package/'lib/src/hook/asset_hashes.dart').read_text()
if "'libsqlite3.x64.linux.so': '"+a['sha256']+"'" not in hashes:
 raise SystemExit('locked sqlite3 package native hash differs')
with urllib.request.urlopen(a['url'],timeout=90) as r:
 b=r.read(32*1024*1024+1)
if len(b)>32*1024*1024 or hashlib.sha256(b).hexdigest()!=a['sha256']:
 raise SystemExit('official Linux SQLite binary checksum mismatch')
t=w/'source'/a['seed_path']; t.parent.mkdir(mode=0o700,parents=True,exist_ok=True)
with t.open('xb') as f: f.write(b)
(w/'evidence/sqlite-native.json').write_text(json.dumps({'sha256':a['sha256'],'bytes':len(b),'source':a['url'],'old_stamp_copied':False})+'\n')
PY
python3 "$repo/ci/mobile-check.py" verify --cohort "$cohort" --work "$work"
