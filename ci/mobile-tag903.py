#!/usr/bin/env python3
"""Manual codec903/905/910/911: fresh format5/analysis5 + same-body old analysis4, exact15."""
import argparse, hashlib, importlib.util, json, os, re, selectors, signal, stat
import subprocess, sys, threading, time
from pathlib import Path, PurePosixPath
from urllib.parse import unquote, urlparse

REPO=Path(__file__).resolve().parent.parent
INPUTS='ci/mobile-tag903/inputs.json'
PARENT_COMMIT='2f17d99d2f32c4fc72b9b922f8d2a8783938b665'
PARENT_SOURCE={'members':363,'digest':'ccec31d13c6e64bb4d0c9ff0b1b85baae3d50d198c743d34e9935a053d17ecd8'}
PARENT_PORTABLE={'members':415,'digest':'30879a35431337689eced3a9b5ce9a7c528342740be0be502053100b7be24d7d'}
PATHS10=['lib/features/chat/domain/chat_message.dart', 'lib/features/chat/data/message_mapper.dart', 'lib/features/chat/presentation/message_media.dart', 'lib/features/chat/presentation/chat_page.dart', 'test/features/chat/legacy_custom_summary_test.dart', 'test/features/chat/batch_media_access_test.dart', 'lib/features/chat/data/chat_repository.dart', 'lib/features/chat/presentation/chat_controller.dart', 'lib/features/contacts/presentation/user_profile_page.dart', 'test/features/chat/chat_forward_page_test.dart']
CHECK_PATHS5=['lib/features/chat/data/chat_repository.dart', 'lib/features/chat/presentation/chat_controller.dart', 'lib/features/chat/presentation/chat_page.dart', 'lib/features/contacts/presentation/user_profile_page.dart', 'test/features/chat/legacy_custom_summary_test.dart']
TEST_PATHS=['test/features/chat/legacy_custom_summary_test.dart', 'test/features/chat/chat_forward_page_test.dart', 'test/features/chat/batch_media_access_test.dart', 'test/features/chat/chat_card_workflow_test.dart']
NAMES15=['CID clear during native local insert retires the original timeline before its late ACK', 'view-only private relationship failures preserve the failed source without native hints or remount reconstruction', 'view-only inspection-unknown relationship failures preserve the failed source without native hints or remount reconstruction', 'view-only relationship hints retire on deletion clear expiry logout and route loss without renewed authority', 'private view-only 911 opens only the original reason flow and confirms the current peer without native hint storage', 'private view-only 911 retained reason cannot apply after clear issuer expiry or its owned route exits', 'private send failure arriving under another route cannot create a late view hint when the chat returns', 'tag903 text metadata forwards through the actual menu and preserves custom JSON', 'meeting905 card displays original fields and forwards the unchanged custom invitation', 'mixed tag903 text keeps copy and reply while raw voice still prevents forwarding', 'tag903 audio starts only on tap and keeps the custom message menu', 'ordinary contact profile without a message source keeps its normal friend request and dialog behavior', 'clear during the requestFriend inner profile await prevents the real final addFriend dispatch', 'foreign coverage of the owned friend-reason dialog retires its retained submit without a new action', '911 hint opens the actual reason dialog once and confirms only the current single peer with no message send']
FINAL_SOURCE={'members': 363, 'digest': 'b981685961f1b866679e48541e82a9e01cab7d39b084d300838458863ebaadac'}
FINAL_PORTABLE={'members': 415, 'digest': '163f69efd5827efebfc4c3d75e5fb16ebeed08ebb50f1e18a1827ebe494c6eba'}
ANALYSIS_REUSE4={'status': 'same-body-old-source-analysis-pass-only', 'source': {'members': 363, 'digest': 'e05419bfbd52e3c27a80c192bc9d3856569cc112b1899afcf2099ebf4d5c81e4'}, 'portable': {'members': 415, 'digest': 'd4fb9703b093fa7134e198bca1052d6e39e4854e9eaabf3382bd9eae54d0ab8a'}, 'head': '1ad378b0367dfe22fe1ef884cbf54def5dfe0218', 'run': 39089287566, 'job': 117562354285, 'compactArtifact': {'artifact': 12122335814, 'run': 39089287566, 'attempt': 1, 'head': '1ad378b0367dfe22fe1ef884cbf54def5dfe0218', 'sha256': '6e2ad33b192634e0d6276998646f369a1fea29c4cd14fe47179cb32e3e97227b', 'bytes': 40926, 'exactAPIDigest': True, 'files': 44, 'binariesDownloaded': False}, 'RootReview': {'path': '.qa/mobile363-codec-combined-batch-1/root-1ad378-format9-analysis6-pass-3-lint-failure-review.json', 'bytes': 16578, 'sha256': '34047d8f714d47756b707023128927c068b8384c8ed37a36cd22097b87bc9e71'}, 'oldParentWholeAccepted': False, 'oldParentAnalysisFailed3': 3, 'oldParent35TestsDispatched': 0, 'originalSourceToolsPostExact': True, 'originalResourceHealthy': True, 'originalToolsBefore': {'path': 'tag903-tools-before.json', 'bytes': 477, 'sha256': 'ec3c14ec1cc96197a2500e88d7b7329b2316a43b6f520ead4f36152ac44469ae'}, 'originalToolsAfter': {'path': 'tag903-tools-after.json', 'bytes': 477, 'sha256': 'ec3c14ec1cc96197a2500e88d7b7329b2316a43b6f520ead4f36152ac44469ae'}, 'originalSourcePost': {'path': 'tag903-source-post.json', 'bytes': 373, 'sha256': '25912e854941a13ada0d0e586782f15bc1a8aea0ba3e022281357f80df3defb0'}, 'freshAnalysisOnCurrentSource': 0, 'rows': [{'path': 'lib/features/chat/data/message_mapper.dart', 'originalPhase': 'tag903-analyze-2', 'source': {'path': 'app/lib/features/chat/data/message_mapper.dart', 'bytes': 16441, 'sha256': 'aa2a72c6998e0052b151df4882bc0a00063680b36729978aad80967f01c30c80'}, 'passed': True, 'diagnostics': 0, 'summary': {'path': 'tag903-analyze-2.summary.json', 'bytes': 128, 'sha256': '1e0bb38c2df2e516ab148f93634f7ff4acb85ed49db7eebfddee748e431ba296'}, 'log': {'path': 'tag903-analyze-2.log', 'bytes': 50, 'sha256': '4e5c3aee8c7d8c0cffa1404ac38d8ebd9727a07f1323f2c2f7a6e6e37fb6e35e'}, 'resource': {'path': 'tag903-analyze-2.resource.json', 'bytes': 484, 'sha256': 'b8e0ed3b5c47c8c1d5a363b99aba2fe159ebe9898b5d349f5e099d8a1fe1e8f3'}}, {'path': 'lib/features/chat/domain/chat_message.dart', 'originalPhase': 'tag903-analyze-3', 'source': {'path': 'app/lib/features/chat/domain/chat_message.dart', 'bytes': 6709, 'sha256': '1d5ed4de6c2b529a9fba28dd4c52455a07aae2f021f4ff0ea411abc6ddb59167'}, 'passed': True, 'diagnostics': 0, 'summary': {'path': 'tag903-analyze-3.summary.json', 'bytes': 128, 'sha256': '5e2e1e465cb8127b661d2ffbc60592d51c3ba11b22ec508260fa73a65de45f8d'}, 'log': {'path': 'tag903-analyze-3.log', 'bytes': 48, 'sha256': '5fdf54ab57dcca04883f5e834cb59e650154702283d6bb71c3753d2f56fd35dd'}, 'resource': {'path': 'tag903-analyze-3.resource.json', 'bytes': 484, 'sha256': '36393ddf75677128017d442b1b133ab9f81144bca850edaed848709fb4a9d6ad'}}, {'path': 'test/features/chat/batch_media_access_test.dart', 'originalPhase': 'tag903-analyze-7', 'source': {'path': 'app/test/features/chat/batch_media_access_test.dart', 'bytes': 16389, 'sha256': '32e1921721943ffa66269278065734628db20a685bca1cfcd8d1e4e55fa31cdc'}, 'passed': True, 'diagnostics': 0, 'summary': {'path': 'tag903-analyze-7.summary.json', 'bytes': 133, 'sha256': '56a4c629fd0f641b7b83fa79e457774e0e74892259ee18b30e1be2fc83f361e5'}, 'log': {'path': 'tag903-analyze-7.log', 'bytes': 59, 'sha256': 'f0fb86c444ad88b3bc18149078d31e6c1254c7f9fd0757d940fcb48f4f984d28'}, 'resource': {'path': 'tag903-analyze-7.resource.json', 'bytes': 489, 'sha256': '2f0aa9fbb92f03df49ead4feaf68df213a47542d63ee29ba73bfea050ad36092'}}, {'path': 'test/features/chat/chat_forward_page_test.dart', 'originalPhase': 'tag903-analyze-8', 'source': {'path': 'app/test/features/chat/chat_forward_page_test.dart', 'bytes': 18698, 'sha256': 'ff98f642c331c2dc83cbc0328ecd8d8557b3a90ddba047035ff605a76f1ec7b8'}, 'passed': True, 'diagnostics': 0, 'summary': {'path': 'tag903-analyze-8.summary.json', 'bytes': 132, 'sha256': '8082563d716c11a29227ce76fa543e62b45b8eccf96f402af9948f706c1ac8ce'}, 'log': {'path': 'tag903-analyze-8.log', 'bytes': 58, 'sha256': '0b017247a75853c9952d6376e0816dbb6ef545317915f1a017927edf8f79b87c'}, 'resource': {'path': 'tag903-analyze-8.resource.json', 'bytes': 489, 'sha256': '9422a38f1f75262b9ce50adf2c86c029ce26a51c473c1380cc941560a86ce001'}}]}
HISTORICAL_PARTIAL26={'status': 'historical-visible-testDone-only-parent-failed', 'run': 39090130182, 'job': 117564932427, 'head': 'cfc194fef4a69bb15be1a31b78fcead10cd21f1a', 'source': {'digest': '481546066d6bada56c6e46498eea0dfb39cec580878546c29ea772e92dc0adbc', 'members': 363}, 'portable': {'digest': 'a3a88bb8da2de8dd6853fe4cea3c1653b1aa5857a32e1bdd3f22ed864983fa94', 'members': 415}, 'compactArtifact': {'artifact': 12122341798, 'run': 39090130182, 'attempt': 1, 'head': 'cfc194fef4a69bb15be1a31b78fcead10cd21f1a', 'sha256': 'db9836736e01b2fdd7104dfe39b9e6fbe8bc61d48f5ccb59056cbe6da5c19a73', 'bytes': 42464, 'exactAPIDigest': True, 'files': 29, 'binariesDownloaded': False}, 'RootReview': {'path': '.qa/mobile363-codec-lint-batch-1/root-cfc194-mobile35-34-pass-clear-late-ack-failure-review.json', 'bytes': 1622, 'sha256': '4c8c93f49189c30a560df448043184641dedf06ec009ae65025423e3f372e344'}, 'machineLog': {'path': '.qa/mobile363-codec-lint-batch-1/mobile-artifact/tag903-target35.log', 'bytes': 26660, 'sha256': 'c9c4f5280aebd0d263b127e98ca6c9874e61c335057201091364e7278d77e361'}, 'resource': {'path': '.qa/mobile363-codec-lint-batch-1/mobile-artifact/tag903-target35.resource.json', 'bytes': 4453, 'sha256': 'cae5211702b8053cb1f413b26c2c990b85ad4e73cd1326e1ff650df5350183c7'}, 'sourcePost': {'path': '.qa/mobile363-codec-lint-batch-1/mobile-artifact/tag903-source-post.json', 'bytes': 373, 'sha256': '969bf09560d4d4111cb15617e15a8702c4cf42f3baad951f88a8ed51298b3429'}, 'names26': ['tag903 voice and malformed text cannot bypass the native forwarding gate', 'tag903 forwarding retains original owner and privacy fences while native creation waits', 'meeting905 malformed fields cannot gain native forwarding authority', 'meeting905 retains original owner and privacy fences while native creation waits', 'tag903 text uses the actual legacy textElem payload', 'tag903 sound has a restricted audio projection without changing custom identity', 'tag903 keeps text priority and rejects unsafe or malformed audio projections', 'real private root still masks every known custom conversation preview', 'standard107 nested custom summaries keep the original joint privacy', 'meeting905 is readable without manufacturing a join action or URL', 'wrong typed known payload fields fall back without string coercion', 'real send 1302 keeps the failed row and original error while inserting only one read local hint', 'real send 1303 keeps the failed row and original error while inserting only one read local hint', 'ordinary unopted send and non-relationship failure never create a local side effect', 'local hint create failure is observable and never replaces or replays the original failure', 'local hint unknown is observable and never replaces or replays the original failure', 'source expiry while create is pending prevents LOCAL insert and never renews the original burn window', 'private source keeps its original privacy and suppresses a persistent hint without a new burn window', '911 retained reason cannot apply after original clear invalidation', '911 retained reason cannot apply after original owner invalidation', '911 retained reason cannot apply after original issuer invalidation', '911 reason retained after its owned profile exits cannot dispatch a friend request', '910 has no friend-request action or ordinary message menu', 'stricter ledger facts than the raw send suppress durable hints without rewriting the original failure', 'damaged LOCAL ACK remains unknown and the already stored native hint is hidden after remount without replay', 'late stricter source facts never upgrade a pending hint and remain effective for a known hint after remount'], 'parentStarted35': 35, 'parentTestDoneSuccess34': 34, 'parentFailed1': ['CID clear during native local insert retires the original timeline before its late ACK'], 'wholeProcessPASS': False, 'currentSourcePASS': False, 'nativeAcceptance': False}
HISTORICAL_PARTIAL5={'status': 'historical-visible-testDone-only-parent-failed', 'run': 39084923688, 'job': 117549360631, 'head': 'bff3ba487899c485c77aaedea695be5a167a063b', 'source': {'members': 363, 'digest': 'e8ef155193d61b94942e29afc269ddf995f12b8dcba6b3253ddaa1b8a050d316'}, 'portable': {'members': 415, 'digest': '8d42e4660335bc172ab40f24369318098429410ee6318af15f25205fb10b7781'}, 'RootReview': {'path': '.qa/tag903-mobile-web-batch-1/root-bff3-mobile7-pass-route-exit-timeout-review.json', 'bytes': 4249, 'sha256': 'ce892b9dc224d426b9fcb3c3e0f10ae71658324316f1240320b9594e7502a998'}, 'compactArtifact': {'artifact': 12119744075, 'run': 39084923688, 'attempt': 1, 'head': 'bff3ba487899c485c77aaedea695be5a167a063b', 'sha256': '758cfbeaa4fe2bab416b879abc59a6e09d2d69ea359be094738e668289b2e07e', 'bytes': 40412, 'exactAPIDigest': True, 'files': 36, 'binariesDownloaded': False}, 'names5': ['tag903 sound has a restricted audio projection without changing custom identity', 'tag903 keeps text priority and rejects unsafe or malformed audio projections', 'wrong typed known payload fields fall back without string coercion', 'nested audio active playback stops after root expiry', 'nested audio late setUrl cannot start after root exit'], 'parentWholeSelected8Accepted': False, 'machineDoneSuccess': False, 'latchedFailure': 'phase_timeout', 'childExitCodeIsNotAcceptance': 0, 'wholeProcessPASS': False, 'newSourcePASS': False}
NAMES8=['tag903 sound has a restricted audio projection without changing custom identity','tag903 keeps text priority and rejects unsafe or malformed audio projections','tag903 audio starts only on tap and keeps the custom message menu','tag903 pending audio cannot start after expiry','tag903 pending audio cannot start after route-exit','wrong typed known payload fields fall back without string coercion','nested audio active playback stops after root expiry','nested audio late setUrl cannot start after root exit']
RESOURCE={'initial_bytes':4<<30,'floor_bytes':1<<30,'allocated_cap_bytes':768<<20,'poll_seconds':.2,'phase_seconds':120}
sha=lambda raw:hashlib.sha256(raw).hexdigest()
def safe(name):
 p=PurePosixPath(name)
 if not name or p.is_absolute() or str(p)!=name or '\\' in name or any(x in name for x in ('\x00',':')) or any(x in ('','.','..') for x in name.split('/')):raise ValueError('finite canonical source path required')
 return p
