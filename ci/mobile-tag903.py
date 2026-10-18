#!/usr/bin/env python3
"""Manual legacy903 receive/play projection: format1, analysis1 and exact3; historical5 is partial only."""
import argparse, hashlib, importlib.util, json, os, re, selectors, signal, stat
import subprocess, sys, threading, time
from pathlib import Path, PurePosixPath
from urllib.parse import unquote, urlparse

REPO=Path(__file__).resolve().parent.parent
INPUTS='ci/mobile-tag903/inputs.json'
PARENT_COMMIT='2f17d99d2f32c4fc72b9b922f8d2a8783938b665'
PARENT_SOURCE={'members':363,'digest':'ccec31d13c6e64bb4d0c9ff0b1b85baae3d50d198c743d34e9935a053d17ecd8'}
PARENT_PORTABLE={'members':415,'digest':'30879a35431337689eced3a9b5ce9a7c528342740be0be502053100b7be24d7d'}
PATHS6=['lib/features/chat/domain/chat_message.dart','lib/features/chat/data/message_mapper.dart','lib/features/chat/presentation/message_media.dart','lib/features/chat/presentation/chat_page.dart','test/features/chat/legacy_custom_summary_test.dart','test/features/chat/batch_media_access_test.dart']
CHECK_PATHS1=['test/features/chat/batch_media_access_test.dart']
TEST_PATHS=['test/features/chat/batch_media_access_test.dart']
NAMES3=['tag903 audio starts only on tap and keeps the custom message menu', 'tag903 pending audio cannot start after expiry', 'tag903 pending audio cannot start after route-exit']
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
def descriptor(repo=REPO):
 c=json.loads(regular(repo/INPUTS))
 if (c['schema']!=1 or c['state']!='FROZEN_FORMATTED_SOURCE' or c['parent_commit']!=PARENT_COMMIT
     or c['parent_source']!=PARENT_SOURCE or c['parent_portable']!=PARENT_PORTABLE
     or c['analysis_paths']!=CHECK_PATHS1 or c['test_paths']!=TEST_PATHS or c['names8']!=NAMES8 or c['selected_names3']!=NAMES3
     or json.dumps(c['historical_partial5'],sort_keys=True)!=json.dumps(HISTORICAL_PARTIAL5,sort_keys=True)
     or c['resource']!=RESOURCE or len(c['delta6'])!=6 or [r['path'][4:] for r in c['delta6']]!=PATHS6
     or c['expected_full_tests_only']!=1333 or c['whole_codec_completed'] is not False):raise ValueError('exact formatted903 scope required')
 for r in c['helpers']+c['controllers']:pinned(repo/safe(r['path']),r)
 old=load(repo/'ci/mobile-input.py','tag903_original_input_source_only');parent,base=old.descriptor()
 if parent['source_digest']!=PARENT_SOURCE['digest'] or parent['portable_digest']!=PARENT_PORTABLE['digest']:raise ValueError('exact363/415 parent required')
 before={r['path']:r for r in parent['source_rows']}
 for r in c['delta6']:
  if r['before']!=before.get(r['path']) or r['after']['path']!=r['path']:raise ValueError('six original preimages required')
 return c,old,parent,base
