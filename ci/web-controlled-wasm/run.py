#!/usr/bin/env python3
"""Manual controlled Web WASM build. No product replacement, native bind or login."""
import argparse
from collections import Counter
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import subprocess
import sys
import tarfile

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
COMMIT = '7b156fc5557878ad6e6a07e572694af5699c6624'
PREFIX = 'github.com/openimsdk/openim-sdk-core/v3/'
SPECS = {
 'bufferplan': {'package':'wasm/bufferplan', 'wasm':False, 'tops':[
  'TestMediaPlansKeepProtocolUUIDsAndFillMissingSizesWithoutDroppingExtensions',
  'TestVideoAndMediaMetadataRejectBeforeRegistrationOnEitherBufferFailure',
  'TestReadyMediaAndTextPreserveStandardSendWithoutFileRegistration'], 'subs':[
  'TestMediaPlansKeepProtocolUUIDsAndFillMissingSizesWithoutDroppingExtensions/'+s for s in ['picture','sound','video','file']]},
 'wasmwrapper': {'package':'wasm/wasm_wrapper', 'wasm':True, 'tops':[
  'TestBufferVideoAcknowledgesPairBeforeOriginalDispatchAndSettlesOnce',
  'TestBufferRegistrationFailureOrRetiredAckNeverDispatches',
  'TestBufferDelayedDispatchKeepsTheOriginalSessionAndMapGuard',
  'TestBufferActiveUUIDRejectsConcurrentByBufferAndByFileWithoutReplacingIt',
  'TestBufferLateReleaseCannotRemoveNewSessionLeaseAndOldProgressIsSuppressed',
  'TestOrdinaryEarliestClaimBlocksBufferBeforeRegistrationAndDoesNotAcceptAnUnopenedTicket'], 'subs':[
  'TestBufferRegistrationFailureOrRetiredAckNeverDispatches/'+s for s in ['failed-map','owner-ABA','lost-worker']]},
 'ownread': {'package':'internal/conversation_msg', 'wasm':True, 'tops':[
  'TestOwnReadTimeEnvelopeAndIncomingIdentity','TestOwnReadTimeCoreDurablePortAndDistinctEvent'], 'subs':[
  'TestOwnReadTimeEnvelopeAndIncomingIdentity/'+s for s in ['foreign_actor','foreign_CID','foreign_peer','ordinary_message_envelope','missing_authoritative_time','duplicate_seq','outgoing_row','real_envelope_time_ignores_original_send_time','mixed_directions_keep_only_exact_incoming_rows','mixed_directions_reject_forged_outgoing_identity','mixed_directions_reject_duplicate_ID_and_seq_before_skipping']] + [
  'TestOwnReadTimeCoreDurablePortAndDistinctEvent/'+s for s in ['committed','unconfirmed','wrong_stored_identity','foreign_preflight','scope_is_captured_before_delayed_conversation_lookup','delayed_conversation_cannot_renew_history_or_owner_scope']]}}
PHASES = ['fetch','metadata','download','module-verify','build','bufferplan','wasmwrapper','ownread','abi']


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def name(value):
    p = PurePosixPath(value)
    if not isinstance(value,str) or not value or p.is_absolute() or str(p)!=value or '..' in p.parts or '\\' in value:
        raise ValueError('canonical relative path required')
    return value


def regular(path):
    path=Path(path)
    if path.is_symlink() or not path.is_file() or path.resolve()!=path:
        raise ValueError('canonical regular source required')
    return path.read_bytes()


def pinned(row, root):
    path=root/name(row['path']);raw=regular(path)
    if sha(raw)!=row['sha256'] or len(raw)!=row['bytes']:
        raise ValueError('source bytes differ: '+row['path'])
    if 'mode' in row and stat.S_IMODE(path.stat().st_mode)!=int(row['mode'],8):
        raise ValueError('source mode differs: '+row['path'])
    return raw


def vector(root):
    out=[]
    for p in sorted(root.rglob('*')):
        if p.is_symlink(): raise ValueError('source symlink forbidden')
        if p.is_file():
            raw=p.read_bytes();out.append({'path':str(p.relative_to(root)),'bytes':len(raw),'sha256':sha(raw),'mode':oct(stat.S_IMODE(p.stat().st_mode))})
    return out


def digest(rows):
    return sha(b''.join((r['path']+'\0'+r['sha256']+'\n').encode() for r in sorted(rows,key=lambda r:r['path'].encode())))


def write(path, value):
    path.parent.mkdir(parents=True,exist_ok=True)
    with path.open('x') as f:
        json.dump(value,f,indent=2);f.write('\n');f.flush();os.fsync(f.fileno())