def regular(path,limit=8<<20):
 path=Path(path)
 if path.resolve()!=path or not stat.S_ISREG(path.lstat().st_mode) or path.stat().st_size>limit:raise ValueError('bounded canonical regular file required')
 raw=path.read_bytes()
 if len(raw)>limit:raise ValueError('source bound exceeded')
 return raw
def pinned(path,row):
 raw=regular(path)
 if len(raw)!=row['bytes'] or sha(raw)!=row['sha256']:raise ValueError('source pin differs: '+row['path'])
 return raw
def rows(files):return [{'path':n,'bytes':len(raw),'sha256':sha(raw)} for n,raw in sorted(files.items())]
def digest(files):return sha(json.dumps(rows(files),ensure_ascii=False,sort_keys=True,separators=(',',':')).encode())
def write(path,value):
 raw=(json.dumps(value,ensure_ascii=False,sort_keys=True,indent=2)+'\n').encode()
 with path.open('xb') as f:f.write(raw);f.flush();os.fsync(f.fileno())
def load(path,label):
 spec=importlib.util.spec_from_file_location(label,path);value=importlib.util.module_from_spec(spec);spec.loader.exec_module(value);return value
def authored_paths(repo):
 actual=set()
 for prefix in ('app/lib','app/test','app/tool'):
  for directory,dirs,names in os.walk(repo/prefix,followlinks=False):
   if any((Path(directory)/name).is_symlink() for name in dirs):raise ValueError('authored directory symlink')
   actual.update(str((Path(directory)/name).relative_to(repo)) for name in names)
 return actual
