#!/usr/bin/env python3
"""Fresh owned graph; no old product binary, runtime result, DB or external deployment is adopted."""
import argparse,hashlib,importlib.util,json,os,re,stat,subprocess,sys,time,shutil
from pathlib import Path
from collections import Counter
REPO=Path(__file__).resolve().parents[2]
INPUTS=Path(__file__).with_name('inputs.json')
FINAL_SOURCE = {'members': 1576, 'digest': 'df4a7fa81384a0985937751553bbca5363630343690b8c2cb8f09838b54c10f8'}
SOURCE_ROOT='ci/candidates/backend-fullgraph/source/'
REQUIRED_GRAPH_SOURCE={'server/internal/runtimeacceptance/acceptance_linux.go','server/internal/runtimeacceptance/graph_linux.go','server/internal/runtimeacceptance/cancellation_linux.go','server/cmd/tools/owned-fullgraph-acceptance/main.go','server/pkg/common/startrpc/fullgraph_evidence_linux_test.go'}
RESOURCE={'initialBytes':4<<30,'floorMiB':1024,'allocatedMiB':16384,'pollSeconds':.2,'controllerSeconds':10800,'mainCompileTotalSeconds':1200,'scenarioSeconds':1380,'originalOperatorContextSeconds':1200,'targetGoTimeoutSeconds':180}
SCENARIOS=('reader-registered-cancel','reader-writer-fence-cancel','ack','redis-unknown')
ROLES=('api','crontask','msggateway','msgtransfer-managed','push','rpc-auth','rpc-conversation','rpc-encryption','rpc-friend','rpc-group','rpc-msg','rpc-rtc','rpc-third','rpc-user')
CLEANUP_SCOPES=[{'label': 'cleanup-ownedcontrol', 'targets': {'internal/namespaceimport/ownedcontrol': ['TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths']}, 'requiredSubtests': {'internal/namespaceimport/ownedcontrol': {'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths': ['local-first']}}}, {'label': 'cleanup-runtimeacceptance', 'targets': {'internal/runtimeacceptance': ['TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup']}, 'requiredSubtests': {'internal/runtimeacceptance': {'TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup': ['cancel-audit-before-stop', 'timeout-before-local-wait-complete']}}}]
CLEANUP_PASSED_REUSE={'run': 39062533742, 'job': 117483277540, 'head': '756f82e2b90310020e197ee7848eb34c50c40766', 'source': {'members': 1575, 'digest': '57513d328ed76c6a1091d8caee531b7a6d5d952982f9621849369aa7e46e6bd6'}, 'reviewSHA256': '01060ed44de69cd19b42b645e384a99a6dd2f0ddcfb7aa8ec434b18ca90257cf', 'artifact': {'artifact': 12112670650, 'run': 39062533742, 'attempt': 1, 'head': '756f82e2b90310020e197ee7848eb34c50c40766', 'sha256': '3eb9a5a1a223210c9222c712a8de4074b03d5606dd72137aaf07b891a29c6581', 'bytes': 54487, 'exactAPIDigest': True, 'files': 69, 'binariesDownloaded': False}, 'topPASS': 2, 'subPASS': 3, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths/local-first'}, {'package': 'internal/runtimeacceptance', 'name': 'TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup'}, {'package': 'internal/runtimeacceptance', 'name': 'TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup/cancel-audit-before-stop'}, {'package': 'internal/runtimeacceptance', 'name': 'TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup/timeout-before-local-wait-complete'}], 'sourceToolsPostExact': True, 'runtimeAccepted': False}
CLEANUP_COMPLETE_SCOPES=[{'label': 'cleanup-ownedcontrol', 'targets': {'internal/namespaceimport/ownedcontrol': ['TestNamespaceRuntimeStopKeepsEveryOriginalChildOwnedAfterOneNoncanonicalExit', 'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths', 'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation', 'TestOwnedLocalCleanupCancelledValidationAndOriginalDoneStayLocal']}, 'requiredSubtests': {'internal/namespaceimport/ownedcontrol': {'TestNamespaceRuntimeStopKeepsEveryOriginalChildOwnedAfterOneNoncanonicalExit': ['unknown-middle-child-does-not-strand-later-original-owner', 'good-drains-retained-and-unknown-never-gets-a-join', 'repeat-Stop-does-not-renew-signal-or-erase-failure'], 'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths': ['canonical-first', 'runtime-first', 'local-first'], 'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation': ['actual-original-image-and-bounded-file-pins', 'real-fd-path-stability-and-mid-read-cancellation'], 'TestOwnedLocalCleanupCancelledValidationAndOriginalDoneStayLocal': ['cancelled-validation-keeps-live-child-unsignalled', 'same-original-wait-during-image-validation-is-local-only']}}}, {'label': 'cleanup-runtimeacceptance', 'targets': {'internal/runtimeacceptance': ['TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup']}, 'requiredSubtests': {'internal/runtimeacceptance': {'TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup': ['cancel-audit-before-stop', 'timeout-before-local-wait-complete']}}}]
CLEANUP_RECENT_REUSE={'run': 39060706865, 'job': 117477900963, 'head': 'f48bd32eeea5b31568fe2e2e03ea56a47c655264', 'source': {'members': 1575, 'digest': '52ddd8742eef822c04d14b99f6ccf47acd12f43c7df06d98fe6fe389da231b58'}, 'reviewSHA256': '41a7f45b3323effb1529bf6e417bbe31cd53cc41fbe354f260cccc335d346794', 'artifact': {'artifact': 12111770451, 'run': 39060706865, 'attempt': 1, 'head': 'f48bd32eeea5b31568fe2e2e03ea56a47c655264', 'sha256': '483cdf79808816f7778eaedb27378e09d85b903370964f66ab083d75b4e8e714', 'bytes': 9702, 'exactAPIDigest': True, 'files': 12, 'binariesDownloaded': False}, 'topPASS': 3, 'subPASS': 9, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeStopKeepsEveryOriginalChildOwnedAfterOneNoncanonicalExit/unknown-middle-child-does-not-strand-later-original-owner'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeStopKeepsEveryOriginalChildOwnedAfterOneNoncanonicalExit/good-drains-retained-and-unknown-never-gets-a-join'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeStopKeepsEveryOriginalChildOwnedAfterOneNoncanonicalExit/repeat-Stop-does-not-renew-signal-or-erase-failure'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestNamespaceRuntimeStopKeepsEveryOriginalChildOwnedAfterOneNoncanonicalExit'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation/actual-original-image-and-bounded-file-pins'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation/real-fd-path-stability-and-mid-read-cancellation'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedLocalCleanupCancelledValidationAndOriginalDoneStayLocal/cancelled-validation-keeps-live-child-unsignalled'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedLocalCleanupCancelledValidationAndOriginalDoneStayLocal/same-original-wait-during-image-validation-is-local-only'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedLocalCleanupCancelledValidationAndOriginalDoneStayLocal'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths/canonical-first'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths/runtime-first'}], 'partialChildParent': {'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths': ['canonical-first', 'runtime-first']}, 'oldParentFAILNotReused': True, 'sourceAndToolsPostExact': True}
CLEANUP_FIXTURE_REPAIR={'target': 'server/internal/namespaceimport/ownedcontrol/process_local_cleanup_linux_test.go', 'beforeSHA256': '4f40dfe1837207983cf907d3ed161e6c6c1c3493cec39c557512d1d5bde9cedd', 'afterSHA256': 'bcf11c0283992a27812fa20666dad5d7ecb95d514e5bb0e0ceee7816279030b8', 'bytes': 6777, 'productionChanges': 0}
CLEANUP_REUSE={'run': 39053868483, 'head': 'ce7964a0c7aa474cbc83c32dd71c1bfcfb83cfec', 'source': {'members': 1573, 'digest': 'f6f1568c3dbc12b36441108f36bb771153f134fd5907a056a5532b0ee773d7f2'}, 'reviewSHA256': '48c619c7b7c19ad33b5bb5b9bfa38f655de918c01ad19a6784fed4d9ccc1efbf', 'package': 'internal/namespaceimport/ownedcontrol', 'subtests': {'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths': ['unbound_start_failure_only_waits_original_done']}}
LAUNCHER_PASSED_REUSE={'run': 39075035152, 'job': 117520306290, 'head': 'a6d9276c53a75e8ca19e351cab1d34b419b560e7', 'source': {'members': 1575, 'digest': '868e46b88717f80dd90e837ebd5bb64634ed5030ddd3c83a8d6c00398c749717'}, 'reviewSHA256': '412fdacae0a1a8ff842d92269d2bf00fb0a578b3ed1eab928ee86d2e56ac1f38', 'artifact': {'artifact': 12115839677, 'run': 39075035152, 'attempt': 1, 'head': 'a6d9276c53a75e8ca19e351cab1d34b419b560e7', 'sha256': '674eadc00436aa018a1fe7828be0568f3ce80f499ef53b487deacfcb5709099a', 'bytes': 52320, 'exactAPIDigest': True, 'files': 66, 'binariesDownloaded': False}, 'launcherNamesSHA256': '6471ec2f43ce64c92e2c399c7a0630e1d86740c9e2ee2a69352573e14b61c932', 'topPASS': 1, 'subPASS': 0, 'skip': 0, 'fail': 0, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedLauncherHoldPersistsAndCannotPrepareMetadata'}], 'sourceToolsPostExact': True, 'currentSourcePASS': False, 'runtimeAccepted': False}
GRAPH_TARGETS={'pkg/common/startrpc': ['TestOwnedPreparedReaderRegistersBeforeServeAndRetainsFinalWriterFence', 'TestOwnedPushActualOwnerClosesOriginalResourcesBeforeRedisUnknownNonpermit']}
REDIS_PARSER_CONTROL = {'phase': 'redis-parser', 'targets': {'internal/namespaceimport/ownedcontrol': ['TestOwnedRedisCredentialNormalizesRESP2RESP3WithoutWeakeningDisableProof']}, 'requiredSubtests': {'internal/namespaceimport/ownedcontrol': {'TestOwnedRedisCredentialNormalizesRESP2RESP3WithoutWeakeningDisableProof': ['equivalent-protocols-complete-summary-and-real-disabled-state', 'malformed-fields-and-nil-first-duplicate-reject', 'disabled-check-keeps-off-allcommands-and-empty-passwords']}}, 'goTimeoutSeconds': 180, 'phaseGuardSeconds': 240, 'newDatabaseFixtures': 0, 'otherR8TargetsDispatched': False}
REDIS_PARSER_PASSED_REUSE = {'run': 39082704756, 'job': 117542803080, 'attempt': 1, 'head': '2f17d99d2f32c4fc72b9b922f8d2a8783938b665', 'source': {'members': 1576, 'digest': '122c6e2d1b158bbe9addd879c733ef85bf6c2b26a345f583aa4b5a31d2260f48'}, 'artifact': {'artifact': 12119877247, 'run': 39082704756, 'attempt': 1, 'head': '2f17d99d2f32c4fc72b9b922f8d2a8783938b665', 'sha256': 'b7bbb052d28081550d537cfb867103cf8e2e19ad84f4b90893a4157f569f7eef', 'bytes': 41738, 'exactAPIDigest': True, 'files': 56, 'binariesDownloaded': False}, 'rootReviewSHA256': 'cc2fa43abea21d6b2c305571e66eeb62e422f2a67f5965b6c71591f532c40501', 'topPASS': 1, 'subPASS': 3, 'skip': 0, 'fail': 0, 'namedCompletions': [{'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedRedisCredentialNormalizesRESP2RESP3WithoutWeakeningDisableProof'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedRedisCredentialNormalizesRESP2RESP3WithoutWeakeningDisableProof/disabled-check-keeps-off-allcommands-and-empty-passwords'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedRedisCredentialNormalizesRESP2RESP3WithoutWeakeningDisableProof/equivalent-protocols-complete-summary-and-real-disabled-state'}, {'package': 'internal/namespaceimport/ownedcontrol', 'name': 'TestOwnedRedisCredentialNormalizesRESP2RESP3WithoutWeakeningDisableProof/malformed-fields-and-nil-first-duplicate-reject'}], 'sourcePins': [{'target': 'server/internal/namespaceimport/ownedcontrol/redis_credential.go', 'bytes': 6107, 'sha256': '26fad310f6cda0b25068cb03e6ab53d2a59a5258f78923b8ab7f49db5a5461b6'}, {'target': 'server/internal/namespaceimport/ownedcontrol/redis_credential_test.go', 'bytes': 5522, 'sha256': 'd9c8471dd01df8dcd9b746edb179a751dbf6444d756eab83d5eea4e3e731867d'}], 'sourcePostExact': True, 'currentSourcePASS': False, 'runtimeAccepted': False, 'actualRedisParserTestsDispatched': 0, 'rawEvidence': [{'path': '.qa/backend-reader-boundary-and-redis-golden-batch-1/graph-artifact/redis-parser-names.json', 'bytes': 956, 'sha256': '94e60e18529a5f933ce070d15e0418819d19ed725929cc4e4cb8c5474bcf1c13'}, {'path': '.qa/backend-reader-boundary-and-redis-golden-batch-1/graph-artifact/redis-parser-events.json', 'bytes': 5001, 'sha256': '1056eb00f05ca35635dca33dd667d538f1f967a329509af0ecec1ba5d9e0afd7'}, {'path': '.qa/backend-reader-boundary-and-redis-golden-batch-1/graph-artifact/source-post.json', 'bytes': 120, 'sha256': 'd25ccb64f558e0966dbdacea71ba478c3f50330e0d3583f611165ece87aed8cc'}, {'path': '.qa/backend-reader-boundary-and-redis-golden-batch-1/graph-artifact/phase-0004.json', 'bytes': 2509, 'sha256': 'b3594ce1d3614e16a7b91e95982b3b4aff0fc5bb6005ea33ce5167eeb423628d'}]}
GRAPH_CHILDREN={'TestOwnedPreparedReaderRegistersBeforeServeAndRetainsFinalWriterFence': ['registered-not-serving-before-readiness', 'known-readiness-still-keeps-writer-fence'], 'TestOwnedPushActualOwnerClosesOriginalResourcesBeforeRedisUnknownNonpermit': ['required-token-only']}


def sha(raw):return hashlib.sha256(raw).hexdigest()
def regular(p,maximum=128<<20):
 p=Path(p)
 if p.resolve()!=p:raise ValueError('canonical bounded regular file required')
 fd=os.open(p,os.O_RDONLY|os.O_NOFOLLOW)
 try:
  before=os.fstat(fd)
  if not stat.S_ISREG(before.st_mode) or before.st_size>maximum:raise ValueError('bounded regular file required')
  chunks=[];left=maximum+1
  while left:
   block=os.read(fd,min(1<<20,left))
   if not block:break
   chunks.append(block);left-=len(block)
  raw=b''.join(chunks);after=os.fstat(fd);current=p.lstat()
  keys=lambda v:(v.st_dev,v.st_ino,v.st_mode,v.st_size,v.st_mtime_ns,v.st_ctime_ns)
  if keys(before)!=keys(after) or keys(before)!=keys(current) or len(raw)!=before.st_size:raise ValueError('file body/mode changed while reading')
  return raw
 finally:os.close(fd)

def pin(row):
 p=REPO/row['path'];raw=regular(p)
 if len(raw)!=row['bytes'] or sha(raw)!=row['sha256'] or stat.S_IMODE(p.stat().st_mode)!=0o644:raise ValueError('pinned input differs')
 return raw
def module(path,label):
 s=importlib.util.spec_from_file_location(label,REPO/path);v=importlib.util.module_from_spec(s);s.loader.exec_module(v);return v
def write(path,value):
 raw=(json.dumps(value,separators=(',',':'),ensure_ascii=False)+'\n').encode()
 fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
 with os.fdopen(fd,'wb') as f:
  if f.write(raw)!=len(raw):raise ValueError('short metadata write')
  f.flush();os.fsync(f.fileno())
 fd=os.open(path.parent,os.O_RDONLY|os.O_DIRECTORY)
 try:os.fsync(fd)
 finally:os.close(fd)
 return sha(raw)
def check_locked(c):
 if FINAL_SOURCE is None or c.get('state')!='FROZEN_FINAL_SOURCE' or c.get('source')!=FINAL_SOURCE or c.get('sourceReviewAccepted') is not True:raise ValueError('Root final formatted whole-source identity required; prepare-only cannot execute')
 if c['scenarios']!=list(SCENARIOS) or c['roles']!=list(ROLES) or c['existingGreenTestsToRerun']!=[] or c['reuseOldBinary'] is not False:raise ValueError('exact fresh graph scope required')
 if c['resource']!=RESOURCE:raise ValueError('reviewed graph resource contract differs')
 if c.get('cleanupScopes')!=CLEANUP_SCOPES or c.get('cleanupCompleteScopes')!=CLEANUP_COMPLETE_SCOPES or c.get('cleanupRecentReuse')!=CLEANUP_RECENT_REUSE or c.get('cleanupFixtureRepair')!=CLEANUP_FIXTURE_REPAIR or c.get('cleanupReuse')!=CLEANUP_REUSE or 'ownedcontrolReuse' in c or c.get('originalTargets')!=GRAPH_TARGETS or c.get('requiredOriginalSubtests')!=GRAPH_CHILDREN or c.get('cleanupPassedReuse')!=CLEANUP_PASSED_REUSE or c.get('cleanupTestsToRun')!=[]:raise ValueError('exact fresh local1/runtime2/graph2+3 with original3top9sub and unbound1 reuse required')
def validate_entries(bodies,c):
    rows = c['entrypoints']
    if len(rows) != 14 or len({r['role'] for r in rows}) != 14 or len({r['package'] for r in rows}) != 14:
        raise ValueError('fourteen actual distinct main packages required')
    families = Counter(r['lifecycleFamily'] for r in rows)
    if families != Counter({'rpc':10,'api':1,'crontask':1,'msggateway':1,'msgtransfer-managed':1}):
        raise ValueError('actual five lifecycle families required')
    if [r['package'] for r in rows] != c['compileOnlyPackages']:
        raise ValueError('compile list is not actual fourteen mains')
    for row in rows:
        raw = bodies.get(row['target']); identity = row['actualMain']
        if (raw is None or row['target'] != 'server/'+row['package'].removeprefix('./')+'/main.go'
                or row['package'].startswith('./cmd/') is not True
                or len(raw) != identity['bytes'] or sha(raw) != identity['sha256']
                or row['outputName'] != row['role'] or not re.fullmatch('[a-z][a-z0-9-]*',row['outputName'])):
            raise ValueError('actual main identity or owned binary output differs')
    for family in c['lifecycleFamilies']:
        for key in ('command','ownedOwner'):
            binding=family[key];raw=bodies.get(binding['target']);identity=binding['source']
            if raw is None or len(raw)!=identity['bytes'] or sha(raw)!=identity['sha256']:
                raise ValueError('actual lifecycle dispatch/owner source differs')
    group = next(r for r in rows if r['role'] == 'rpc-group')
    if b'StartSvrManaged' not in bodies[group['target']]:
        raise ValueError('actual managed group entry cannot be replaced by old main')

def verify():
 c=json.loads(regular(INPUTS));check_locked(c)
 check_launcher_reuse(c.get('launcherPassedReuse'))
 if c.get('launcherTestsToRun')!=[]:raise ValueError('unchanged launcher must use exact historical receipt without Go dispatch')
 for row in c['dependencies']:pin(row)
 parent=module(c['sourceProvider'],'fullgraph_actual_source_parent');old,bodies,recipe=parent.verify()
 if old['source']!=c['parent']:raise ValueError('explicit parent identity differs')
 parent_bodies=dict(bodies);bodies=dict(bodies);seen=set()
 for r in c['overlay']:
  target=r['target'];preimage=bodies.get(target)
  if target in seen or r['path']!=SOURCE_ROOT+target or r['beforeSHA256']!=(sha(preimage) if preimage is not None else None):raise ValueError('exact final overlay preimage required')
  seen.add(target);bodies[target]=pin(r)
 actual={str(p.relative_to(REPO)) for p in (REPO/SOURCE_ROOT).rglob('*') if p.is_file() or p.is_symlink()}
 if seen!=set(c['allowedOverlayTargets']) or actual!={r['path'] for r in c['overlay']} or len(bodies)!=FINAL_SOURCE['members'] or recipe.digest(recipe.rows(bodies))!=FINAL_SOURCE['digest']:raise ValueError('whole final source vector differs')
 for t in ('server/go.mod','server/go.sum','server/config/config.example.yaml'):
  if bodies[t]!=parent_bodies[t]:raise ValueError('original app module locks/config changed')
 if not REQUIRED_GRAPH_SOURCE<=set(bodies):raise ValueError('actual graph and original oracle implementation required')
 if set(c['sourceModes'])!=set(bodies) or any(type(v) is not int or v not in (0o644,0o755) for v in c['sourceModes'].values()):raise ValueError('whole final source mode vector required')
 validate_entries(bodies,c['entrypointContract'])
 catalog=json.loads(pin(c['catalog']))
 for row in c['controllerPins']:pin(row)
 check_redis_parser_reuse(c.get('redisParserPassedReuse'),bodies)
 if c.get('redisParserTestsToRun')!=[]:raise ValueError('unchanged Redis parser must not be redispatched')
 if catalog['scope']!='fresh_owned_fullgraph_compatibility_fixture_only_no_production_default' or catalog['minio']['mode']!='source-elf' or catalog['minio']['status']!='historical_unmaintained_test_only':raise ValueError('explicit source-MinIO fixture required')
 if catalog['images']['minio']!=catalog['images']['redis'] or catalog['images']['keeper']!=catalog['images']['redis']:raise ValueError('known Redis base must remain explicit')
 module('ci/backend-fullgraph/minio_build.py','fullgraph_fixed_fixture_policy').fixture_policy(c)
 return c,bodies,recipe,parent,old,catalog

def project_go_test_events(raw,phase):
 if phase not in {'pending-two-original-oracles','launcher-ownedcontrol','redis-parser'}|{scope['label'] for scope in CLEANUP_SCOPES}:raise ValueError('only exact original and cleanup Go test phases may expose build diagnostics')
 events=[];digest=hashlib.sha256();total=0;head=bytearray()
 for line in raw.splitlines():
  event=json.loads(line)
  events.append({k:event[k] for k in ('Time','Action','Package','Test','Elapsed') if k in event})
  if event.get('Action')=='build-output' and event.get('Test') in (None,''):
   output=event.get('Output')
   if not isinstance(output,str):raise ValueError('public Go build-output must be a string')
   block=output.encode('utf-8');digest.update(block);total+=len(block)
   head.extend(block[:max(0,65536-len(head))])
 if len(events)>4096:raise ValueError('finite original oracle event bound')
 diagnostic=None
 if total:
  text=bytes(head).decode('utf-8','ignore')
  diagnostic={'phase':phase,'publicSourceOnly':True,'selection':'go-test-build-output-without-Test-only','completeBytes':total,'completeSHA256':digest.hexdigest(),'diagnostic':text,'truncated':len(text.encode('utf-8'))<total}
 return events,diagnostic

def runtime_cleanup_observations(raw,phase):
 if phase!='cleanup-runtimeacceptance':raise ValueError('only exact runtime cleanup fixture observations are public')
 marker='LUOBO_RUNTIME_CLEANUP_DIAG_V1 ';top='TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup';package='github.com/openimsdk/open-im-server/v3/internal/runtimeacceptance'
 kinds={'cancel-audit-before-stop','timeout-before-local-wait-complete'};stages={'begin','private-directory','launcher-open','executable-path','executable-canonical','executable-read','child-directory','bootstrap-write','receipt-write','child-start','child-ready','fixture-ready','assert-early-incomplete','short-cleanup','assert-short-incomplete','final-cleanup-start','signal-wait','release-write','final-cleanup-return','assert-primary','assert-complete','count-check','repeat-cleanup','complete'}
 fields={'version','kind','stage','errorClass','shortErrorClass','role','binaryBytes','childReturned','finalReturned','observedExits','complete','launcherClosed','signal0','signal1','primaryPreserved','elapsedMicros','firstStopMicros','finalCleanupMicros'}
 classes={'none','deadline','cancelled','owned-evidence','not-exist','permission','other'};signals={'unobserved','seen','one','other','unreadable'};rows={};rejected=0
 def unique(pairs):
  result=dict(pairs)
  if len(result)!=len(pairs):raise ValueError('duplicate diagnostic field')
  return result
 for line in raw.splitlines():
  try:event=json.loads(line)
  except (ValueError,TypeError):rejected+=1;continue
  if not isinstance(event,dict) or event.get('Action')!='output' or event.get('Package')!=package or not isinstance(event.get('Test'),str) or event.get('Test') not in {top+'/'+kind for kind in kinds}:continue
  output=event.get('Output')
  if not isinstance(output,str) or not output.startswith(marker):continue
  try:
   if len(output.encode('utf-8'))>2048 or not output.endswith('\n') or output.count('\n')!=1:raise ValueError('bounded single diagnostic line required')
   value=json.loads(output[len(marker):-1],object_pairs_hook=unique)
   if not isinstance(value,dict) or set(value)!=fields or value['version']!='runtime-cleanup-fixture-observation-v1' or value['kind'] not in kinds or event['Test']!=top+'/'+value['kind'] or value['stage'] not in stages or value['errorClass'] not in classes or value['shortErrorClass'] not in classes|{'unobserved'} or value['role'] not in {'none','rpc-msg','msgtransfer-legacy'} or value['signal0'] not in signals or value['signal1'] not in signals:raise ValueError('closed diagnostic enums required')
   if any(type(value[key]) is not bool for key in ('childReturned','finalReturned','complete','launcherClosed','primaryPreserved')):raise ValueError('closed diagnostic booleans required')
   for key,maximum in [('binaryBytes',1<<30),('observedExits',2),('elapsedMicros',180000000),('firstStopMicros',180000000),('finalCleanupMicros',180000000)]:
    if type(value[key]) is not int or not 0<=value[key]<=maximum:raise ValueError('bounded diagnostic integers required')
   if value['kind'] in rows:raise ValueError('one diagnostic per exact failed child required')
  except (ValueError,TypeError,KeyError,UnicodeError):rejected+=1;continue
  rows[value['kind']]=value
 return {'version':'runtime-cleanup-fixture-observations-v1','diagnosticsComplete':set(rows)==kinds and rejected==0,'rejectedMarkers':min(rejected,4096),'observations':[rows[key] for key in sorted(rows)]}

def check_redis_parser_reuse(value,bodies):
 # This exact 122c receipt is historical1/3, never a fresh-source PASS. JSON
 # comparison also keeps numeric/boolean types and the complete four names.
 if not isinstance(value,dict) or json.dumps(value,sort_keys=True,separators=(',',':'))!=json.dumps(REDIS_PARSER_PASSED_REUSE,sort_keys=True,separators=(',',':')):
  raise ValueError('exact Root accepted historical Redis parser receipt required')
 for row in value['sourcePins']:
  raw=bodies.get(row['target'])
  if raw is None or len(raw)!=row['bytes'] or sha(raw)!=row['sha256']:
   raise ValueError('reused Redis parser production/test source changed')
 return value

def load_redis_parser_reuse(out,bodies):
 receipt=check_redis_parser_reuse(dict(REDIS_PARSER_PASSED_REUSE),bodies)
 write(out/'redis-parser-passed-reuse.json',receipt)
 return receipt

def run_redis_parser_control(go,environment,command,parent,out):
 # This pure parser test needs no plane, DB fixture or product process. Its
 # original Go reporter is audited before any success receipt is written.
 control=REDIS_PARSER_CONTROL;targets=control['targets'];package=next(iter(targets));top=targets[package][0]
 raw=command(control['phase'],[go,'test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run','^'+top+'$','./'+package],control['phaseGuardSeconds'],environment)
 events=[json.loads(line) for line in raw.decode().splitlines() if line.strip()]
 result=parent.audit(events,0,targets)
 expected={(package,top)}|{(package,top+'/'+child) for child in control['requiredSubtests'][package][top]}
 names=[(row['package'],row['name']) for row in result['namedCompletions']]
 if result['topPASS']!=1 or result['subPASS']!=3 or len(names)!=4 or set(names)!=expected:
  raise ValueError('exact Redis parser parent and all three children required')
 receipt=dict(result,source=FINAL_SOURCE,accepted=True,newDatabaseFixtures=0,otherR8TargetsDispatched=False)
 write(out/'redis-parser-names.json',receipt)
 return receipt

def run_cleanup_controls(go,environment,command,parent,out):
 results=[]
 for scope in CLEANUP_SCOPES:
  names=sorted(name for tops in scope['targets'].values() for name in tops)
  children=sorted(child for tops in scope['requiredSubtests'].values() for subs in tops.values() for child in subs)
  pattern='^('+'|'.join(re.escape(name) for name in names)+')$/^('+'|'.join(re.escape(child) for child in children)+')$'
  raw=command(scope['label'],[go,'test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run',pattern]+['./'+pkg for pkg in scope['targets']],240,environment)
  events=[json.loads(line) for line in raw.decode().splitlines() if line.strip()]
  result=parent.audit(events,0,scope['targets'])
  actual={(row['package'],row['name']) for row in result['namedCompletions'] if '/' in row['name']}
  expected={(pkg,top+'/'+child) for pkg,tops in scope['requiredSubtests'].items() for top,subs in tops.items() for child in subs}
  if actual!=expected:raise ValueError('all actual cleanup descendants required')
  write(out/(scope['label']+'-names.json'),result);results.append(result)
 union={'topPASS':sum(row['topPASS'] for row in results),'subPASS':sum(row['subPASS'] for row in results),'namedCompletions':[name for row in results for name in row['namedCompletions']]}
 if (union['topPASS'],union['subPASS'])!=(2,3):raise ValueError('exact fresh cleanup local1/runtime1top2sub required')
 write(out/'cleanup-names.json',union)
 return union

def audit_target_union(original,cleanup):
 entries=original['namedCompletions']+cleanup['namedCompletions'];actual=Counter((row['package'],row['name']) for row in entries)
 graph={(pkg,top) for pkg,tops in GRAPH_TARGETS.items() for top in tops}
 graph|={(pkg,top+'/'+child) for pkg,tops in GRAPH_TARGETS.items() for top in tops for child in GRAPH_CHILDREN[top]}
 def scope_names(scopes):
  names=set()
  for scope in scopes:
   names|={(pkg,top) for pkg,tops in scope['targets'].items() for top in tops}
   names|={(pkg,top+'/'+child) for pkg,tops in scope['requiredSubtests'].items() for top,subs in tops.items() for child in subs}
  return names
 expected=graph|scope_names(CLEANUP_SCOPES)
 if set(actual)!=expected or any(count!=1 for count in actual.values()) or len(expected)!=10:raise ValueError('exact fresh4top6sub, including failed local-first only, required')
 full=graph|scope_names(CLEANUP_COMPLETE_SCOPES)
 recent=Counter((row['package'],row['name']) for row in CLEANUP_RECENT_REUSE['namedCompletions'])
 if set(recent)!=full-expected or any(count!=1 for count in recent.values()) or len(recent)!=12 or (sum('/' not in name for pkg,name in recent),sum('/' in name for pkg,name in recent))!=(3,9):raise ValueError('only actual original3top9sub/partial child reuse required')
 if (CLEANUP_REUSE['package'],'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths') in recent:raise ValueError('failed original parent cannot be supplied by partial child PASS')
 prior={(CLEANUP_REUSE['package'],top+'/'+child) for top,subs in CLEANUP_REUSE['subtests'].items() for child in subs}
 all_names=expected|set(recent)|prior
 new_tops={'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation','TestOwnedLocalCleanupCancelledValidationAndOriginalDoneStayLocal'}
 new_names={(pkg,name) for pkg,name in full if name.split('/')[0] in new_tops}
 if len(prior)!=1 or full&prior or len(full)!=22 or len(new_names)!=6 or len(all_names-new_names)!=17 or len(all_names)!=23:raise ValueError('complete original5top12sub and new2top4sub obligations cannot shrink')
 receipt=dict(CLEANUP_REUSE,topPASS=0,subPASS=1,namedCompletions=[{'package':pkg,'name':name} for pkg,name in sorted(prior)])
 return {'source':FINAL_SOURCE,'topPASS':4,'subPASS':6,'skip':0,'fail':0,'namedCompletions':[{'package':pkg,'name':name} for pkg,name in sorted(actual)],'cleanupReuse':receipt,'cleanupRecentReuse':CLEANUP_RECENT_REUSE,'originalObligation':{'topCount':5,'subCount':12,'crossSource':True},'newSafety':{'topCount':2,'subCount':4,'crossSource':True},'completeObligation':{'topCount':7,'subCount':16,'crossSource':True}}

def check_launcher_names(value):
 expected=[{'package':'internal/namespaceimport/ownedcontrol','name':'TestOwnedLauncherHoldPersistsAndCannotPrepareMetadata'}]
 if not isinstance(value,dict) or set(value)!={'source','topPASS','subPASS','skip','fail','namedCompletions'} or value.get('source')!=FINAL_SOURCE or value.get('namedCompletions')!=expected:raise ValueError('exact current-source launcher completion required')
 if any(type(value.get(key)) is not int or value[key]!=count for key,count in [('topPASS',1),('subPASS',0),('skip',0),('fail',0)]):raise ValueError('launcher exact1top0sub without fail or skip required')
 return value

def run_launcher_control(go,environment,command,parent,out):
 targets={'internal/namespaceimport/ownedcontrol':['TestOwnedLauncherHoldPersistsAndCannotPrepareMetadata']}
 pattern='^TestOwnedLauncherHoldPersistsAndCannotPrepareMetadata$'
 raw=command('launcher-ownedcontrol',[go,'test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run',pattern,'./internal/namespaceimport/ownedcontrol'],240,environment)
 events=[json.loads(line) for line in raw.decode().splitlines() if line.strip()]
 actual=parent.audit(events,0,targets)
 receipt=check_launcher_names(dict(source=FINAL_SOURCE,**{key:actual[key] for key in ('topPASS','subPASS','skip','fail','namedCompletions')}))
 write(out/'launcher-names.json',receipt)
 return receipt

def check_launcher_reuse(value):
 # Compare the closed original receipt, including JSON numeric/boolean types.
 # Its source is868e, never FINAL_SOURCE. It is not new-source Go acceptance.
 if not isinstance(value,dict) or json.dumps(value,sort_keys=True,separators=(',',':'))!=json.dumps(LAUNCHER_PASSED_REUSE,sort_keys=True,separators=(',',':')):raise ValueError('exact Root accepted historical launcher receipt required')
 return value

def load_launcher_reuse(out):
 # Source-bound 868e launcher evidence is reused without a new Go dispatch.
 # Its exact Hold/reopen/reject-Start test path does not call the changed
 # bootstrap, Merkle proof, Agent.Acquire, flow or acceptance observation code.
 # Those source changes are not a claim that all dependency bytes are identical.
 receipt=dict(LAUNCHER_PASSED_REUSE)
 write(out/'launcher-passed-reuse.json',check_launcher_reuse(receipt))
 return receipt

def load_cleanup_reuse(out):
 # Root accepted actual2top3sub at575; observer-only bootstrap additions do
 # not enter these unchanged cleanup bodies. Never dispatch Go here.
 receipt=dict(CLEANUP_PASSED_REUSE)
 write(out/'cleanup-passed-reuse.json',receipt)
 return receipt

def audit_current_oracles(original,reused):
 if reused!=CLEANUP_PASSED_REUSE:raise ValueError('exact Root accepted cleanup receipt required')
 # Retain the old closed obligation audit as set arithmetic; its historical
 # helpers/controls remain unchanged. Only new oracle names are fresh here.
 closure=audit_target_union(original,reused)
 return dict(closure,topPASS=2,subPASS=3,namedCompletions=original['namedCompletions'],cleanupPassedReuse=reused,actualCleanupTestsDispatched=0)

def public_cleanup(row):
 if row['version']!='owned-fullgraph-resource-cleanup-v1' or type(row['accepted']) is not bool or not re.fullmatch('[0-9a-f]{32}',row['nonce']) or any(not re.fullmatch('[0-9a-f]{64}',v) for v in row['exactContainerIDs']):raise ValueError('exact resource cleanup receipt required')
 return {'version':row['version'],'ownerNonceSHA256':sha(row['nonce'].encode()),'exactContainerIDs':row['exactContainerIDs'],'unconfirmedIDs':row['unconfirmedIDs'],'accepted':row['accepted']}

def check_business_permissions(value):
 if not isinstance(value,dict) or set(value)!={'version','principals'} or value.get('version')!='owned-fullgraph-business-permissions-v1':raise ValueError('actual finite business permission evidence required')
 rows=value['principals']
 fields={'role','redisWhoami','redisClientInfo','redisAclGetUserError','redisConfigGetError','redisForeignKeyError','kafkaOriginalTopics','kafkaAclDescribeError','kafkaForeignTopicError','kafkaConnectionClosed'}
 if not isinstance(rows,list) or len(rows)!=2:raise ValueError('both original business principals required')
 finite=[]
 for role,row in zip(('legacy','runtime'),rows):
  if not isinstance(row,dict) or set(row)!=fields or row.get('role')!=role:raise ValueError('exact business principal evidence required')
  if any(row.get(k) is not True for k in ('redisWhoami','redisClientInfo','kafkaOriginalTopics','kafkaConnectionClosed')):raise ValueError('actual original identity/topics and connection close required')
  if any(row.get(k)!='NOPERM' for k in ('redisAclGetUserError','redisConfigGetError','redisForeignKeyError')):raise ValueError('typed Redis NOPERM required; absence or transport errors never pass')
  if type(row.get('kafkaAclDescribeError')) is not int or row['kafkaAclDescribeError']!=31 or type(row.get('kafkaForeignTopicError')) is not int or row['kafkaForeignTopicError']!=29:raise ValueError('typed Kafka cluster/topic authorization refusal required')
  finite.append({key:row[key] for key in sorted(fields)})
 return {'version':'owned-fullgraph-business-permissions-v1','principals':finite}

def check_cancel(value,scenario,source,builds,plane_sha):
 permissions=check_business_permissions(value.get("businessPermissions"))
 # The concrete original operator must actually cancel its retained group.
 # Exact cancellation fields are frozen with the final oracle source, never
 # inferred from a non-serving snapshot or from container disappearance.
 if value.get('version')!='owned-fullgraph-token-v2-result-v1' or value.get('scenario')!=scenario or value.get('accepted') is not True or value.get('sourceSHA256')!=source or value.get('resourcePlaneSHA256')!=plane_sha or value.get('phase')!='actual-prerelease-cancel-and-original-joins-accepted' or value.get('freshPlaneOnly') is not True or value.get('knownAdmissionSHA256','') or value.get('actualTokenRPCDispatched')!=0:raise ValueError('actual prerelease cancellation required')
 phase='registered-not-serving-before-readiness' if scenario=='reader-registered-cancel' else 'known-readiness-still-keeps-writer-fence'
 expected={'rpc-msg','rpc-conversation'} if scenario=='reader-registered-cancel' else set(ROLES)
 cancellation=value.get('cancellation')
 if not isinstance(cancellation,dict) or cancellation.get('pausedPhase')!=phase or cancellation.get('actualPreparedRoles')!=len(expected) or value.get('actualStartedRoles')!=0 or value.get('roles') not in (None,[]) or value.get('readerServed') not in (None,[]):raise ValueError('original prerelease roles/fence differ')
 if any(cancellation.get(k) is not True for k in ('knownAdmissionAbsent','readerDiscoveryAbsentBefore','readerDiscoveryAbsentAfter','originalStopReturnedError')):raise ValueError('actual original cancellation refusal required')
 if scenario=='reader-registered-cancel' and value.get('dataReadySHA256','') or scenario=='reader-writer-fence-cancel' and not re.fullmatch('[0-9a-f]{64}',value.get('dataReadySHA256','')):raise ValueError('exact original readiness boundary differs')
 phases=value['readerPhases']
 names=['registered-not-serving-before-readiness','known-readiness-still-keeps-writer-fence'][:1 if len(expected)==2 else 2]
 if [r['phase'] for r in phases]!=names or any(r['bothActualConnectionsNotServing'] is not True for r in phases):raise ValueError('real registered listener observations required')
 rows=cancellation['roles'];by={b['role']:b for b in builds['binaries']}
 if len(rows)!=len(expected) or {r['role'] for r in rows}!=expected or len({r['pid'] for r in rows})!=len(rows) or len({r['ownerNonceSHA256'] for r in rows})!=len(rows):raise ValueError('all original launched roles must be retained')
 for row in rows:
  if row['sourceSHA256']!=source or row['binarySHA256']!=by[row['role']]['sha256'] or not row['startToken'] or any(row.get(k) is not True for k in ('actualAbsent','announcementAbsent','canonicalDrainAbsent','privateReleaseAbsent','originalWaitObserved')):raise ValueError('actual cancellation join/absence differs')
 return {'scenario':scenario,'accepted':True,'source':source,'originalLaunchedRoles':len(rows),'actualAbsentRoles':len(rows),'noAdmission':True,'noRelease':True,'tokenRPCDispatches':0,'businessPermissions':permissions,'originalResultSHA256':None}

def check_result(value,scenario,source,builds,plane_sha):
 if scenario.startswith('reader-'):return check_cancel(value,scenario,source,builds,plane_sha)
 permissions=check_business_permissions(value.get('businessPermissions'))
 if (value['version']!='owned-fullgraph-token-v2-result-v1' or value['scenario']!=scenario or value['accepted'] is not True or value['sourceSHA256']!=source or value['resourcePlaneSHA256']!=plane_sha or value['actualStartedRoles']!=14 or not value['freshPlaneOnly'] or value['actualTokenRPCDispatched']!=1 or not value['rawPushRejected'] or not value['actualUserReadExact'] or not value['redisKeyPresentBefore'] or not value['redisKeyAbsentAfter'] or value['mongoMessageCount']!=0):raise ValueError('actual complete fullgraph result required')
 rows=value['roles'];by={b['role']:b for b in builds['binaries']}
 if len(rows)!=14 or {r['role'] for r in rows}!=set(ROLES) or len({r['pid'] for r in rows})!=14 or len({r['ownerNonceSHA256'] for r in rows})!=14:raise ValueError('fourteen original distinct process owners required')
 for r in rows:
  if not r['actualAbsent'] or r['sourceSHA256']!=source or r['binarySHA256']!=by[r['role']]['sha256']:raise ValueError('original process/binary/source or cleanup differs')
  expected=not(scenario=='redis-unknown' and r['role']=='push')
  if r['canonicalDrain']!=expected or expected and not re.fullmatch('[0-9a-f]{64}',r.get('drainSHA256','')):raise ValueError('unknown must never become canonical drain')
 phases=value['readerPhases'];names=['registered-not-serving-before-readiness','known-readiness-still-keeps-writer-fence','known-admission-without-private-release-still-not-serving']
 if len(phases)!=3 or [r['phase'] for r in phases]!=names or any(not r['bothActualConnectionsNotServing'] for r in phases):raise ValueError('actual staged reader fences required')
 served=value.get('readerServed')
 if not isinstance(served,list) or len(served)!=2 or {r['role'] for r in served}!={'rpc-msg','rpc-conversation'}:raise ValueError('same actual readers must respond after release')
 original={r['role']:r for r in phases[0]['endpoints']}
 for row in served:
  before=original[row['role']]
  if any(row.get(k)!=before.get(k) for k in ('pid','startToken','endpoint','configurationSHA256','listenerInode')) or any(row.get(k) is not True for k in ('actualTCPConnected','originalListenerRetained','actualUnimplementedAfterRelease')):raise ValueError('release must use same original listener and owner')
 loss=value['ackLoss'];unknown=scenario=='redis-unknown'
 if loss.get('version')!='real-redis-exact-del-ack-loss-v1' or loss.get('exactDeleteRequests')!=int(unknown) or loss.get('actualReplyDiscarded')!=unknown or loss.get('transportFailure') is not False or unknown and loss.get('actualUpstreamIntegerOne') is not True:raise ValueError('exact actual Redis ACK outcome required')
 if value['actualTokenRPCSucceeded']!=(scenario=='ack') or value['canonicalShutdown']!=(scenario=='ack') or value['unknownOwnerRetained']!=(scenario=='redis-unknown'):raise ValueError('known/unknown boundary differs')
 return {'scenario':scenario,'accepted':True,'source':source,'actualStartedRoles':14,'actualAbsentRoles':14,'readerPhases':names,'tokenRPCDispatches':1,'tokenSucceeded':value['actualTokenRPCSucceeded'],'canonicalShutdown':value['canonicalShutdown'],'unknownOwnerRetained':value['unknownOwnerRetained'],'mongoMessageCount':0,'businessPermissions':permissions,'originalResultSHA256':None}

def tool_snapshot():
 names={'python':sys.executable,'go':shutil.which('go'),'gcc':shutil.which('gcc'),'git':'/usr/bin/git','docker':'/usr/bin/docker','sudo':'/usr/bin/sudo','nsenter':'/usr/bin/nsenter','setpriv':'/usr/bin/setpriv','env':'/usr/bin/env'}
 result={}
 for role,path in names.items():
  if path is None:raise ValueError('required hosted tool absent')
  p=Path(path).resolve();raw=regular(p)
  result[role]={'path':str(p),'bytes':len(raw),'sha256':sha(raw),'mode':stat.S_IMODE(p.stat().st_mode)}
 return result

def job_identity():
 return {'version':'owned-fullgraph-ci-allocation-v1','uid':os.getuid(),'runID':os.environ['GITHUB_RUN_ID'],'attempt':os.environ['GITHUB_RUN_ATTEMPT'],'head':os.environ['GITHUB_SHA']}
def allocation_root():
 run,attempt=os.environ['GITHUB_RUN_ID'],os.environ['GITHUB_RUN_ATTEMPT']
 if not run.isdecimal() or not attempt.isdecimal() or not re.fullmatch('[0-9a-f]{40}',os.environ['GITHUB_SHA']):raise ValueError('exact original hosted attempt required')
 return Path(os.environ['RUNNER_TEMP'])/('backend-fullgraph-'+run+'-'+attempt)
def admit():
 c,_,_,_,_,_=verify();root=allocation_root()
 if root.resolve()!=root or root.exists() or os.statvfs(root.parent).f_bavail*os.statvfs(root.parent).f_frsize<c['resource']['initialBytes']:raise ValueError('fresh allocation and cold4GiB admission required')
 root.mkdir(mode=0o700);write(root/'allocation-owner.json',job_identity())
 out=REPO/'ci-results';out.mkdir(exist_ok=True)
 write(out/'backend-fullgraph-admission.json',{'accepted':True,'minimumInitialBytes':c['resource']['initialBytes'],'source':c['source'],'businessDeployment':False})

def source_post(root,bodies,c,template):
 post={'accepted':False,'source':FINAL_SOURCE}
 try:
  for target,raw in bodies.items():
   p=root/target
   if regular(p)!=raw or stat.S_IMODE(p.stat().st_mode)!=c['sourceModes'][target]:raise ValueError('whole source or mode drift')
  if regular(root/'server/config/config.yaml')!=template or stat.S_IMODE((root/'server/config/config.yaml').stat().st_mode)!=0o600:raise ValueError('generated example config drift')
  verify();post['accepted']=True
 except Exception as error:post['exceptionType']=type(error).__name__
 return post

def run():
 fixture_token=os.environ.pop('GH_TOKEN',None)  # API-only; never inherited by Go/Docker/operator children.
 if any(os.environ.get(k)!=v for k,v in {'GITHUB_ACTIONS':'true','RUNNER_ENVIRONMENT':'github-hosted','RUNNER_OS':'Linux','GOTOOLCHAIN':'local','GOWORK':'off'}.items()) or os.getuid()==0:raise ValueError('disposable hosted nonroot Linux only')
 c,bodies,recipe,parent,old,catalog=verify();started=time.monotonic();temporary=Path(os.environ['RUNNER_TEMP']);allocation=allocation_root();root=allocation/'work'
 if temporary.resolve()!=temporary or allocation.resolve()!=allocation or root.exists() or os.statvfs(temporary).f_bavail*os.statvfs(temporary).f_frsize<c['resource']['initialBytes']:raise ValueError('fresh owned root and initial floor required')
 if json.loads(regular(allocation/'allocation-owner.json'))!=job_identity():raise ValueError('original allocation admission differs')
 root.mkdir(mode=0o700);private=root/'private';private.mkdir(mode=0o700);out=REPO/'ci-results/backend-fullgraph';out.mkdir(mode=0o700,parents=True,exist_ok=False)
 write(private/'job-owner.json',{'version':'owned-fullgraph-ci-job-v1','uid':os.getuid(),'runID':os.environ['GITHUB_RUN_ID'],'attempt':os.environ['GITHUB_RUN_ATTEMPT'],'head':os.environ['GITHUB_SHA'],'source':FINAL_SOURCE})
 for target,raw in bodies.items():
  p=root/target;p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(raw);p.chmod(c['sourceModes'][target])
 template=bodies['server/config/config.example.yaml'];(root/'server/config/config.yaml').write_bytes(template);(root/'server/config/config.yaml').chmod(0o600)
 write(private/'source-materialized.json',{'source':FINAL_SOURCE,'accepted':True})
 primary=None
 try:
  tools=tool_snapshot();write(out/'tools-before.json',tools)
  builder=module('ci/backend-fullgraph/minio_build.py','owned_minio_source_builder');resources=module('ci/backend-fullgraph/resources.py','owned_fullgraph_resources');selectors=module('ci/backend-fullgraph/service_selectors.py','owned_fullgraph_selectors')
  command_index=0
  def command(label,argv,timeout,env=None,cwd=None):
   nonlocal command_index
   command_index+=1;remaining=c['resource']['controllerSeconds']-(time.monotonic()-started)
   if remaining<1:raise ValueError('controller budget exhausted')
   timeout=max(1,int(min(timeout,remaining)));folder=private/(f'{command_index:04d}-'+label);folder.mkdir(mode=0o700)
   args=[sys.executable,'-B',str(REPO/'ci/group-effects/capture.py'),'--stdout',str(folder/'stdout'),'--stderr',str(folder/'stderr'),'--']
   if env is not None:args+=['/usr/bin/env','-i']+[k+'='+v for k,v in env.items()]
   args+=argv
   wrapped=[sys.executable,'-B',str(REPO/'ci/group-effects/resource_guard.py'),'--cwd',str(cwd or root/'server'),'--log',str(folder/'guard.log'),'--record',str(folder/'resource.json'),'--min-free-mib','1024','--timeout',str(timeout),'--poll-interval','.2','--allocated-root',str(root),'--max-allocated-mib','16384','--']+args
   child=subprocess.run(wrapped,check=False,timeout=timeout+30);resource=json.loads(regular(folder/'resource.json'))
   failed=child.returncode!=0 or resource.get('status')!='passed' or resource.get('exit_code')!=0
   raw_custody={}
   for name in ('stdout','stderr'):
    try:
     value=regular(folder/name,32<<20);raw_custody[name]={'bytes':len(value),'sha256':sha(value),'private':True}
    except Exception as error:raw_custody[name]={'private':True,'exceptionType':type(error).__name__}
   write(out/(f'phase-{command_index:04d}.json'),{'phase':label,'exitCode':child.returncode,'resource':resource,'rawCustody':raw_custody})
   if failed and (label.startswith('compile-') or label=='minio-source-compile'):
    # These commands run solely on reviewed public source, before any private
    # resource credentials/configs exist. No operator/DB/SDK text is selected.
    raw=regular(folder/'stderr',32<<20) if (folder/'stderr').exists() else b''
    write(out/(f'compile-failure-{command_index:04d}.json'),{'phase':label,'publicSourceOnly':True,'completeBytes':len(raw),'completeSHA256':sha(raw),'diagnostic':raw[:65536].decode('utf-8','replace'),'truncated':len(raw)>65536})
   fixture_observations=None
   if label in {'pending-two-original-oracles','launcher-ownedcontrol','redis-parser'} or label in {scope['label'] for scope in CLEANUP_SCOPES}:
    raw_events=regular(folder/'stdout',32<<20)
    if label=='cleanup-runtimeacceptance':
     fixture_observations=runtime_cleanup_observations(raw_events,label)
     write(out/'runtime-cleanup-observations.json',dict(fixture_observations,source=FINAL_SOURCE))
    events,diagnostic=project_go_test_events(raw_events,label)
    if diagnostic is not None:write(out/(f'go-test-compile-diagnostic-{command_index:04d}.json'),diagnostic)
    write(out/('oracle-events.json' if label=='pending-two-original-oracles' else label+'-events.json'),events)
   if failed:raise ValueError('actual phase failed; private raw retained')
   return regular(folder/'stdout',32<<20)
  env={'PATH':os.environ['PATH'],'HOME':os.environ['HOME'],'GOTOOLCHAIN':'local','GOWORK':'off','GOENV':'off','GOAUTH':'off','GOMAXPROCS':'2','GOMEMLIMIT':'900MiB','CGO_ENABLED':'1'}
  for key,name in [('GOMODCACHE','modcache'),('GOCACHE','buildcache'),('GOPATH','gopath'),('TMPDIR','tmp')]:
   p=root/name;p.mkdir(mode=0o700);env[key]=str(p)
  env['GOTMPDIR']=env['TMPDIR'];results=[]
  env['GOROOT']=command('go-root',['go','env','GOROOT'],15,env).decode().strip();go=str(Path(env['GOROOT'])/'bin/go')
  if command('go-version',[go,'version'],15,env).decode().strip()!='go version go1.27.2 linux/amd64':raise ValueError('exact Go1.27.2 required')
  tool_sha=sha(regular(Path(go).resolve()))
  if tool_sha!=tools['go']['sha256']:raise ValueError('Go command differs from observed original tool')
  reused_fixture=None
  if c['minioFixtureBinding'] is not None:
   reused_fixture=builder.receive(root,catalog,c['minioFixtureBinding'],tool_sha,fixture_token,os.environ['GITHUB_REPOSITORY'])
  fixture_token=None
  command('app-module-download',[go,'mod','download','-json'],600,dict(env,GOPROXY='https://proxy.golang.org',GOSUMDB='sum.golang.org'))
  offline=dict(env,GOPROXY='off',GOSUMDB='off')
  redis_parser_reuse=load_redis_parser_reuse(out,bodies)
  launcher_reuse=load_launcher_reuse(out)
  cleanup_audit=load_cleanup_reuse(out)
  binroot=root/'bin';binroot.mkdir(mode=0o700);binaries=[];build_start=time.monotonic()
  entries=[{'role':'acceptance-operator','package':'./cmd/tools/owned-fullgraph-acceptance'}]+c['entrypointContract']['entrypoints']+[{'role':'msgtransfer-legacy','package':'./cmd/openim-msgtransfer'}]
  for entry in entries:
   role=entry['role'];binary=binroot/role;remaining=1200-(time.monotonic()-build_start)
   if remaining<1:raise ValueError('actual14 and auxiliary compile budget exhausted')
   command('compile-'+role,[go,'build','-mod=readonly','-p=1','-o',str(binary),entry['package']],int(remaining),offline)
   raw=builder.stable(binary)
   if raw[:6]!=b'\x7fELF\x02\x01' or len(raw)<20 or raw[18:20]!=b'\x3e\x00':raise ValueError('actual linux amd64 executable required')
   receipt={'schema':1,'sourceSHA256':FINAL_SOURCE['digest'],'binarySHA256':sha(raw),'binaryBytes':len(raw),'toolchainSHA256':tool_sha};receiptpath=binroot/(role+'-receipt.json');receiptsha=write(receiptpath,receipt)
   binaries.append({'role':role,'path':str(binary),'sha256':sha(raw),'sourceSHA256':FINAL_SOURCE['digest'],'receiptPath':str(receiptpath),'receiptSHA256':receiptsha,'services':selectors.SERVICES.get(role,[]),'components':selectors.COMPONENTS.get(role,[])})
   write(out/('compiled-'+role+'.json'),{'role':role,'package':entry['package'],'source':FINAL_SOURCE,'bytes':len(raw),'sha256':sha(raw),'executed':False})
  builds={'version':'source-bound-fullgraph-builds-v1','sourceSHA256':FINAL_SOURCE['digest'],'binaries':[b for b in binaries if b['role'] in ROLES],'legacyMessageTransfer':next(b for b in binaries if b['role']=='msgtransfer-legacy')};buildpath=private/'builds.json';buildsha=write(buildpath,builds);operator=next(b['path'] for b in binaries if b['role']=='acceptance-operator')
  if c['minioFixtureBinding'] is None:
   minio,receipt=builder.build(root,catalog,command,go,tool_sha,env)
   write(out/'source-minio-acquisition.json',{'mode':'original-source-build','fixtureOnly':True,'productRuntimeAccepted':False})
  else:
   minio,receipt=reused_fixture
   write(out/'source-minio-acquisition.json',{'mode':'root-accepted-fixture-artifact','fixtureOnly':True,'productRuntimeAccepted':False,'artifactID':c['minioFixtureBinding']['artifactID'],'rootAcceptanceSHA256':c['minioFixtureBinding']['rootAcceptanceSHA256'],'freshMinioCompile':False})
  write(out/'source-minio-build.json',receipt)
  for image in sorted(set(catalog['images'].values())):command('image-acquire-'+str(len(results))+'-'+sha(image.encode())[:10],['/usr/bin/docker','--host','unix:///var/run/docker.sock','pull','--platform','linux/amd64',image],600)
  for scenario in SCENARIOS:
   if c['resource']['controllerSeconds']-(time.monotonic()-started)<c['resource']['scenarioSeconds']+180:raise ValueError('complete operator-context plus original cleanup budget required before new plane')
   plane=None;scenario_error=None
   try:
    plane=resources.Plane(root/('plane-'+scenario),catalog['images'],minio_source=minio)
    claim=plane.prepare();plane_path=plane.private/'plane.json';cred=plane.private/'credentials.json';work=root/('scenario-'+scenario);work.mkdir(mode=0o700)
    p={'version':'owned-fullgraph-acceptance-v1','scenario':scenario,'work':str(work),'planePath':str(plane_path),'planeSHA256':sha(regular(plane_path)),'credentialsPath':str(cred),'credentialsSHA256':sha(regular(cred)),'buildsPath':str(buildpath),'buildsSHA256':buildsha};pp=private/(scenario+'-input.json');psha=write(pp,p)
    keeper=next(r for r in claim['observations'] if r['role']=='keeper');groups=','.join(str(g) for g in sorted(set(os.getgroups()+[os.getgid()])))
    args=['sudo','-n','/usr/bin/nsenter','--net=/proc/'+str(keeper['PID'])+'/ns/net','--','/usr/bin/setpriv','--reuid',str(os.getuid()),'--regid',str(os.getgid()),'--groups',groups,'--no-new-privs','--','/usr/bin/env','-i','PATH=/usr/bin:/bin','HOME='+os.environ['HOME'],'TMPDIR='+str(work),operator,'--private-input',str(pp),'--private-input-sha256',psha]
    write(work/'operator-intent.json',{'version':'owned-fullgraph-operator-intent-v1','scenario':scenario,'privateInputSHA256':psha,'operatorSHA256':next(b['sha256'] for b in binaries if b['role']=='acceptance-operator'),'sourceSHA256':FINAL_SOURCE['digest']})
    command('runtime-'+scenario,args,1380)
    result_path=work/'result.json';raw=regular(result_path,1<<20);value=json.loads(raw);safe=check_result(value,scenario,FINAL_SOURCE['digest'],builds,p['planeSHA256']);safe['originalResultSHA256']=sha(raw);write(out/(scenario+'-result.json'),safe);results.append((scenario,result_path,sha(raw)))
   except BaseException as error:scenario_error=error;raise
   finally:
    if plane is not None:
     try:
      cleanup=plane.cleanup()
      write(out/('resource-cleanup-'+scenario+'.json'),public_cleanup(cleanup))
     except Exception:
      if scenario_error is None:raise
  tests_env=dict(offline,LUOBO_FULLGRAPH_SOURCE_SHA256=FINAL_SOURCE['digest'])
  for scenario,path,pin_sha in results:
   prefix='LUOBO_FULLGRAPH_'+scenario.upper().replace('-','_');tests_env[prefix+'_RESULT']=str(path);tests_env[prefix+'_SHA256']=pin_sha
  pattern='^(TestOwnedPreparedReaderRegistersBeforeServeAndRetainsFinalWriterFence|TestOwnedPushActualOwnerClosesOriginalResourcesBeforeRedisUnknownNonpermit)$/^(registered-not-serving-before-readiness|known-readiness-still-keeps-writer-fence|required-token-only)$'
  raw=command('pending-two-original-oracles',[go,'test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run',pattern,'./pkg/common/startrpc'],240,tests_env)
  events=[json.loads(x) for x in raw.decode().splitlines() if x.strip()];tops={'pkg/common/startrpc':['TestOwnedPreparedReaderRegistersBeforeServeAndRetainsFinalWriterFence','TestOwnedPushActualOwnerClosesOriginalResourcesBeforeRedisUnknownNonpermit']};audit=parent.audit(events,0,tops)
  expected={tops['pkg/common/startrpc'][0]+'/'+s for s in ('registered-not-serving-before-readiness','known-readiness-still-keeps-writer-fence')}|{tops['pkg/common/startrpc'][1]+'/required-token-only'}
  if {r['name'] for r in audit['namedCompletions'] if '/' in r['name']}!=expected:raise ValueError('exact original two tops/three children required')
  write(out/'oracle-names.json',audit)
  targeted_audit=audit_current_oracles(audit,cleanup_audit)
  targeted_audit=dict(targeted_audit,launcherPassedReuse=check_launcher_reuse(launcher_reuse),actualLauncherTestsDispatched=0)
  write(out/'targeted-names.json',targeted_audit)
 except BaseException as error:primary=error;raise
 finally:
  post=source_post(root,bodies,c,template)
  try:
   after=tool_snapshot();write(out/'tools-after.json',after)
   if after!=tools:raise ValueError('hosted tool body/mode changed')
  except Exception as error:post['accepted']=False;post['toolExceptionType']=type(error).__name__
  try:write(out/'source-post.json',post)
  except Exception:
   if primary is None:raise
  if not post['accepted'] and primary is None:raise ValueError('source-post rejected')
 write(out/'summary.json',{'accepted':True,'source':FINAL_SOURCE,'actualCurrentMainBuilds':14,'auxiliaryBuilds':2,'freshGraphScenarios':4,'pendingOriginalTopPASS':2,'pendingOriginalSubPASS':3,'cleanupTopPASS':0,'cleanupSubPASS':0,'cleanupPassedReuse':CLEANUP_PASSED_REUSE,'launcherTopPASS':0,'launcherSubPASS':0,'launcherPassedReuse':LAUNCHER_PASSED_REUSE,'actualLauncherTestsDispatched':0,'targetedTopPASS':2,'targetedSubPASS':3,'redisParserTopPASS':0,'redisParserSubPASS':0,'redisParserHistoricalTopPASS':1,'redisParserHistoricalSubPASS':3,'actualRedisParserTestsDispatched':0,'redisParserPassedReuse':redis_parser_reuse,'cleanupReuse':dict(CLEANUP_REUSE,topPASS=0,subPASS=1),'cleanupRecentReuse':CLEANUP_RECENT_REUSE,'originalObligation':{'topCount':5,'subCount':12,'crossSource':True},'newSafety':{'topCount':2,'subCount':4,'crossSource':True},'completeObligation':{'topCount':7,'subCount':16,'crossSource':True},'old67or21TestsRerun':0,'reuseOldBinary':False,'deployment':False,'resourceCleanup':'must independently pass always collector','privateLogsUploaded':False})
 return 0

if __name__=='__main__':
 p=argparse.ArgumentParser();g=p.add_mutually_exclusive_group();g.add_argument('--verify',action='store_true');g.add_argument('--admit',action='store_true');a=p.parse_args()
 if a.verify:
  c,_,_,_,_,_=verify();print(json.dumps({'source':c['source'],'actualRun':False,'Go':0,'DB':0,'containers':0}))
 elif a.admit:admit()
 else:raise SystemExit(run())