def load_contract(path, expected, root=REPO):
    name(str(path))
    if not str(path).startswith('ci/web-controlled-wasm/') or not str(path).endswith('.json') or not re.fullmatch('[0-9a-f]{64}',expected):
        raise ValueError('explicit committed final manifest and SHA256 required')
    raw=regular(root/path)
    if sha(raw)!=expected: raise ValueError('final manifest SHA differs')
    c=json.loads(raw)
    if (c['schemaVersion']!=1 or c['baseCommit']!=COMMIT or c['baseInputsSHA256']!=sha(regular(HERE/'base-inputs.json')) or c['harnessInputsSHA256']!=sha(regular(HERE/'inputs.json'))
            or c['goSumSHA256']!='8eb9b73313a7f774bfef48814bfc1b73e7ff7f57bd37f9d40555e8d24f70262f'
            or c['requiredABI']!=145 or not 309<len(c['core'])<=1000 or digest(c['core'])!=c['coreDigest']):
        raise ValueError('fixed controlled source contract differs')
    if len({r['path'] for r in c['core']})!=len(c['core']): raise ValueError('duplicate final core path')
    for r in c['core']: name(r['path'])
    targets=set();baseRows={r['path']:r for r in json.loads(regular(HERE/'base-inputs.json'))['core']}
    effective=dict(baseRows)
    for r in c['overlay']:
        target=name(r['target'])
        if target in targets or not target.endswith('.go') or not r['path'].startswith('ci/web-controlled-wasm/source/core/'):
            raise ValueError('exact unique Go overlay required')
        old=baseRows.get(target)
        if r['beforeSHA256']!=(old['sha256'] if old else None):raise ValueError('exact overlay source before required')
        targets.add(target);pinned(r,root)
        effective[target]={'path':target,'bytes':r['bytes'],'sha256':r['sha256'],'mode':r['mode']}
    if sorted(effective.values(),key=lambda r:r['path'])!=c['core']:
        raise ValueError('final vector must exactly equal fixed base plus declared overlay')
    for r in c['support']: pinned(r,root)
    return c


def audit_test(events, code, label):
    if label not in SPECS or code!=0: raise ValueError('targeted test process failed')
    spec=SPECS[label];expected=set(spec['tops']+spec['subs']);runs=Counter();passes=Counter();foot=[]
    for e in events:
        if e.get('Package')!=PREFIX+spec['package']: raise ValueError('unexpected Go package')
        output=e.get('Output','')
        if any(x in output for x in ['WARNING: DATA RACE','panic:','build failed','FAIL\t','fatal error:']): raise ValueError('runtime/compile failure output')
        test=e.get('Test');action=e.get('Action')
        if action in ['fail','skip']: raise ValueError('failure or skipped target')
        if test:
            if test not in expected: raise ValueError('unexpected test name')
            if action=='run':runs[test]+=1
            if action=='pass':passes[test]+=1
        elif action=='pass': foot.append(e)
    if runs!=Counter({n:1 for n in expected}) or passes!=runs or len(foot)!=1:
        raise ValueError('exact target names/run/pass/footer required')
    return {'topPASS':len(spec['tops']),'subPASS':len(spec['subs']),'skip':0,'fail':0,'race':not spec['wasm']}


def clean_environment(base, owned, goroot):
    # Preserve actual HOME. All Go caches/temp are task-owned; no ambient auth.
    keys=['PATH','HOME','LANG','LC_ALL','TZ','SYSTEMROOT']
    env={k:base[k] for k in keys if k in base}
    env.update(GOROOT=goroot,GOTOOLCHAIN='local',GOENV='off',GOWORK='off',GOAUTH='off',
      GOPROXY='off',GOSUMDB='off',GOPRIVATE='',GONOPROXY='',GONOSUMDB='',GOFLAGS='-mod=readonly',
      GOCACHE=str(owned/'cache/build'),GOMODCACHE=str(owned/'cache/mod'),GOPATH=str(owned/'cache/gopath'),
      TMPDIR=str(owned/'tmp'),GOTMPDIR=str(owned/'tmp'),GOMAXPROCS='2',GOMEMLIMIT='2GiB',
      GIT_CONFIG_NOSYSTEM='1',GIT_CONFIG_GLOBAL='/dev/null',GIT_TERMINAL_PROMPT='0',
      NODE_OPTIONS='',NODE_PATH='')
    return env


def phase_allowed(label, completed):
    i=PHASES.index(label)
    if completed!=PHASES[:i]: raise ValueError('phase dispatched before verified prerequisite')
    return label not in ['fetch','download']