def analysis_reuse4(c,repo=REPO):
 # Only four unchanged old analyzer results; no download/re-analysis occurs.
 proof=c.get('analysis_reuse4')
 if c.get('source')!=FINAL_SOURCE or c.get('portable')!=FINAL_PORTABLE or proof!=ANALYSIS_REUSE4:raise ValueError('exact same-body old-source analysis4 proof required')
 if len(proof['rows'])!=4 or len({r['path'] for r in proof['rows']})!=4 or set(CHECK_PATHS5)&{r['path'] for r in proof['rows']}:raise ValueError('fresh5 and original4 must be disjoint')
 for row in proof['rows']:
  pinned(repo/safe(row['source']['path']),row['source'])
 return {'status':'same-body-old-source-analysis-pass-only','currentSource':FINAL_SOURCE,'currentPortable':FINAL_PORTABLE,'freshAnalysisOnCurrentSource':0,'sameBodyOldAnalysisCount':4,'originalProof':proof}
def descriptor(repo=REPO):
 c=json.loads(regular(repo/INPUTS))
 if (c['schema']!=1 or c['state']!='FROZEN_FORMATTED_SOURCE' or c['parent_commit']!=PARENT_COMMIT
     or c['parent_source']!=PARENT_SOURCE or c['parent_portable']!=PARENT_PORTABLE
     or c['source']!=FINAL_SOURCE or c['portable']!=FINAL_PORTABLE
     or c['analysis_paths']!=CHECK_PATHS5 or c['test_paths']!=TEST_PATHS or c['names8']!=NAMES8 or c['selected_names15']!=NAMES15
     or json.dumps(c['historical_partial5'],sort_keys=True)!=json.dumps(HISTORICAL_PARTIAL5,sort_keys=True)
     or c['historical_partial26']!=HISTORICAL_PARTIAL26
     or c['resource']!=RESOURCE or len(c['delta10'])!=10 or [r['path'][4:] for r in c['delta10']]!=PATHS10
     or c['expected_full_tests_only']!=1363 or c['whole_codec_completed'] is not False):raise ValueError('exact formatted combined codec scope required')
 for r in c['helpers']+c['controllers']:pinned(repo/safe(r['path']),r)
 old=load(repo/'ci/mobile-input.py','tag903_original_input_source_only');parent,base=old.descriptor()
 if parent['source_digest']!=PARENT_SOURCE['digest'] or parent['portable_digest']!=PARENT_PORTABLE['digest']:raise ValueError('exact363/415 parent required')
 before={r['path']:r for r in parent['source_rows']}
 for r in c['delta10']:
  if r['before']!=before.get(r['path']) or r['after']['path']!=r['path']:raise ValueError('ten original preimages required')
 analysis_reuse4(c,repo)
 return c,old,parent,base
