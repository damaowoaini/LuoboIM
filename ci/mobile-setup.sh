#!/usr/bin/env bash
# Runs only in a disposable GitHub-hosted Linux job. Network is setup-only.
set -euo pipefail
[[ $# == 2 ]] || { echo 'usage: mobile-setup.sh COHORT FRESH_ROOT' >&2; exit 2; }
cohort=$1
work=$2
repo=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
[[ "$cohort" == current304 ]]
[[ "$work" == /* && ! -e "$work" && ! -L "$work" ]]
umask 077
mkdir -- "$work"
mkdir -- "$work/evidence" "$work/home" "$work/tmp" "$work/pub-cache" "$work/config"
phase=source
on_failure() {
  status=$?
  trap - ERR
  # Setup has no application credentials. Still remove URL userinfo/query and
  # control characters from a bounded diagnostic, and prefix every log line.
  python3 - "$work/evidence" "$phase" "$status" <<'PY'
import json,pathlib,re,sys,urllib.parse
root=pathlib.Path(sys.argv[1]); phase=sys.argv[2]; code=int(sys.argv[3])
(root/'setup-failure.json').write_text(json.dumps({'phase':phase,'exit_code':code})+'\n')
logs={'source':'source.json','config':'flutter-config.log','version':'flutter-version.json',
      'version_check':'flutter-version.json','precache':'precache.log','pub_get':'pub-get.log'}
print('[mobile setup] failed phase='+phase+' exit='+str(code),file=sys.stderr)
log=root/logs.get(phase,'missing.log')
if log.is_file():
 text=log.read_bytes()[-16384:].decode('utf-8','replace')
 def clean_url(match):
  try:
   u=urllib.parse.urlsplit(match.group()); host=u.hostname or 'redacted'
   return urllib.parse.urlunsplit((u.scheme,host+(':'+str(u.port) if u.port else ''),u.path,'',''))
  except ValueError:return '[redacted URL]'
 text=re.sub(r'https?://[^\s]+',clean_url,text)
 text=re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]','',text)
 for line in text.splitlines()[-60:]:
  print('setup | '+''.join(c for c in line if c=='\t' or ord(c)>=32),file=sys.stderr)
PY
  exit "$status"
}
trap on_failure ERR
python3 "$repo/ci/mobile-source.py" --cohort "$cohort" --output "$work/source" > "$work/evidence/source.json"

# Official tag is fetched from the official repository, then verified by commit.
# No floating channel, third-party setup action or local tool snapshot is used.
phase=toolchain
python3 - "$repo/ci/mobile/inputs.json" "$work" <<'PY'
import json, pathlib, subprocess, sys
p=json.loads(pathlib.Path(sys.argv[1]).read_text())['toolchain']
w=pathlib.Path(sys.argv[2]); sdk=w/'flutter'
subprocess.run(['git','init','--quiet',str(sdk)],check=True)
subprocess.run(['git','-C',str(sdk),'remote','add','origin',p['repository']],check=True)
tag='refs/tags/'+p['tag']
# Flutter derives its semantic version from local tags, not FETCH_HEAD alone.
subprocess.run(['git','-C',str(sdk),'fetch','--depth=1','origin',tag+':'+tag],check=True)
subprocess.run(['git','-C',str(sdk),'checkout','--quiet','--detach',tag],check=True)
head=subprocess.check_output(['git','-C',str(sdk),'rev-parse','HEAD'],text=True).strip()
if head != p['framework_revision']: raise SystemExit('official framework commit mismatch')
tag_head=subprocess.check_output(['git','-C',str(sdk),'rev-parse',tag+'^{commit}'],text=True).strip()
if tag_head != head: raise SystemExit('local release tag does not point at pinned framework')
tags=subprocess.check_output(['git','-C',str(sdk),'tag','--points-at','HEAD'],text=True).splitlines()
if p['tag'] not in tags: raise SystemExit('local semantic release tag is missing')
if (sdk/'bin/internal/engine.version').read_text().strip()!=p['engine_revision']:
 raise SystemExit('pinned engine mismatch')
PY
export HOME="$work/home" PUB_CACHE="$work/pub-cache" TMPDIR="$work/tmp"
export XDG_CONFIG_HOME="$work/config" XDG_CACHE_HOME="$work/config/cache"
export FLUTTER_SUPPRESS_ANALYTICS=true DART_SUPPRESS_ANALYTICS=true CI=true
flutter="$work/flutter/bin/flutter"
phase=config
"$flutter" config --no-analytics > "$work/evidence/flutter-config.log" 2>&1
phase=version
"$flutter" --version --machine > "$work/evidence/flutter-version.json"
phase=version_check
python3 - "$repo/ci/mobile/inputs.json" "$work/evidence/flutter-version.json" <<'PY'
import json,sys
p=json.load(open(sys.argv[1]))['toolchain']; v=json.load(open(sys.argv[2]))
for key,want in [('frameworkVersion',p['tag']),('frameworkRevision',p['framework_revision']),('engineRevision',p['engine_revision']),('dartSdkVersion',p['dart_version'])]:
 if v.get(key)!=want: raise SystemExit('official SDK version mismatch: '+key)
PY
phase=precache
"$flutter" precache --linux > "$work/evidence/precache.log" 2>&1
phase=pub_get
(cd "$work/source/app" && "$flutter" pub get --enforce-lockfile) > "$work/evidence/pub-get.log" 2>&1

# The official sqlite3 hook checks the exact upstream binary hash itself.
# Seed only its fresh shared output; never copy hook snapshots or stamps.
phase=sqlite
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
phase=verify
python3 "$repo/ci/mobile-check.py" verify --cohort "$cohort" --work "$work"
trap - ERR