def extract_archive(raw, destination):
    if len(raw)>8*1024*1024: raise ValueError('fixed core archive too large')
    total=0
    with tarfile.open(fileobj=io.BytesIO(raw),mode='r:') as t:
        seen=set()
        for member in t:
            path=name(member.name.rstrip('/'))
            if path in seen or not(member.isfile() or member.isdir()):raise ValueError('unsafe archive member')
            seen.add(path);out=destination/path
            if member.isdir():out.mkdir(parents=True,exist_ok=True);continue
            total+=member.size
            if total>16*1024*1024:raise ValueError('fixed core archive decoded limit')
            out.parent.mkdir(parents=True,exist_ok=True)
            with out.open('xb') as f:shutil.copyfileobj(t.extractfile(member),f)
            out.chmod(member.mode & 0o755)


def prepare(base, c, owned, archive):
    core=owned/'source/core';protocol=owned/'source/protocol';core.mkdir(parents=True);protocol.mkdir()
    extract_archive(regular(archive),core)
    patch=base['compatPatch'];raw=regular(REPO/patch['path'])
    if sha(raw)!=patch['sha256']:raise ValueError('fixed compatibility patch changed')
    git=['git','-c','credential.helper=','-c','core.hooksPath=/dev/null','--git-dir='+str(owned/'download/git/.git'),'--work-tree='+str(core)]
    gitEnv={**os.environ,'GIT_CONFIG_GLOBAL':'/dev/null','GIT_CONFIG_NOSYSTEM':'1','GIT_TERMINAL_PROMPT':'0'}
    p=subprocess.run(git+['apply','--check',str(REPO/patch['path'])],cwd=core,env=gitEnv,capture_output=True)
    if p.returncode:raise ValueError('fixed patch precondition failed')
    p=subprocess.run(git+['apply',str(REPO/patch['path'])],cwd=core,env=gitEnv,capture_output=True)
    if p.returncode:raise ValueError('fixed patch apply failed')
    old=regular(core/'go.sum');acquired=regular(REPO/base['acquiredSum']['path'])
    if sha(old)!=base['originalSourceSumSHA256'] or sha(acquired)!=base['acquiredSum']['sha256'] or not set(old.splitlines())<=set(acquired.splitlines()):
        raise ValueError('additive reviewed checksum lock required')
    (core/'go.sum').write_bytes(acquired)
    if vector(core)!=base['core']:raise ValueError('entire patched base vector/modes differ')
    for r in base['protocol']:
        raw=pinned(r,REPO/'server/pkg/protocol');out=protocol/r['path'];out.parent.mkdir(parents=True,exist_ok=True);out.write_bytes(raw);out.chmod(int(r['mode'],8))
    if vector(protocol)!=base['protocol']:raise ValueError('entire protocol52 differs')
    original={r['path']:r for r in base['core']}
    for r in c['overlay']:
        old=original.get(r['target'])
        if r['beforeSHA256']!=(old['sha256'] if old else None):raise ValueError('overlay before differs')
        out=core/r['target'];out.parent.mkdir(parents=True,exist_ok=True);out.write_bytes(pinned(r,REPO));out.chmod(int(r['mode'],8))
    if vector(core)!=c['core']:raise ValueError('whole final source vector/modes differ')
    before=regular(core/'go.mod');a=base['absoluteProtocolReplace'].encode();b=base['portableProtocolReplace'].encode()
    if before.count(a)!=1:raise ValueError('exact private protocol replacement absent')
    portable=before.replace(a,b,1);(core/'go.mod').write_bytes(portable)
    write(owned/'raw/source-manifest.json',{'baseCommit':COMMIT,'archiveSHA256':sha(regular(archive)),'compatPatchSHA256':base['compatPatch']['sha256'],'acquiredGoSumSHA256':sha(acquired),'coreBeforePortable':c['core'],
      'coreEffective':vector(core),'protocol':vector(protocol),'portableGoModBeforeSHA':sha(before),'portableGoModAfterSHA':sha(portable),'fullRegression':False,'runtimeReplacement':False})
    return core,vector(core),vector(protocol)


def require_guard(record, code):
    if (code!=0 or record.get('status')!='passed' or record.get('exit_code')!=0
            or record.get('minimum_observed_free_bytes',0)<1073741824):raise ValueError('resource/process phase failed')