def checkout_sources(c,parent,base,repo=REPO):
 delta={r['path']:r for r in c['delta10']};files={}
 for row in parent['source_rows']:
  target=delta.get(row['path'],{}).get('after',row);files[row['path']]=pinned(repo/safe(row['path']),target)
 actual=authored_paths(repo)
 expected={n for n in files if n.startswith(('app/lib/','app/test/','app/tool/'))}
 if actual!=expected or len(files)!=363 or digest(files)!=c['source']['digest'] or c['source']['members']!=363:raise ValueError('new363 authored inventory differs')
 for row in [*base['sdk_portable']['files'],base['migration']]:
  if row['path'] in files:raise ValueError('portable closure overlap')
  files[row['path']]=pinned(repo/safe(row['path']),row)
 if len(files)!=415 or digest(files)!=c['portable']['digest'] or c['portable']['members']!=415:raise ValueError('new415 closure differs')
 for row in base['native_source_snapshot']['files']:pinned(repo/safe(row['path']),row)
 return files
def parent_git_body(repo,commit,row):
 # Exactly ten public old source blobs. No fetch, floating ref or caller path.
 if commit!=PARENT_COMMIT or row['path'][4:] not in PATHS10:raise ValueError('fixed parent blob only')
 command=['/usr/bin/git','--no-pager','-C',str(repo),'show',commit+':'+row['path']]
 child=subprocess.Popen(command,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL)
 data=bytearray();deadline=time.monotonic()+10;watch=selectors.DefaultSelector()
 try:
  watch.register(child.stdout,selectors.EVENT_READ)
  while watch.get_map():
   remaining=deadline-time.monotonic()
   if remaining<=0:raise ValueError('parent blob read timeout')
   for key,_ in watch.select(min(remaining,.2)):
    block=os.read(key.fileobj.fileno(),min(32768,row['bytes']+1-len(data)))
    if not block:watch.unregister(key.fileobj);break
    data.extend(block)
    if len(data)>row['bytes']:raise ValueError('parent source blob exceeds exact length')
  if child.wait(timeout=max(.1,deadline-time.monotonic()))!=0:raise ValueError('pinned parent source unavailable')
 finally:
  watch.close();child.stdout.close()
  if child.poll() is None:child.kill();child.wait()
 raw=bytes(data)
 if len(raw)!=row['bytes'] or sha(raw)!=row['sha256']:raise ValueError('parent blob identity differs')
 return raw