def checkout_sources(c,parent,base,repo=REPO):
 delta={r['path']:r for r in c['delta6']};files={}
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
 # Exactly six public old source blobs. No fetch, floating ref or caller path.
 if commit!=PARENT_COMMIT or row['path'][4:] not in PATHS6:raise ValueError('fixed parent blob only')
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
 for r in c['delta6']:before[r['path']]=reader(repo,c['parent_commit'],r['before'])
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
 return {'parent_commit':c['parent_commit'],'source':PARENT_SOURCE,'portable':PARENT_PORTABLE,'files':len(files),'body_bytes':sum(map(len,files.values())),'restored_only6':[r['before'] for r in c['delta6']],'original_helpers_unchanged':True},before
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
     or projection.get('original_setup_source_sha256')!=sha(raw) or projection.get('overlay6')!=c['delta6']
     or projection.get('original_helpers_unchanged') is not True or projection.get('old_vm_result_reused') is not False or projection.get('native_acceptance') is not False):raise ValueError('explicit original-setup-to-six projection required')
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
 for r in c['delta6']:
  path=work/'source'/safe(r['path']);pinned(path,r['before'])
  with path.open('wb') as out:out.write(pinned(REPO/safe(r['path']),r['after']));out.flush();os.fsync(out.fileno())
 verify_work(work,c,parent,base)
 write(work/'evidence/tag903-source.json',dict(projection,source=c['source'],portable=c['portable'],setup_parent_source=PARENT_SOURCE,setup_parent_portable=PARENT_PORTABLE,original_setup_source_sha256=setup_sha,overlay6=c['delta6'],native_acceptance=False,old_vm_result_reused=False))
 return {'source':c['source'],'portable':c['portable'],'setup':'original-current363-exact-parent-then-explicit6','native_or_VM_acceptance':False}
def phase_commands(work):
 dart=str(work/'flutter/bin/cache/dart-sdk/bin/dart');flutter=str(work/'flutter/bin/flutter')
 return ([dart,'format','--output=none','--set-exit-if-changed',*CHECK_PATHS1],[[dart,'analyze','--fatal-infos',p] for p in CHECK_PATHS1],[flutter,'test','--no-pub','--machine','--concurrency=1','--no-enable-vmservice','--name','^(?:'+'|'.join(map(re.escape,NAMES3))+')$',*TEST_PATHS])
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
 monitor=threading.Thread(target=guard.poll,daemon=True);monitor.start();primary=None;tools=None;analysis=[];tests=None
 try:
  tools=tool_snapshot(work);write(work/'evidence/tag903-tools-before.json',tools)
  write(work/'evidence/tag903-historical-partial5.json',HISTORICAL_PARTIAL5)
  fmt,analyses,test=phase_commands(work)
  check.guarded(fmt,work,guard,'tag903-format1',timeout=120)
  for index,command in enumerate(analyses,1):
   verify_work(work,c,parent,base);label='tag903-analyze-'+str(index)
   log=check.guarded(command,work,guard,label,timeout=120,accepted_exits=(0,1,2,3))
   record=json.loads(regular(work/'evidence'/(label+'.resource.json')))
   result=old.ANALYSIS.analysis_outcome(record,log.read_text(),label,command);analysis.append(result)
   write(work/'evidence'/(label+'.summary.json'),result);verify_work(work,c,parent,base)
  if not all(r['passed'] for r in analysis):raise ValueError('analysis1 failed; no tests started')
  log=check.guarded(test,work,guard,'tag903-target3',timeout=120)
  tests=check.required_name_summary(log.read_text(),3,[{'name':n,'count':1} for n in NAMES3])
  write(work/'evidence/tag903-target3.summary.json',tests)
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
 if primary is not None or guard.reason or not post['accepted'] or tests is None:
  write(work/'evidence/tag903-failed.json',{'accepted':False,'exceptionType':type(primary).__name__ if primary is not None else 'ValueError','analysisExecuted':len(analysis),'testsAccepted':tests is not None,'latched_failure':guard.reason,'codec_completed':False})
  if primary is not None:raise primary
  raise ValueError('903 scope/post/resource failed')
 write(work/'evidence/tag903-passed.json',{'accepted':True,'source':c['source'],'portable':c['portable'],'formatPaths':1,'analysisPaths':1,'selectedTests':3,'testNames':NAMES3,'newTests':0,'directExisting':3,'historicalPartial5':HISTORICAL_PARTIAL5,'wholeSelected8Accepted':False,'full1333Executed':False,'nativeAcceptance':False,'wholeCodecCompleted':False,'remaining':['905','910','911']})
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