def main():
    a=argparse.ArgumentParser();a.add_argument('--manifest',required=True);a.add_argument('--manifest-sha256',required=True);a.add_argument('--verify',action='store_true');args=a.parse_args()
    c=load_contract(Path(args.manifest),args.manifest_sha256)
    inputs=json.loads(regular(HERE/'inputs.json'));base=json.loads(regular(HERE/'base-inputs.json'))
    if sha(regular(HERE/'base-inputs.json'))!=inputs['baseInputsSHA256'] or sha(regular(HERE/'required-abi.json'))!=inputs['requiredABISHA256']:
        raise ValueError('harness fixed input changed')
    controllerRaw=regular(HERE/'controller-pins.json')
    if sha(controllerRaw)!=inputs['controllerPinsSHA256']:raise ValueError('harness controller lock differs')
    controllerPins=json.loads(controllerRaw)['files']
    for row in controllerPins:pinned(row,REPO)
    inputsBefore=sha(regular(HERE/'inputs.json'))
    guard=REPO/inputs['resourceGuard']['path']
    if sha(regular(guard))!=inputs['resourceGuard']['sha256']:raise ValueError('resource guard changed')
    for r in [base['compatPatch'],base['acquiredSum']]:
        if sha(regular(REPO/r['path']))!=r['sha256']:raise ValueError('fixed base input changed')
    for r in base['protocol']:pinned(r,REPO/'server/pkg/protocol')
    if args.verify:
        print(json.dumps({'state':'manifest_and_source_only','coreCount':len(c['core']),'Go':0,'build':0}));return 0
    if (os.environ.get('GITHUB_ACTIONS')!='true' or os.environ.get('GITHUB_EVENT_NAME')!='workflow_dispatch'
            or os.environ.get('GITHUB_REPOSITORY')!='damaowoaini/LuoboIM' or os.geteuid()==0):
        raise ValueError('explicit manual hosted non-root runner required')
    owned=REPO/'.ci-owned/web-controlled-wasm'
    owned.parent.mkdir(exist_ok=True);owned.mkdir(mode=0o700)
    for p in ['raw','products','cache/build','cache/mod','cache/gopath','tmp','download']:(owned/p).mkdir(parents=True,exist_ok=True)
    coldFree=shutil.disk_usage(owned).free
    write(owned/'raw/admission.json',{'freeBytes':coldFree,'minimumColdFreeBytes':4294967296,'continuousFloorBytes':1073741824})
    if coldFree<4294967296:raise ValueError('4GiB cold admission required')
    write(owned/'ownership.json',{'runID':os.environ['GITHUB_RUN_ID'],'attempt':os.environ['GITHUB_RUN_ATTEMPT'],'head':os.environ['GITHUB_SHA'],'manifestSHA256':args.manifest_sha256})
    goroot=subprocess.check_output(['go','env','GOROOT'],text=True,env={**os.environ,'GOTOOLCHAIN':'local','GOENV':'off','GOWORK':'off','GOAUTH':'off'}).strip()
    env=clean_environment(os.environ,owned,goroot);completed=[]
    uid,gid=os.getuid(),os.getgid()
    isolate=['sudo','-n','unshare','--net','--','setpriv','--reuid='+str(uid),'--regid='+str(gid),'--clear-groups','--no-new-privs','--']
    def phase(label,argv,seconds=600,extra=None,cwd=None):
        isolated=phase_allowed(label,completed);folder=owned/'raw'/label;folder.mkdir()
        phaseenv=dict(env,**(extra or {}))
        if isolated:
            phaseenv.update(LUOBO_WASM_ISOLATED='1',LUOBO_WASM_HOST_NET_INODE=str(os.stat('/proc/self/ns/net').st_ino))
        # env -i removes hosted credentials and all proxy/auth fallbacks from children.
        command=['env','-i']+[k+'='+v for k,v in sorted(phaseenv.items())]+[sys.executable,str(HERE/'phase.py'),str(folder),'--']+argv
        if isolated:command=isolate+command
        cmd=[sys.executable,str(guard),'--cwd',str(cwd or owned),'--log',str(folder/'guard.log'),'--record',str(folder/'resource.json'),
          '--min-free-mib','1024','--poll-interval','0.2','--allocated-root',str(owned),'--max-allocated-mib','8192','--timeout',str(seconds),'--']+command
        code=subprocess.call(cmd,stdout=subprocess.DEVNULL)
        record=json.loads(regular(folder/'resource.json'));require_guard(record,code)
        result=json.loads(regular(folder/'process.json'))
        if result['exit']!=0:raise ValueError('child command failed: '+label)
        completed.append(label);return folder
    accepted=False;coreBefore=protocolBefore=None;core=None
    try:
        # Only public Git origin. No ambient credential helper and no GitHub auth.
        phase('fetch',[sys.executable,str(HERE/'fetch.py'),str(owned/'download'),COMMIT],900)
        core,coreBefore,protocolBefore=prepare(base,c,owned,owned/'download/core.tar')
        meta=phase('metadata',[sys.executable,str(HERE/'metadata.py'),goroot],60)
        metadata=json.loads(regular(meta/'stdout.log'))
        if metadata['go']!=inputs['go'] or metadata['node']!=inputs['node']:raise ValueError('exact Go/Node required')
        phase('download',['go','mod','download','all'],900,{'GOPROXY':'https://proxy.golang.org','GOSUMDB':'sum.golang.org'},core)
        if vector(core)!=coreBefore or vector(owned/'source/protocol')!=protocolBefore:raise ValueError('download modified source lock')
        phase('module-verify',['go','mod','verify'],180,cwd=core)
        phase('build',['go','build','-mod=readonly','-p=1','-trimpath','-o',str(owned/'products/openIM.wasm'),'./wasm/cmd'],900,{'GOOS':'js','GOARCH':'wasm','CGO_ENABLED':'0'},core)
        for label,spec in SPECS.items():
            opts={'GOOS':'js','GOARCH':'wasm','CGO_ENABLED':'0'} if spec['wasm'] else {'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'1'}
            argv=['go','test','-mod=readonly','-p=1','-parallel=1','-json','-count=1','-vet=off','-timeout=180s','-run','^('+'|'.join(spec['tops'])+')$']
            if spec['wasm']:argv+=['-exec',str(Path(goroot)/'lib/wasm/go_js_wasm_exec')]
            else:argv+=['-race']
            folder=phase(label,argv+['./'+spec['package']],600,opts,core)
            events=[json.loads(x) for x in regular(folder/'stdout.log').decode().splitlines() if x.strip()]
            write(folder/'summary.json',audit_test(events,0,label))
        shim=regular(Path(goroot)/'lib/wasm/wasm_exec.js');(owned/'products/wasm_exec.js').write_bytes(shim)
        abi=phase('abi',['node',str(HERE/'observe.mjs'),str(owned/'products/wasm_exec.js'),str(owned/'products/openIM.wasm'),str(HERE/'required-abi.json')],60)
        observation=json.loads(regular(abi/'stdout.log'));write(owned/'products/abi.json',observation)
        if vector(core)!=coreBefore or vector(owned/'source/protocol')!=protocolBefore:raise ValueError('source/modes/locks changed')
        for r in c['support']+controllerPins:pinned(r,REPO)
        if sha(regular(HERE/'inputs.json'))!=inputsBefore:raise ValueError('harness inputs changed')
        write(owned/'raw/source-post.json',{'core':vector(core),'protocol':vector(owned/'source/protocol'),'supportExact':True})
        shutil.copyfile(owned/'raw/source-manifest.json',owned/'products/source-manifest.json')
        write(owned/'products/artifact-manifest.json',{'files':vector(owned/'products'),'manifestSHA256':args.manifest_sha256,'Go':metadata,'runtimeReplacement':False,'ABIIsRegistrationOnly':True})
        accepted=True
    finally:
        sourcePost=False;cleanup=False;postError=None
        try:
            if core is not None and coreBefore is not None:
                actualCore=vector(core);actualProtocol=vector(owned/'source/protocol')
                sourcePost=actualCore==coreBefore and actualProtocol==protocolBefore
                for r in c['support']+controllerPins:pinned(r,REPO)
                sourcePost=sourcePost and sha(regular(HERE/'inputs.json'))==inputsBefore
                if not (owned/'raw/source-post.json').exists():
                    write(owned/'raw/source-post.json',{'core':actualCore,'protocol':actualProtocol,'exact':sourcePost})
        except BaseException as error:
            postError=type(error).__name__
        finally:
            # No services start. Remove only this run's fresh owned trees.
            try:
                for r in [owned/'cache',owned/'tmp',owned/'download',owned/'source']:
                    if r.exists():shutil.rmtree(r)
                cleanup=all(not r.exists() for r in [owned/'cache',owned/'tmp',owned/'download',owned/'source'])
            finally:
                accepted=accepted and sourcePost and cleanup and postError is None
                write(owned/'raw/controller.json',{'accepted':accepted,'completedPhases':completed,'runtimeReplacement':False,'nativeBind':False,'SDKInit':0,'login':0,'send':0,'fullRegression':False,'ownedCacheSourceRemoved':cleanup,'sourcePostExact':sourcePost,'postErrorType':postError})
    if not accepted:raise ValueError('source or phase acceptance incomplete')
    return 0

if __name__=='__main__':
    raise SystemExit(main())