def prepare_repo(c,parent,base,view,repo=REPO,reader=parent_git_body):
 current=checkout_sources(c,parent,base,repo);before=dict(current)
 for r in c['delta10']:before[r['path']]=reader(repo,c['parent_commit'],r['before'])
 authored={p:b for p,b in before.items() if p.startswith('app/')}
 if len(authored)!=363 or digest(authored)!=PARENT_SOURCE['digest'] or digest(before)!=PARENT_PORTABLE['digest']:raise ValueError('actual old415 reconstruction differs')
 files=dict(before)
 for row in c['helpers']+base['native_source_snapshot']['files']:
  raw=pinned(repo/safe(row['path']),row)
  if row['path'] in files and files[row['path']]!=raw:raise ValueError('prepared source overlap differs')
  files[row['path']]=raw
 if len(files)>600 or sum(map(len,files.values()))>64<<20 or view.exists() or view.resolve()!=view:raise ValueError('fresh finite prepared repo required')
 view.mkdir(mode=0o700)
 for name,raw in files.items():
  p=view/safe(name);p.parent.mkdir(mode=0o700,parents=True,exist_ok=True)
  with p.open('xb') as out:out.write(raw)
 return {'parent_commit':c['parent_commit'],'source':PARENT_SOURCE,'portable':PARENT_PORTABLE,'files':len(files),'body_bytes':sum(map(len,files.values())),'restored_only10':[r['before'] for r in c['delta10']],'original_helpers_unchanged':True},before
def canonical_work(work):
 if work.resolve()!=work or work.is_relative_to(REPO) or not work.is_dir() or work.is_symlink():raise ValueError('canonical owned work outside checkout required')
def verify_work(work,c,parent,base,before=False,view=None):
 canonical_work(work);expected=checkout_sources(c,parent,base)
 if before:
  if view is None:raise ValueError('original prepared source required')
  expected={n:regular(view/safe(n)) for n in expected}
  if digest(expected)!=PARENT_PORTABLE['digest']:raise ValueError('original view source drift')
 for name,body in expected.items():
  if regular(work/'source'/safe(name))!=body:raise ValueError('owned source differs: '+name)
 authored=authored_paths(work/'source')
 if authored!={n for n in expected if n.startswith(('app/lib/','app/test/','app/tool/'))}:raise ValueError('owned authored inventory differs')
 config=work/'source/app/.dart_tool/package_config.json'
 for item in json.loads(regular(config))['packages']:
  u=urlparse(item['rootUri'])
  if u.scheme not in ('','file') or u.netloc:raise ValueError('local package roots required')
  p=Path(unquote(u.path));p=(p if p.is_absolute() else config.parent/p).resolve()
  allowed=[work/'flutter',work/'pub-cache',work/'source/app',work/'source/packages/flutter_openim_sdk']
  if not any(p==root or p.is_relative_to(root) for root in allowed):raise ValueError('package outside fresh roots')
  if item['name']=='imapp' and p!=work/'source/app' or item['name']=='flutter_openim_sdk' and p!=work/'source/packages/flutter_openim_sdk':raise ValueError('actual app/SDK package required')
 version=json.loads(regular(work/'evidence/flutter-version.json'))
 for key,want in [('frameworkVersion',base['toolchain']['tag']),('frameworkRevision',base['toolchain']['framework_revision']),('engineRevision',base['toolchain']['engine_revision']),('dartSdkVersion',base['toolchain']['dart_version'])]:
  if version.get(key)!=want:raise ValueError('original official toolchain differs')
 return expected
def original_setup_receipt(raw,before):
 value=json.loads(raw)
 if (value.get('cohort')!='current363' or value.get('source363')!=PARENT_SOURCE['digest']
     or value.get('portable415')!=PARENT_PORTABLE['digest'] or value.get('rows')!=rows(before)
     or value.get('old_generated_outputs_copied')!=0 or value.get('native_acceptance') is not False):raise ValueError('original setup source.json differs')
 return sha(raw)
def origin_snapshot(work,c,parent,base):
 raw=regular(work/'evidence/source.json');value=json.loads(raw)
 expected=sorted([*parent['source_rows'],*base['sdk_portable']['files'],base['migration']],key=lambda r:r['path'])
 if (value.get('cohort')!='current363' or value.get('source363')!=PARENT_SOURCE['digest'] or value.get('portable415')!=PARENT_PORTABLE['digest']
     or value.get('rows')!=expected or value.get('old_generated_outputs_copied')!=0 or value.get('native_acceptance') is not False):raise ValueError('original415 setup receipt required')
 projection_raw=regular(work/'evidence/tag903-source.json');projection=json.loads(projection_raw)
 if (projection.get('source')!=c['source'] or projection.get('portable')!=c['portable'] or projection.get('parent_commit')!=PARENT_COMMIT
     or projection.get('setup_parent_source')!=PARENT_SOURCE or projection.get('setup_parent_portable')!=PARENT_PORTABLE
     or projection.get('original_setup_source_sha256')!=sha(raw) or projection.get('overlay10')!=c['delta10']
     or projection.get('original_helpers_unchanged') is not True or projection.get('old_vm_result_reused') is not False or projection.get('native_acceptance') is not False):raise ValueError('explicit original-setup-to-ten projection required')
 return {'originalSetupSHA256':sha(raw),'explicitDeltaSHA256':sha(projection_raw)}
def setup(allocation):
 os.umask(0o077);c,old,parent,base=descriptor();check=old.CHECK
 if (allocation.resolve()!=allocation or allocation.exists() or allocation.is_relative_to(REPO)
     or os.statvfs(allocation.parent).f_bavail*os.statvfs(allocation.parent).f_frsize<check.INITIAL):raise ValueError('fresh allocation and4GiB initial required')
 if os.environ.get('HOME')!=check.run_user_home(os.environ.get('USER','')):raise ValueError('real HOME required')
 allocation.mkdir(mode=0o700);view=allocation/'prepared-repo';work=allocation/'work'
 projection,_=prepare_repo(c,parent,base,view)
 # This is the original, pinned, byte-identical setup—not the old VM/test lane.
 child=subprocess.run(['/usr/bin/bash',str(view/'ci/mobile-input-setup.sh'),'current363',str(work)],check=False)
 if child.returncode:raise ValueError('original setup failed; original evidence retained')
 setup_raw=regular(work/'evidence/source.json')
 before=verify_work(work,c,parent,base,before=True,view=view)
 setup_sha=original_setup_receipt(setup_raw,before)
 for r in c['delta10']:
  path=work/'source'/safe(r['path']);pinned(path,r['before'])
  with path.open('wb') as out:out.write(pinned(REPO/safe(r['path']),r['after']));out.flush();os.fsync(out.fileno())
 verify_work(work,c,parent,base)
 write(work/'evidence/tag903-source.json',dict(projection,source=c['source'],portable=c['portable'],setup_parent_source=PARENT_SOURCE,setup_parent_portable=PARENT_PORTABLE,original_setup_source_sha256=setup_sha,overlay10=c['delta10'],native_acceptance=False,old_vm_result_reused=False))
 return {'source':c['source'],'portable':c['portable'],'setup':'original-current363-exact-parent-then-explicit10','native_or_VM_acceptance':False}
def phase_commands(work):
 dart=str(work/'flutter/bin/cache/dart-sdk/bin/dart');flutter=str(work/'flutter/bin/flutter')
 return ([dart,'format','--output=none','--set-exit-if-changed',*CHECK_PATHS5],[[dart,'analyze','--fatal-infos',p] for p in CHECK_PATHS5],[flutter,'test','--no-pub','--machine','--concurrency=1','--no-enable-vmservice','--name','^(?:'+'|'.join(map(re.escape,NAMES15))+')$',*TEST_PATHS])
def tool_snapshot(work):
 out={}
 for name,p in [('dart',work/'flutter/bin/cache/dart-sdk/bin/dart'),('flutter',work/'flutter/bin/flutter')]:
  p=p.resolve();raw=regular(p,128<<20);out[name]={'path':str(p),'bytes':len(raw),'sha256':sha(raw),'mode':stat.S_IMODE(p.stat().st_mode)}
 return out
def source_post(work,c,parent,base,inputs_raw,origin):
 # Same finite10s failure-post allowance as the existing input controller.
 def expired(signum,frame):raise TimeoutError('bounded903 source post')
 previous=signal.signal(signal.SIGALRM,expired);signal.setitimer(signal.ITIMER_REAL,10)
 result={'accepted':False,'source':c['source'],'portable':c['portable'],'inputsSHA256':sha(inputs_raw)}
 try:
  if regular(REPO/INPUTS)!=inputs_raw:raise ValueError('original controller input body changed')
  current,_,newparent,newbase=descriptor()
  if current!=c or newparent!=parent or newbase!=base:raise ValueError('controller/parent changed')
  if origin_snapshot(work,c,parent,base)!=origin:raise ValueError('original setup/projection receipt changed')
  verify_work(work,c,parent,base);result['accepted']=True
 except Exception as error:result['exceptionType']=type(error).__name__
 finally:signal.setitimer(signal.ITIMER_REAL,0);signal.signal(signal.SIGALRM,previous)
 return result
def run(work):
 os.umask(0o077);inputs_raw=regular(REPO/INPUTS);c,old,parent,base=descriptor();check=old.CHECK;verify_work(work,c,parent,base);origin=origin_snapshot(work,c,parent,base);check.prove_namespace()
 guard=check.Guard(work)
 if guard.sample()<check.INITIAL or guard.reason:raise ValueError('initial4GiB/cap gate')
 monitor=threading.Thread(target=guard.poll,daemon=True);monitor.start();primary=None;tools=None;analysis=[];tests=None;reused=None
 try:
  tools=tool_snapshot(work);write(work/'evidence/tag903-tools-before.json',tools)
  write(work/'evidence/tag903-historical-partial5.json',HISTORICAL_PARTIAL5)
  write(work/'evidence/tag903-historical-partial26.json',HISTORICAL_PARTIAL26)
  reused=analysis_reuse4(c);write(work/'evidence/tag903-analysis-reused4.json',reused)
  fmt,analyses,test=phase_commands(work)
  check.guarded(fmt,work,guard,'tag903-format5',timeout=120)
  for index,command in enumerate(analyses,1):
   verify_work(work,c,parent,base);label='tag903-analyze-'+str(index)
   log=check.guarded(command,work,guard,label,timeout=120,accepted_exits=(0,1,2,3))
   record=json.loads(regular(work/'evidence'/(label+'.resource.json')))
   result=old.ANALYSIS.analysis_outcome(record,log.read_text(),label,command);analysis.append(result)
   write(work/'evidence'/(label+'.summary.json'),result);verify_work(work,c,parent,base)
  if not all(r['passed'] for r in analysis):raise ValueError('analysis5 failed; no tests started')
  log=check.guarded(test,work,guard,'tag903-target15',timeout=120)
  tests=check.required_name_summary(log.read_text(),15,[{'name':n,'count':1} for n in NAMES15])
  write(work/'evidence/tag903-target15.summary.json',tests)
 except BaseException as error:primary=error
 finally:
  post={'accepted':False,'source':c['source'],'portable':c['portable']}
  try:post=source_post(work,c,parent,base,inputs_raw,origin)
  except BaseException as error:
   post['exceptionType']=type(error).__name__
   if primary is None:primary=error
  try:
   after=tool_snapshot(work);write(work/'evidence/tag903-tools-after.json',after)
   post['toolsExact']=tools is not None and after==tools
   if not post['toolsExact']:post['accepted']=False
  except Exception as error:post['accepted']=False;post['toolExceptionType']=type(error).__name__
  try:write(work/'evidence/tag903-source-post.json',post)
  except BaseException as error:
   if primary is None:primary=error
  finally:
   try:
    guard.sample();write(work/'evidence/tag903-final-resource.json',{'minimum_free_bytes':guard.minimum,'peak_source_allocated_bytes':guard.peak,'latched_failure':guard.reason})
   except BaseException as error:
    if primary is None:primary=error
   finally:guard.stop.set();monitor.join()
 if primary is not None or guard.reason or not post['accepted'] or tests is None or reused is None:
  write(work/'evidence/tag903-failed.json',{'accepted':False,'exceptionType':type(primary).__name__ if primary is not None else 'ValueError','analysisExecutedFresh':len(analysis),'analysisReusedSameBodyOld':4 if reused is not None else 0,'testsAccepted':tests is not None,'latched_failure':guard.reason,'codec_completed':False})
  if primary is not None:raise primary
  raise ValueError('903 scope/post/resource failed')
 write(work/'evidence/tag903-passed.json',{'accepted':True,'source':c['source'],'portable':c['portable'],'formatPaths':5,'analysisPathsFresh':5,'analysisPathsSameBodyOld':4,'analysisPathsUnion':9,'analysisReuseReceiptSHA256':sha(regular(work/'evidence/tag903-analysis-reused4.json')),'selectedTests':15,'testNames':NAMES15,'newTests':6,'directExisting':9,'historicalPartial5':HISTORICAL_PARTIAL5,'historicalPartial26':HISTORICAL_PARTIAL26,'wholeSelected8Accepted':False,'full1363Executed':False,'nativeAcceptance':False,'wholeCodecCompleted':False,'remaining':['905 actual meeting admission/join/RTC','native and real-service codec acceptance']})
 return 0
def netns(work,run_as):
 if os.geteuid()!=0 or not run_as or run_as=='root':raise ValueError('root namespace launcher and original nonroot tester required')
 c,old,parent,base=descriptor();canonical_work(work);home=old.CHECK.run_user_home(run_as)
 env={'HOME':home,'USER':run_as,'LOGNAME':run_as,'PATH':'/usr/local/bin:/usr/bin:/bin','LANG':'C.UTF-8','LC_ALL':'C.UTF-8','PUB_CACHE':str(work/'pub-cache'),'TMPDIR':str(work/'tmp'),'TMP':str(work/'tmp'),'TEMP':str(work/'tmp'),'XDG_CONFIG_HOME':str(work/'config'),'XDG_CACHE_HOME':str(work/'config/cache'),'FLUTTER_ROOT':str(work/'flutter'),'FLUTTER_SUPPRESS_ANALYTICS':'true','DART_SUPPRESS_ANALYTICS':'true','CI':'true','TERM':'dumb'}
 subprocess.run(['ip','link','set','lo','up'],check=True);old.CHECK.prove_namespace()
 argv=['/usr/sbin/runuser','-u',run_as,'--','env','-i']+[k+'='+v for k,v in env.items()]+['python3','-B',str(Path(__file__).resolve()),'run','--work',str(work)]
 os.execv(argv[0],argv)
def main():
 parser=argparse.ArgumentParser();parser.add_argument('command',choices=['verify','setup','run','netns']);parser.add_argument('--allocation',type=Path);parser.add_argument('--work',type=Path);parser.add_argument('--run-as');a=parser.parse_args()
 if a.command=='setup':print(json.dumps(setup(a.allocation)));return 0
 if a.command=='run':return run(a.work)
 if a.command=='netns':netns(a.work,a.run_as);return 0
 c,_,p,b=descriptor();files=checkout_sources(c,p,b);print(json.dumps({'source':c['source'],'portable':c['portable'],'files':len(files),'GoOrFlutterExecuted':0,'fullCodecCompleted':False}));return 0
if __name__=='__main__':raise SystemExit(main())
