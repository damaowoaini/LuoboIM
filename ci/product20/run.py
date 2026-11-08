#!/usr/bin/env python3
"""Run the selected backend or browser acceptance against owned Linux services."""
import argparse,hashlib,importlib.util,json,os,pwd,re,secrets,shutil,stat,subprocess,sys,time,urllib.request,urllib.parse,zipfile
from pathlib import Path
REPO=Path(__file__).resolve().parents[2]
HERE=Path(__file__).resolve().parent
NAMES=('admin-rpc','chat-rpc','office-rpc','admin-api','chat-api','route-service')
ASSEMBLY_MODE='core14-currentChart5-historicalRouteFrom6-v1'
CHART5={'chat-api':'./cmd/api/chat-api','admin-api':'./cmd/api/admin-api','chat-rpc':'./cmd/rpc/chat-rpc','admin-rpc':'./cmd/rpc/admin-rpc','office-rpc':'./cmd/rpc/office-rpc'}
OVERLAYS=('server/internal/runtimeacceptance/product20_linux.go','server/cmd/tools/product20-acceptance/main.go')
BOOT='server/internal/runtimeacceptance/bootstrap_linux.go'
ANCHOR='"secret": c["serviceSecret"], "multiLoginPolicy": 1'
REPLACEMENT='"secret": c["serviceSecret"], "adminDelegation": map[string]any{"issuer": b.Header.Tenant}, "mysql": fixtureSQL, "messageVerify": map[string]bool{"friendVerify": false}, "multiLoginPolicy": 1'
DISCOVERY_ANCHOR=b'"zookeeper": map[string]any{"schema": "fullgraph_" + b.Resource.Nonce,'
DISCOVERY_REPLACEMENT=b'"zookeeper": map[string]any{"schema": b.Header.Tenant,'
CORE_BUILD_MODE='fresh-current-core14-before-P20-v1'
CORE_GATEWAY_BUILD_MODE='fresh-current-core14-managed-gateway-context-v1'
MONGO_SOURCE='server/pkg/common/db/unrelation/mongo.go'
MONGO_TEST='server/pkg/common/db/unrelation/mongo_error_test.go'
MONGO_TEST_NAME='TestMongoNoDocumentsMapsToOpenIMRecordNotFound'
GATEWAY_SOURCE='server/internal/msgtransfer/managed_ordinary_start.go'
GATEWAY_TEST='server/internal/msgtransfer/managed_ordinary_gateway_operation_test.go'
GATEWAY_TEST_NAME='TestManagedOrdinaryGatewayAuthorizationProvidesRPCOperationID'
GATEWAY_TEST_PHASE='current-core-managed-gateway-context-test'
CORE_MONGO_TEST_REUSE = {'goTool': {'bytes': 17164055,
            'mode': 493,
            'sha256': '548d3c32f83fa81152f543eb1526be7884cfe0526eb5e9ce29d3eca14786e7e0',
            'version': 'go version go1.27.2 linux/amd64'},
 'locks': {'go.mod': 'e12331dfa5ce70b14a99f2c4408347ca11747ebe23e19d0d9b023f634eeab029',
           'go.sum': '97c43c12be1edc668225b2ef21c23e95c242327c310d0f05ffe84bc2d05c71c8'},
 'postReceiptSHA256': '4a4ac5cbcb5756e85c6c2189a6f246602d94e8fd0d87a4992fd19a04096c7b67',
 'priorReceipt': {'eventsSHA256': '7c7f2652dc517d038be114bc37ec92f198b1ce9317051aff048555af660ba880',
                  'fail': 0,
                  'name': 'TestMongoNoDocumentsMapsToOpenIMRecordNotFound',
                  'package': 'github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation',
                  'skip': 0,
                  'subPASS': 0,
                  'topPASS': 1},
 'producer': {'attempt': 1,
              'head': '35c3e12dc8be834f07e708e2d6ae3eded4782706',
              'job': 128297421883,
              'run': 42337430486},
 'rootReviewSHA256': 'ec93a05d8abdafa94eb27e6dc19fcc94bd7713eee6e6898f7c2cf30fc369571d',
 'source': {'digest': 'af917bed287806f1f44254f3be1f830456afec40fedfc12f14765898aee6220a',
            'members': 1585},
 'testReceiptSHA256': 'e82b10f270e9c590925a964735bee5f052087bca51bea8252ba30d24822d751a',
 'version': 'same-canonical-core-mongo-test-v1'}
def sha(b):return hashlib.sha256(b).hexdigest()
def regular(path,maximum=32<<20):
 p=Path(path)
 if p.resolve()!=p:raise ValueError('canonical source required')
 fd=os.open(p,os.O_RDONLY|os.O_NOFOLLOW)
 try:
  a=os.fstat(fd)
  if not stat.S_ISREG(a.st_mode) or a.st_nlink!=1 or a.st_size>maximum:raise ValueError('bounded regular input required')
  chunks=[];left=maximum+1
  while left:
   part=os.read(fd,min(left,1<<20))
   if not part:break
   chunks.append(part);left-=len(part)
  b=os.fstat(fd);c=p.lstat();key=lambda x:(x.st_dev,x.st_ino,x.st_mode,x.st_size,x.st_mtime_ns,x.st_ctime_ns)
  if key(a)!=key(b) or key(a)!=key(c):raise ValueError('source changed')
  raw=b''.join(chunks)
  if len(raw)!=a.st_size:raise ValueError('short read')
  return raw
 finally:os.close(fd)
def write(p,value):
 raw=(json.dumps(value,separators=(',',':'),ensure_ascii=False)+'\n').encode()
 fd=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
 with os.fdopen(fd,'wb') as f:f.write(raw);f.flush();os.fsync(f.fileno())
 return sha(raw)
def load(path,label):
 spec=importlib.util.spec_from_file_location(label,REPO/path);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
def vector(bodies):
 rows=[{'path':n,'bytes':len(b),'sha256':sha(b)} for n,b in sorted(bodies.items())]
 return {'members':len(rows),'digest':sha(json.dumps(rows,sort_keys=True,separators=(',',':')).encode())}
def derive_bootstrap(body):
 config_anchor=b'\tvalue := map[string]any{"envs"'
 config_prefix=b'\tfixtureSQL, e := product20FixtureSQL(b)\n\tif e != nil {\n\t\treturn "", e\n\t}\n'
 if any(body.count(anchor)!=1 for anchor in (ANCHOR.encode(),config_anchor,DISCOVERY_ANCHOR)):raise ValueError('single fixture constructor anchors required')
 return body.replace(config_anchor,config_prefix+config_anchor,1).replace(ANCHOR.encode(),REPLACEMENT.encode(),1).replace(DISCOVERY_ANCHOR,DISCOVERY_REPLACEMENT,1)
def core_source(c):
 return c['currentCoreBuild']['source'] if 'currentCoreBuild' in c else c['graphProvider']['source']
def core_vector(bodies):
 return {'members':len(bodies),'digest':sha(b''.join(path.encode()+b'\0'+sha(body).encode()+b'\n' for path,body in sorted(bodies.items())))}
def select_core_source(c,bodies):
 if 'currentCoreBuild' not in c:return dict(bodies)
 plan=c['currentCoreBuild']
 if (type(plan) is not dict or set(plan)!={'mode','baseSource','source','changes'}
     or plan['mode'] not in (CORE_BUILD_MODE,CORE_GATEWAY_BUILD_MODE) or plan['baseSource']!=c['graphProvider']['source']
     or core_vector(bodies)!=plan['baseSource']):raise ValueError('explicit current core build baseline required')
 gateway=plan['mode']==CORE_GATEWAY_BUILD_MODE
 expected={MONGO_SOURCE,MONGO_TEST}
 if gateway:
  if 'coreMongoTestReuse' in c:raise ValueError('gateway build cannot claim Mongo test reuse')
  expected.update((GATEWAY_SOURCE,GATEWAY_TEST))
  if MONGO_SOURCE not in bodies or GATEWAY_SOURCE not in bodies or MONGO_TEST in bodies or GATEWAY_TEST in bodies:
   raise ValueError('gateway baseline requires two original sources and two new regression files')
 rows=plan['changes']
 if (type(rows) is not list or len(rows)!=len(expected)
     or any(type(r) is not dict or type(r.get('target')) is not str for r in rows)
     or {r['target'] for r in rows}!=expected):raise ValueError('exact current core source delta required')
 selected=dict(bodies)
 for row in rows:
  target=row['target'];old=bodies.get(target)
  if set(row)!={'target','path','beforeSHA256','bytes','sha256'} or row['path']!='product/'+target or row['beforeSHA256']!=(sha(old) if old is not None else None) or type(row['bytes']) is not int or row['bytes']<=0 or type(row['sha256']) is not str or not re.fullmatch('[0-9a-f]{64}',row['sha256']):raise ValueError('current core source preimage differs')
  body=regular(REPO/row['path'])
  if len(body)!=row['bytes'] or sha(body)!=row['sha256'] or gateway and body==old:raise ValueError('current core source pin differs')
  selected[target]=body
 if core_vector(selected)!=plan['source']:raise ValueError('current core source vector differs')
 return selected
def check_named_core_test_events(raw,package,name,failure,subtests=()):
 events=[json.loads(line) for line in raw.splitlines() if line.strip()]
 expected={name,*(name+'/'+part for part in subtests)}
 relevant=[e for e in events if e.get('Action') in ('run','pass','fail','skip')]
 if (any(e.get('Package')!=package or e.get('Action') in ('fail','skip') or e.get('Test') not in expected|{None} for e in relevant)
     or any(sum(e.get('Action')==action and e.get('Test')==test for e in relevant)!=1 for action in ('run','pass') for test in expected)
     or sum(e.get('Action')=='pass' and 'Test' not in e for e in relevant)!=1):raise ValueError(failure)
 return {'package':package,'name':name,'topPASS':1,'subPASS':len(subtests),'skip':0,'fail':0,'eventsSHA256':sha(raw)}
def check_current_core_test_events(raw):
 return check_named_core_test_events(raw,'github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation',MONGO_TEST_NAME,'one actual Mongo normalization test and package completion required')
def check_gateway_core_test_events(raw):
 return check_named_core_test_events(raw,'github.com/openimsdk/open-im-server/v3/internal/msgtransfer',GATEWAY_TEST_NAME,'one actual managed gateway context test and package completion required',('generates-operation-for-background-worker','preserves-existing-operation'))
def verify_core_mongo_test_reuse(c,canonical):
 if 'coreMongoTestReuse' not in c:return None
 policy=c['coreMongoTestReuse']
 encode=lambda value:json.dumps(value,sort_keys=True,separators=(',',':'))
 if type(policy) is not dict or encode(policy)!=encode(CORE_MONGO_TEST_REUSE):
  raise ValueError('exact prior Mongo test policy required')
 if 'currentCoreBuild' not in c or core_source(c)!=policy['source'] or core_vector(canonical)!=policy['source']:
  raise ValueError('Mongo reuse requires the same complete canonical source')
 if any('server/'+name not in canonical or sha(canonical['server/'+name])!=expected for name,expected in policy['locks'].items()):
  raise ValueError('Mongo reuse requires the original module locks')
 return policy

def core_mongo_test_reuse_receipt(c,canonical,actual_go):
 policy=verify_core_mongo_test_reuse(c,canonical)
 if policy is None or actual_go!=policy['goTool']:
  raise ValueError('actual Go tool must match the original Mongo test')
 return {'version':'product20-core-mongo-test-reuse-v1','source':policy['source'],
         'sourceAndLocksExact':True,'actualGoToolExact':True,'currentFreshTestCommands':0,
         'priorReceipt':{'producer':policy['producer'],'test':policy['priorReceipt'],
           'testReceiptSHA256':policy['testReceiptSHA256'],'postReceiptSHA256':policy['postReceiptSHA256'],
           'rootReviewSHA256':policy['rootReviewSHA256']}}

def verify():
 c=json.loads(regular(HERE/'inputs.json'))
 if c['schema']!=1 or c['scope']!='one-fresh-product20-backend-or-web' or c['productRebuilds']!=(14 if 'currentCoreBuild' in c else 0) or c['oldTestsToRerun']!=[]:raise ValueError('exact chain scope required')
 for path,expected in c['helpers'].items():
  if sha(regular(REPO/path))!=expected:raise ValueError('source helper drift')
 builder=load(c['fixtureSourceBuilder'],'p20_original_fixture_policy')
 if c.get('fixtureSourceBuildRequiredWhenNoVerifiedProvider') is not True:raise ValueError('explicit null-provider fixture source-build policy required')
 builder.fixture_policy({'minioFixturePolicy':builder.FIXTURE_POLICY,'minioFixtureBinding':c['fixtureMinIOProvider']})
 graph=load(c['graphProvider']['path'],'p20_graph_source');g,bodies,*_=graph.verify()
 if g['source']!=c['graphProvider']['source']:raise ValueError('explicit graph source differs')
 bodies=select_core_source(c,bodies)
 verify_core_mongo_test_reuse(c,bodies)
 chart=load('ci/chart-route-build/run.py','p20_chart_source');cc,chart_bodies=chart.verify()
 if cc['sources']!=c['chartProvider']['sources'] or cc['locks']!=c['chartProvider']['locks']:raise ValueError('accepted chart6 source/locks differ')
 if 'historicalPreviousChart5' in c:
  t=c.get('typedStartupDiagnostic')
  if type(t) is not dict or set(t)!={'path','sha256','version','prefix'} or t['path']!='chart/pkg/common/chatrpcstart/start.go' or t['version']!='chart-startup-error-v1' or t['prefix']!='LUOBO_CHART_STARTUP_V1 ' or sha(chart_bodies.get(t['path'],b''))!=t['sha256']:raise ValueError('current typed startup source binding differs')
 original=dict(bodies);body=bodies[BOOT]
 if sha(body)!=c['fixtureConfiguration']['beforeSHA256']:raise ValueError('original fixture bootstrap differs')
 bodies=dict(bodies);bodies[BOOT]=derive_bootstrap(body)
 if sha(bodies[BOOT])!=c['fixtureConfiguration']['afterSHA256']:raise ValueError('derived fixture bootstrap differs')
 for target in OVERLAYS:
  raw=regular(HERE/'source'/target)
  if target in bodies or sha(raw)!=c['overlay'][target]:raise ValueError('new tool overlay identity differs')
  bodies[target]=raw
 if vector(bodies)!=c['derivedHarnessSource']:raise ValueError('explicit auxiliary-only derived source differs')
 chart_assembly_policy(c)
 return c,graph,g,original,bodies,chart_bodies

def artifact_binding(a):
 if not isinstance(a,dict) or set(a)!={'run','head','attempt','artifactID','artifactBytes','artifactSHA256','manifestSHA256'} or any(type(a[k]) is not int or a[k]<=0 for k in ('run','artifactID','artifactBytes')) or any(not isinstance(a[n],str) or not re.fullmatch('[0-9a-f]{64}',a[n]) for n in ('artifactSHA256','manifestSHA256')) or not isinstance(a['head'],str) or not re.fullmatch('[0-9a-f]{40}',a['head']) or type(a['attempt']) is not int or a['attempt']!=1 or a['artifactBytes']>2<<30:raise ValueError('exact successful artifact binding required')
 return a

def legacy_chart_assembly_policy(c):
 p=c.get('chartAssembly')
 if not isinstance(p,dict) or set(p)!={'mode','currentChart5','historicalRouteFrom6'} or p['mode']!=ASSEMBLY_MODE:raise ValueError('explicit split producer assembly required')
 five,old=p['currentChart5'],p['historicalRouteFrom6']
 if set(five)!={'artifact','expectedManifest','derivation'} or set(old)!={'artifact','provider','selectedRoute','originalManifestBytes','originalManifestSHA256'}:raise ValueError('exact assembly policy fields required')
 a=artifact_binding(five['artifact']);original=artifact_binding(old['artifact']);expect=five['expectedManifest'];m=expect['body']
 raw=(json.dumps(m,sort_keys=True,indent=2)+'\n').encode()
 if set(expect)!={'evidence','bytes','sha256','body'} or expect['evidence']!='expected-derived-not-observed' or type(expect['bytes']) is not int or not 0<expect['bytes']<=1<<20 or len(raw)!=expect['bytes'] or sha(raw)!=expect['sha256'] or expect['sha256']!=a['manifestSHA256']:raise ValueError('explicit expected not observed five manifest required')
 keys={'schema','kind','head','run','attempt','source','locks','binaries','buildResources','freshChartBuilds','freshRouteBuilds','sourceToolHelperPostExact','GoVersion','selectedLive4Accepted','SQLRedisOriginalOwnerCleanupAccepted','parserOracleExecuted','ReadyOrP20OrDeploymentAccepted','outerResourceGuardStillRequired'}
 if set(m)!=keys or type(m['schema']) is not int or m['schema']!=1 or m['kind']!='current259-five-chart-build-only-v1' or m['head']!=a['head'] or m['run']!=str(a['run']) or m['attempt']!=str(a['attempt']) or m['source']!=c['chartProvider']['sources']['chart'] or m['locks']!=c['chartProvider']['locks']['chart'] or m['GoVersion']!='go1.27.2 linux/amd64' or type(m['freshChartBuilds']) is not int or m['freshChartBuilds']!=5 or type(m['freshRouteBuilds']) is not int or m['freshRouteBuilds']!=0:raise ValueError('expected five producer source or scope differs')
 if any(m[k] is not True for k in ('sourceToolHelperPostExact','selectedLive4Accepted','SQLRedisOriginalOwnerCleanupAccepted','outerResourceGuardStillRequired')) or any(m[k] is not False for k in ('parserOracleExecuted','ReadyOrP20OrDeploymentAccepted')):raise ValueError('expected five admission boundary differs')
 rows=m['binaries']
 if not isinstance(rows,list) or len(rows)!=5 or {r['name'] for r in rows}!=set(CHART5) or sum(r['bytes'] for r in rows)>512<<20:raise ValueError('exact five current rows required')
 for r in rows:
  if set(r)!={'module','package','name','sourceVector','bytes','sha256','mode','GOOS','GOARCH','CGO_ENABLED'} or r['module']!='chart' or r['package']!=CHART5[r['name']] or r['sourceVector']!=m['source'] or type(r['bytes']) is not int or not 0<r['bytes']<=128<<20 or not re.fullmatch('[0-9a-f]{64}',r['sha256']) or r['mode']!=0o700 or (r['GOOS'],r['GOARCH'],r['CGO_ENABLED'])!=('linux','amd64','0'):raise ValueError('exact five platform or body row differs')
 if set(m['buildResources'])!=set(CHART5):raise ValueError('five actual build resources required')
 for r in m['buildResources'].values():
  if set(r)!={'exit','status','resourceSHA256'} or type(r['exit']) is not int or r['exit']!=0 or r['status']!='passed' or not re.fullmatch('[0-9a-f]{64}',r['resourceSHA256']):raise ValueError('five build resource boundary differs')
 if set(five['derivation'])!={'producerControllerSHA256','producerInputsSHA256','buildsReceiptSHA256','rootExpectedOnlyReviewSHA256'} or any(not isinstance(v,str) or not re.fullmatch('[0-9a-f]{64}',v) for v in five['derivation'].values()):raise ValueError('finite expected manifest derivation pins required')
 historical=c['historicalAcceptedChartArtifact']
 if original!=historical['artifact'] or old['provider']!=historical['provider'] or old['originalManifestSHA256']!=original['manifestSHA256'] or type(old['originalManifestBytes']) is not int or not 0<old['originalManifestBytes']<=1<<20 or old['provider']['sources']['route']!=c['chartProvider']['sources']['route'] or old['provider']['locks']['route']!=c['chartProvider']['locks']['route']:raise ValueError('original route source and locks must remain historical')
 r=old['selectedRoute']
 if set(r)!={'module','package','name','sourceVector','bytes','sha256','mode','GOOS','GOARCH','CGO_ENABLED'} or (r['module'],r['package'],r['name'])!=('route','.','route-service') or r['sourceVector']!=c['chartProvider']['sources']['route'] or type(r['bytes']) is not int or not 0<r['bytes']<=128<<20 or not re.fullmatch('[0-9a-f]{64}',r['sha256']) or r['mode']!=0o700 or (r['GOOS'],r['GOARCH'],r['CGO_ENABLED'])!=('linux','amd64','0'):raise ValueError('original route row differs')
 return p

# Current-only schema successor; legacy artifact receiver and ELF loop stay intact.
STARTUP_FIVE_KIND='current-chart-five-startup1-prior-admin4-compatible-v2'
STARTUP_COHORT='chart-startup1-prior-admin4-chart5-v2'
STARTUP_PACKAGE='github.com/OpenIMSDK/chat/pkg/common/chatrpcstart'
STARTUP_TEST='TestStartupErrorPreservesCauseAndClosedDiagnostic'
PRIOR_ADMIN_REVIEW='e22941a1365bd9f89bc371a2fc58bb4c7a219db17533fb22871fcab0e73f232e'
PRIOR_ADMIN_RECEIPTS={'events.json':'98cf718a3d7ed9a9f29e6e4533f656a1e0401ec64c75eb7fb80e9e375da68d4d','summary.json':'1eb4721422f4101659f98a91c0852dff495bf4bab9bae25b15c0fa9037c5c9d6','source-tools.json':'e7c7f980256fe07b93e954f2700d25a71d7aa0368018feba4649099a3cf6cf1c','cleanup.json':'3364a137647f1e40618938a9ca29f7b61937aa045da1e15b1fc62d5498fe57fd','fixture-after.json':'f6bb7138fd7d0ad1d4f94b59ac20b22d289c953fc3d06b90175b49eef33c7373','cleanup-final.json':'3364a137647f1e40618938a9ca29f7b61937aa045da1e15b1fc62d5498fe57fd','builds.json':'f39f43f8dc8e22e1472bd35df78550e9583554bd51a1efc340e00fa4622e0355'}

def historical_chart_control(c):
 h=c.get('historicalPreviousChart5')
 if type(h) is not dict or set(h)!={'chartProvider','currentChart5'}:raise ValueError('explicit original chart259 history required')
 legacy=dict(c);legacy['chartProvider']=h['chartProvider'];legacy['chartAssembly']={'mode':ASSEMBLY_MODE,'currentChart5':h['currentChart5'],'historicalRouteFrom6':c['chartAssembly']['historicalRouteFrom6']}
 if legacy['chartProvider']['sources']['chart']!={'members':259,'sha256':'432310bf9869e1960964c84c442e45ffbd85a7e4634b3052bb4e8dd2092ac7cc'} or legacy['chartProvider']['sources']['route']!=c['chartProvider']['sources']['route'] or legacy['chartProvider']['locks']!=c['chartProvider']['locks']:raise ValueError('historical provider must not be relabeled current')
 legacy_chart_assembly_policy(legacy)
 return legacy

def startup_five_manifest(c,five,a,m):
 keys={'schema','kind','head','run','attempt','source','locks','binaries','buildResources','freshChartBuilds','freshRouteBuilds','sourceToolHelperPostExact','GoVersion','startupTest','priorAdmin4','currentAdmin4TestCommands','SQLRedisFixturesStarted','parserOracleExecuted','ReadyOrP20OrDeploymentAccepted','outerResourceGuardStillRequired'}
 if set(m)!=keys or type(m['schema']) is not int or m['schema']!=2 or m['kind']!=STARTUP_FIVE_KIND or m['head']!=a['head'] or m['run']!=str(a['run']) or m['attempt']!=str(a['attempt']) or m['source']!=c['chartProvider']['sources']['chart'] or m['locks']!=c['chartProvider']['locks']['chart'] or m['GoVersion']!='go1.27.2 linux/amd64':raise ValueError('new startup-five identity/source differs')
 if any(type(m[k]) is not int or m[k]!=n for k,n in [('freshChartBuilds',5),('freshRouteBuilds',0),('currentAdmin4TestCommands',0),('SQLRedisFixturesStarted',0)]) or any(m[k] is not True for k in ('sourceToolHelperPostExact','outerResourceGuardStillRequired')) or any(m[k] is not False for k in ('parserOracleExecuted','ReadyOrP20OrDeploymentAccepted')):raise ValueError('new startup-five scope differs')
 t=m['startupTest'];tk={'actualTopRun','actualTopPASS','package','subtests','skips','packagePASS','actualFooter','actualExitCode','originalMachineJSON_SHA256','originalMachineJSON_Bytes','rawOutputExported','actualResourceGuardPassed','resourceSHA256'}
 if type(t) is not dict or set(t)!=tk or t['actualTopRun']!=[STARTUP_TEST] or t['actualTopPASS']!=[STARTUP_TEST] or t['package']!=STARTUP_PACKAGE or any(type(t[k]) is not int or t[k]!=0 for k in ('subtests','skips','actualExitCode')) or any(t[k] is not True for k in ('packagePASS','actualFooter','actualResourceGuardPassed')) or t['rawOutputExported'] is not False or type(t['originalMachineJSON_Bytes']) is not int or not 0<t['originalMachineJSON_Bytes']<=16<<20 or any(not re.fullmatch('[0-9a-f]{64}',t[k]) for k in ('originalMachineJSON_SHA256','resourceSHA256')):raise ValueError('actual startup1 receipt required')
 p=m['priorAdmin4'];pk={'kind','originalHead','originalRun','originalJob','originalChartSource','originalSourceDigest','originalRootReviewSHA256','originalReceiptPins','sourceBridgeSHA256','closureReceiptSHA256','compatibilityAccepted','currentAdmin4TestCommands','currentFreshAdmin4PASS'}
 if type(p) is not dict or set(p)!=pk or p['kind']!='historical-selective4-source-compatible-not-fresh' or p['originalHead']!='98d6a73a3b6342bbfaf525f478bc7ccf701ceb77' or p['originalRun']!=42283432687 or p['originalJob']!=128118049210 or p['originalRootReviewSHA256']!=PRIOR_ADMIN_REVIEW or p['originalChartSource']!={'members':259,'sha256':'432310bf9869e1960964c84c442e45ffbd85a7e4634b3052bb4e8dd2092ac7cc'} or p['originalSourceDigest']!='40f8e478567a98cb080f107ca5eb80e05580db6b531c9108780f9815ec027647' or p['compatibilityAccepted'] is not True or p['currentFreshAdmin4PASS'] is not False or type(p['currentAdmin4TestCommands']) is not int or p['currentAdmin4TestCommands']!=0:raise ValueError('historical four cannot become fresh/current PASS')
 if p['originalReceiptPins']!=PRIOR_ADMIN_RECEIPTS or any(not re.fullmatch('[0-9a-f]{64}',p[k]) for k in ('sourceBridgeSHA256','closureReceiptSHA256')):raise ValueError('exact historical/actual compatibility pins required')

def chart_assembly_policy(c):
 p=c.get('chartAssembly')
 if type(p) is not dict:raise ValueError('explicit chart assembly required')
 if 'pendingCurrent5' in p:
  if set(p)!={'mode','pendingCurrent5','currentChart5','historicalRouteFrom6'} or p['mode']!=ASSEMBLY_MODE or p['currentChart5']!={'artifact':None,'expectedManifest':None,'derivation':None} or c.get('executionBinding') is not None or c.get('acceptedChartArtifact') is not None:raise ValueError('pending current5 must have no execution or artifact identity')
  wait=p['pendingCurrent5']
  if type(wait) is not dict or set(wait)!={'state','source','locks','entries','producerCohort','kind','retainedCore14'} or wait['state']!='pending-current5' or wait['source']!=c['chartProvider']['sources']['chart'] or wait['locks']!=c['chartProvider']['locks']['chart'] or wait['entries']!=[['chart',package,name] for name,package in CHART5.items()] or wait['producerCohort']!=STARTUP_COHORT or wait['kind']!=STARTUP_FIVE_KIND:raise ValueError('pending source/build recipe must be explicit and current')
  artifact_binding(wait['retainedCore14']);historical_chart_control(c)
  return p
 five=p.get('currentChart5',{});expect=five.get('expectedManifest',{})
 if expect.get('body',{}).get('kind')!=STARTUP_FIVE_KIND:return legacy_chart_assembly_policy(c)
 if set(p)!={'mode','currentChart5','historicalRouteFrom6'} or p['mode']!=ASSEMBLY_MODE or set(five)!={'artifact','expectedManifest','derivation'}:raise ValueError('exact new split chart assembly required')
 a=artifact_binding(five['artifact']);raw=(json.dumps(expect['body'],sort_keys=True,indent=2)+'\n').encode()
 if set(expect)!={'evidence','bytes','sha256','body'} or expect['evidence']!='expected-derived-not-observed' or type(expect['bytes']) is not int or not 0<expect['bytes']<=1<<20 or len(raw)!=expect['bytes'] or sha(raw)!=expect['sha256'] or expect['sha256']!=a['manifestSHA256']:raise ValueError('explicit expected new producer body required')
 startup_five_manifest(c,five,a,expect['body']);m=expect['body'];old=p['historicalRouteFrom6']
 if set(old)!={'artifact','provider','selectedRoute','originalManifestBytes','originalManifestSHA256'}:raise ValueError('original route policy fields differ')
 original=artifact_binding(old['artifact'])
 # The original common five-row/resource/derivation/historical-route policy is
 # inserted here verbatim by the authoring splice, without legacy identities.
 rows=m['binaries']
 if not isinstance(rows,list) or len(rows)!=5 or {r['name'] for r in rows}!=set(CHART5) or sum(r['bytes'] for r in rows)>512<<20:raise ValueError('exact five current rows required')
 for r in rows:
  if set(r)!={'module','package','name','sourceVector','bytes','sha256','mode','GOOS','GOARCH','CGO_ENABLED'} or r['module']!='chart' or r['package']!=CHART5[r['name']] or r['sourceVector']!=m['source'] or type(r['bytes']) is not int or not 0<r['bytes']<=128<<20 or not re.fullmatch('[0-9a-f]{64}',r['sha256']) or r['mode']!=0o700 or (r['GOOS'],r['GOARCH'],r['CGO_ENABLED'])!=('linux','amd64','0'):raise ValueError('exact five platform or body row differs')
 if set(m['buildResources'])!=set(CHART5):raise ValueError('five actual build resources required')
 for r in m['buildResources'].values():
  if set(r)!={'exit','status','resourceSHA256'} or type(r['exit']) is not int or r['exit']!=0 or r['status']!='passed' or not re.fullmatch('[0-9a-f]{64}',r['resourceSHA256']):raise ValueError('five build resource boundary differs')
 if set(five['derivation'])!={'producerControllerSHA256','producerInputsSHA256','buildsReceiptSHA256','rootExpectedOnlyReviewSHA256'} or any(not isinstance(v,str) or not re.fullmatch('[0-9a-f]{64}',v) for v in five['derivation'].values()):raise ValueError('finite expected manifest derivation pins required')
 historical=c['historicalAcceptedChartArtifact']
 if original!=historical['artifact'] or old['provider']!=historical['provider'] or old['originalManifestSHA256']!=original['manifestSHA256'] or type(old['originalManifestBytes']) is not int or not 0<old['originalManifestBytes']<=1<<20 or old['provider']['sources']['route']!=c['chartProvider']['sources']['route'] or old['provider']['locks']['route']!=c['chartProvider']['locks']['route']:raise ValueError('original route source and locks must remain historical')
 r=old['selectedRoute']
 if set(r)!={'module','package','name','sourceVector','bytes','sha256','mode','GOOS','GOARCH','CGO_ENABLED'} or (r['module'],r['package'],r['name'])!=('route','.','route-service') or r['sourceVector']!=c['chartProvider']['sources']['route'] or type(r['bytes']) is not int or not 0<r['bytes']<=128<<20 or not re.fullmatch('[0-9a-f]{64}',r['sha256']) or r['mode']!=0o700 or (r['GOOS'],r['GOARCH'],r['CGO_ENABLED'])!=('linux','amd64','0'):raise ValueError('original route row differs')
 return p

def project_chart_startup_lines(streams,chart_bodies,policy):
 if policy is None:return None
 if type(streams) is not dict or set(streams)!={'stdout','stderr'} or any(type(v) is not bytes or len(v)>P20_CHILD_EXIT_POLICY['perStreamBytes'] for v in streams.values()) or sum(map(len,streams.values()))>P20_CHILD_EXIT_POLICY['totalBytes']:raise ValueError('original bounded child streams required')
 lines=[]
 for stream in ('stderr','stdout'):
  for line in streams[stream].splitlines():
   if len(lines)==P20_CHILD_EXIT_POLICY['lineLimit']:break
   lines.append(line)
  if len(lines)==P20_CHILD_EXIT_POLICY['lineLimit']:break
 if not any(line.startswith(b'panic: LUOBO_CHART_STARTUP_V1 ') for line in lines):return None
 if type(policy) is not dict or set(policy)!={'path','sha256','version','prefix'} or policy['path']!='chart/pkg/common/chatrpcstart/start.go' or policy['version']!='chart-startup-error-v1' or policy['prefix']!='LUOBO_CHART_STARTUP_V1 ' or sha(chart_bodies.get(policy['path'],b''))!=policy['sha256']:raise ValueError('source-bound typed startup emitter required')
 sdk={500,1000,1001,1002,1003,1004,1101,90002,90004,90007};redis={'none','unknown','NOAUTH','WRONGPASS','NOPERM','NOSCRIPT','WRONGTYPE','LOADING','READONLY','OOM','EXECABORT','MISCONF','CLUSTERDOWN','BUSY','ERR'}
 def unique(pairs):
  d={}
  for key,value in pairs:
   if key in d:raise ValueError('duplicate typed startup field')
   d[key]=value
  return d
 found=[];prefix=('panic: '+policy['prefix']).encode()
 for line in lines:
  if not line.startswith(prefix):continue
  if len(line)>P20_CHILD_EXIT_POLICY['lineBytesLimit']:raise ValueError('typed startup marker is oversized')
  value=json.loads(line[len(prefix):].decode('utf8'),object_pairs_hook=unique)
  if type(value) is not dict or set(value)!={'version','stage','causes','truncated'} or value['version']!=policy['version'] or value['stage'] not in {'discovery','register-ip','rpc-init','rpc-register','listen','serve','unknown'} or type(value['truncated']) is not bool or type(value['causes']) is not list or not 1<=len(value['causes'])<=8:raise ValueError('closed typed startup body required')
  for row in value['causes']:
   if type(row) is not dict or set(row)!={'kind','mysqlNumber','errno','grpcCode','sdkCode','redisPrefix'} or row['kind'] not in {'context-canceled','context-deadline','mysql','errno','grpc','sdk','redis','unknown'} or any(type(row[k]) is not int for k in ('mysqlNumber','errno','grpcCode','sdkCode')):raise ValueError('typed startup cause shape differs')
   kind=row['kind']
   if (not 0<=row['mysqlNumber']<=65535 or kind!='mysql' and row['mysqlNumber']!=0 or not (1<=row['errno']<=4095 if kind=='errno' else row['errno']==0) or not (0<=row['grpcCode']<=16 if kind=='grpc' else row['grpcCode']==-1) or not (row['sdkCode'] in sdk if kind=='sdk' else row['sdkCode']==0) or row['redisPrefix'] not in redis or kind!='redis' and row['redisPrefix']!='none'):raise ValueError('typed startup code/prefix differs')
  found.append({'source':{'path':policy['path'],'sha256':policy['sha256']},'diagnostic':value,'diagnosticOnlyNotStageSuccess':True})
 if len(found)>1:raise ValueError('ambiguous typed startup marker')
 return found[0] if found else None


def chart_artifact_binding(c):
 p=chart_assembly_policy(c)
 return {'mode':ASSEMBLY_MODE,'currentChart5':p['currentChart5']['artifact'],'historicalRouteFrom6':p['historicalRouteFrom6']['artifact']}

def require_bound(c):
 if c.get('chartAssembly',{}).get('pendingCurrent5') is not None:raise ValueError('pending current5 cannot dispatch runtime')
 binding=c.get('executionBinding')
 if not isinstance(binding,dict) or set(binding)!={'rootAccepted','source','rootAcceptanceSHA256','mode','core14','currentChart5','historicalRouteFrom6'} or binding.get('rootAccepted') is not True or binding.get('source')!=core_source(c) or not re.fullmatch('[0-9a-f]{64}',binding.get('rootAcceptanceSHA256','')) or binding.get('mode')!=ASSEMBLY_MODE:raise ValueError('Root-bound current core source and products required')
 if 'currentCoreBuild' in c:
  if binding['core14'] is not None:raise ValueError('fresh core build cannot use a historical core artifact')
 else:artifact_binding(binding['core14'])
 for key in ('currentChart5','historicalRouteFrom6'):artifact_binding(binding[key])
 selected={k:binding[k] for k in ('mode','currentChart5','historicalRouteFrom6')}
 if selected!=c['acceptedChartArtifact'] or selected!=chart_artifact_binding(c):raise ValueError('original split chart and route artifact bindings differ')
 mysql=c.get('mysql')
 if not isinstance(mysql,dict) or not re.fullmatch('docker.io/library/mysql@sha256:[0-9a-f]{64}',mysql.get('image','')) or not mysql.get('officialSourceReviewSHA256'):raise ValueError('reviewed exact MySQL fixture binding required')
 return binding

def manifest_products(m,a,names,limit,core_source=None,chart_sources=None,chart5=None):
 if sum(x is not None for x in (core_source,chart_sources,chart5))!=1:raise ValueError('one explicit original manifest schema required')
 if not isinstance(m,dict) or not isinstance(m.get('binaries'),list):raise ValueError('explicit product manifest object required')
 products=m['binaries']
 if len(products)!=len(names) or {r['name'] for r in products}!=set(names) or sum(r['bytes'] for r in products)>limit:raise ValueError('exact product manifest required')
 if core_source:
  if not {'source','head','runID','attempt','CGO_ENABLED','configsLogsKeysAuxiliariesIncluded'}<=set(m):raise ValueError('core manifest schema required')
  if m['source']!=core_source or m['head']!=a['head'] or str(m['runID'])!=str(a['run']) or str(m['attempt'])!=str(a['attempt']) or m['CGO_ENABLED']!='1' or m['configsLogsKeysAuxiliariesIncluded'] is not False:raise ValueError('core product source/platform differs')
 elif chart5 is not None:
  if a!=chart5['artifact'] or m!=chart5['expectedManifest']['body']:raise ValueError('actual five manifest differs from explicit expected producer body')
 else:
  if not {'sourceVectors','head','run','attempt','sourceHelperToolPostExact','configsLogsOrSourceIncluded'}<=set(m):raise ValueError('original six manifest schema required')
  if m['sourceVectors']!=chart_sources or m['head']!=a['head'] or str(m['run'])!=str(a['run']) or str(m['attempt'])!=str(a['attempt']) or m['sourceHelperToolPostExact'] is not True or m['configsLogsOrSourceIncluded'] is not False:raise ValueError('six product source/platform differs')
 return products

def assemble_chart_products(c,five,six):
 p=chart_assembly_policy(c);current,old=p['currentChart5'],p['historicalRouteFrom6']
 current_rows=manifest_products(five,current['artifact'],tuple(CHART5),512<<20,chart5=current)
 old_rows=manifest_products(six,old['artifact'],NAMES,512<<20,chart_sources=old['provider']['sources'])
 if six['locks']!=old['provider']['locks']:raise ValueError('original six module locks differ')
 route=next(r for r in old_rows if r['name']=='route-service')
 if route!=old['selectedRoute']:raise ValueError('original route selection differs')
 by_name={r['name']:{'provider':'currentChart5','binary':r} for r in current_rows}
 by_name['route-service']={'provider':'historicalRouteFrom6','binary':route}
 return [by_name[name] for name in NAMES]
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,req,fp,code,msg,headers,newurl):return None

def artifact(a,names,directory,limit,core_source=None,chart_sources=None,chart5=None):
 # Only API metadata receives the task token. Signed blob URL is never logged
 # or sent with Bearer; no binary is downloaded on the local workstation.
 repo=os.environ['GITHUB_REPOSITORY'];token=os.environ['GH_TOKEN']
 if not re.fullmatch('[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+',repo):raise ValueError('repository shape differs')
 base='https://api.github.com/repos/'+repo+'/actions/artifacts/'+str(a['artifactID'])
 headers={'Authorization':'Bearer '+token,'Accept':'application/vnd.github+json','X-GitHub-Api-Version':'2022-11-28'}
 def api(path):
  with urllib.request.urlopen(urllib.request.Request(path,headers=headers),timeout=30) as r:
   raw=r.read((2<<20)+1)
   if len(raw)>2<<20:raise ValueError('metadata limit')
   return json.loads(raw)
 meta=api(base)
 if meta['id']!=a['artifactID'] or meta['expired'] or meta['size_in_bytes']!=a['artifactBytes'] or meta.get('digest')!='sha256:'+a['artifactSHA256'] or meta['workflow_run']['id']!=a['run'] or meta['workflow_run']['head_sha']!=a['head']:raise ValueError('artifact API body differs')
 run=api('https://api.github.com/repos/'+repo+'/actions/runs/'+str(a['run']))
 if run['head_sha']!=a['head'] or run['run_attempt']!=a['attempt'] or run['status']!='completed' or run['conclusion']!='success':raise ValueError('actual successful original producer required')
 try:urllib.request.build_opener(NoRedirect()).open(urllib.request.Request(base+'/zip',headers=headers),timeout=30);raise ValueError('signed download redirect required')
 except urllib.error.HTTPError as e:
  if e.code!=302:raise
  url=e.headers['Location']
 u=urllib.parse.urlsplit(url)
 if u.scheme!='https' or not u.hostname or not u.hostname.endswith('.blob.core.windows.net') or u.username or u.password or u.port not in (None,443):raise ValueError('anonymous GitHub blob required')
 archive=directory.parent/(directory.name+'.zip');h=hashlib.sha256();size=0
 with urllib.request.urlopen(url,timeout=30) as source,archive.open('xb') as target:
  while part:=source.read(1<<20):
   size+=len(part)
   if size>a['artifactBytes']:raise ValueError('artifact size cap')
   target.write(part);h.update(part)
 if size!=a['artifactBytes'] or h.hexdigest()!=a['artifactSHA256']:raise ValueError('artifact ZIP body differs')
 directory.mkdir(mode=0o700)
 with zipfile.ZipFile(archive) as z:
  rows=z.infolist()
  if len(rows)!=len(names)+1 or {r.filename for r in rows}!=set(names)|{'manifest.json'} or len({r.filename for r in rows})!=len(rows) or sum(r.file_size for r in rows)>limit+(1<<20):raise ValueError('exact bounded flat artifact required')
  for row in rows:
   kind=stat.S_IFMT(row.external_attr>>16)
   if row.flag_bits&1 or kind not in (0,stat.S_IFREG) or row.file_size>(1<<20 if row.filename=='manifest.json' else (256<<20 if core_source else 128<<20)):raise ValueError('artifact entry type/cap')
   with z.open(row) as src,(directory/row.filename).open('xb') as dst:
    total=0
    while part:=src.read(1<<20):dst.write(part);total+=len(part)
    if total!=row.file_size:raise ValueError('complete CRC-checked body required')
 manifest_raw=regular(directory/'manifest.json',1<<20)
 if sha(manifest_raw)!=a['manifestSHA256']:raise ValueError('original producer manifest differs')
 m=json.loads(manifest_raw);products=manifest_products(m,a,names,limit,core_source=core_source,chart_sources=chart_sources,chart5=chart5)
 for row in products:
  p=directory/row['name'];rawhead=b'';h=hashlib.sha256();n=0
  with p.open('rb') as f:
   while part:=f.read(1<<20):
    if not rawhead:rawhead=part[:64]
    n+=len(part);h.update(part)
  if n!=row['bytes'] or h.hexdigest()!=row['sha256'] or rawhead[:6]!=b'\x7fELF\x02\x01' or rawhead[18:20]!=b'\x3e\x00' or row['GOOS']!='linux' or row['GOARCH']!='amd64' or row['CGO_ENABLED']!=('1' if core_source else '0') or row['mode']!=0o700:raise ValueError('original ELF body/mode/platform differs')
  p.chmod(0o700) # upload-artifact does not preserve Unix permissions.
 return m

def rootdir():return Path(os.environ['RUNNER_TEMP'])/('product20-'+os.environ['GITHUB_RUN_ID']+'-'+os.environ['GITHUB_RUN_ATTEMPT'])
def outdir():return REPO/'ci-results/product20'
def envbase():return {'PATH':os.environ['PATH'],'HOME':os.environ['HOME'],'GOTOOLCHAIN':'local','GOWORK':'off','GOENV':'off','GOAUTH':'off','GOMAXPROCS':'2','GOMEMLIMIT':'900MiB','CGO_ENABLED':'1'}
def allocation_owner():return {'run':os.environ['GITHUB_RUN_ID'],'attempt':os.environ['GITHUB_RUN_ATTEMPT'],'head':os.environ['GITHUB_SHA'],'uid':os.getuid()}
def admit():
 c,*_=verify();require_bound(c);root=rootdir()
 if os.getuid()==0 or os.environ.get('RUNNER_ENVIRONMENT')!='github-hosted' or os.environ.get('RUNNER_OS')!='Linux' or root.exists() or root.resolve()!=root or os.statvfs(root.parent).f_bavail*os.statvfs(root.parent).f_frsize<4<<30:raise ValueError('fresh hosted cold4GiB admission required')
 root.mkdir(mode=0o700);write(root/'allocation.json',allocation_owner());return 0

P20_FAILURE_STAGES = frozenset(('before-post-aux-records','browser-launch-probe','web-bundle-receive','core-receipt-reconstruction','legacy-elf-read','legacy-receipt-write','build-ledger-write','fixture-modules-and-catalog','minio-source-build','minio-artifact-receive','minio-public-receipts','post-aux-boundary-complete'))
P20_FAILURE_SOURCES = ('ci/product20/run.py','ci/product20/browser_bundle.py','ci/backend-fullgraph/minio_build.py','ci/backend-fullgraph/resources.py','ci/backend-fullgraph/service_selectors.py')

def project_p20_failure(stage,error,c,controls):
 if type(stage) is not str or stage not in P20_FAILURE_STAGES or not isinstance(error,BaseException):raise ValueError('closed original P20 failure stage and exception required')
 aliases={};limits={}
 for relative in P20_FAILURE_SOURCES:
  path=REPO/relative;expected=controls.get(relative,c['helpers'].get(relative));body=regular(path,1<<20)
  if not isinstance(expected,str) or not re.fullmatch('[0-9a-f]{64}',expected) or sha(body)!=expected:raise ValueError('unchanged allowed public CI source required')
  aliases[str(path)]=relative;aliases[relative]=relative;aliases['./'+relative]=relative;limits[relative]=len(body.splitlines())
 kinds=((ValueError,'value-error'),(KeyError,'key-error'),(TypeError,'type-error'),(FileNotFoundError,'file-missing'),(FileExistsError,'file-exists'),(PermissionError,'permission'),(TimeoutError,'timeout'),(urllib.error.HTTPError,'http-error'),(urllib.error.URLError,'url-error'),(zipfile.BadZipFile,'zip-error'),(OSError,'os-error'))
 statuses={301:'redirect',302:'redirect',303:'redirect',307:'redirect',308:'redirect',400:'bad-request',401:'unauthorized',403:'forbidden',404:'not-found',408:'timeout',409:'conflict',410:'gone',413:'too-large',422:'unprocessable',429:'rate-limited',500:'server-error',502:'server-error',503:'server-error',504:'server-error'}
 base=lambda exc,name:BaseException.__dict__[name].__get__(exc,BaseException)
 result={'version':'product20-post-aux-failure-observation-v1','stage':stage,'exceptions':[],'chainTruncated':False,'framesTruncated':False,'contextTraversal':'bounded-cause-or-context-including-suppressed','diagnosticOnly':True,'uniqueCauseProven':False,'rawErrorTextExported':False,'exceptionArgumentsRead':False,'privateLogRead':False,'privateContentHashesExported':False,'runtimeReadyP20Accepted':False}
 current=error;seen=set();frame_seen=set();visited_frames=0
 while current is not None and len(result['exceptions'])<4:
  if id(current) in seen:result['chainTruncated']=True;break
  seen.add(id(current));exact=type(current);kind=next((label for cls,label in kinds if exact is cls),'other');row={'class':kind,'frames':[]}
  if exact is urllib.error.HTTPError:
   code=dict.get(object.__getattribute__(current,'__dict__'),'code')
   row['httpStatus']=code if type(code) is int and code in statuses else None;row['httpStatusClass']=statuses[code] if type(code) is int and code in statuses else 'other-http'
  trace=base(current,'__traceback__')
  while trace is not None and visited_frames<32:
   visited_frames+=1;relative=aliases.get(trace.tb_frame.f_code.co_filename);line=trace.tb_lineno
   if relative is not None and type(line) is int and 1<=line<=limits[relative]:
    key=(relative,line)
    if key not in frame_seen:
     if len(frame_seen)<8:frame_seen.add(key);row['frames'].append({'path':relative,'line':line})
     else:result['framesTruncated']=True
   trace=trace.tb_next
  if trace is not None:result['framesTruncated']=True
  result['exceptions'].append(row);cause=base(current,'__cause__');current=cause if cause is not None else base(current,'__context__')
 if current is not None:result['chainTruncated']=True
 return result

def emit_p20_failure_observation(out,stage,error,c,controls):
 # Observation failure must never replace the original controller exception.
 try:write(out/'pre-fixture-failure-observation.json',project_p20_failure(stage,error,c,controls));return True
 except BaseException:return False

P20_CHILD_EXIT_POLICY={'version':'p20-original-six-child-exit-v1','perStreamBytes':4<<20,'totalBytes':8<<20,'lineLimit':16384,'lineBytesLimit':8192,'framesLimit':8}
P20_CHILD_EXIT_FAMILIES={'tools-log':('github.com/OpenIMSDK/tools','v0.0.17','log/'),'zk':('github.com/go-zookeeper/zk','v1.0.3',''),'grpc':('google.golang.org/grpc','v1.84.0','')}
def p20_exit_private(root,relative,maximum):
 # All callers use fixed children of this exact allocation; reject aliases at
 # every directory and again after the bounded nonblocking fd read.
 root=Path(root)
 if not root.is_absolute() or root.resolve()!=root or not relative or any(type(x) is not str or not x or x in ('.','..') or '/' in x or '\\' in x for x in relative):raise ValueError('original child private path rejected')
 parents=[root];p=root
 for name in relative[:-1]:p=p/name;parents.append(p)
 def directories():
  values=[]
  for directory in parents:
   a=directory.lstat()
   if not stat.S_ISDIR(a.st_mode) or stat.S_IMODE(a.st_mode)!=0o700 or a.st_uid!=os.getuid() or directory.resolve()!=directory:raise ValueError('original child private directory rejected')
   values.append((a.st_dev,a.st_ino,a.st_mode,a.st_uid))
  return values
 before=directories();path=p/relative[-1];fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK|os.O_CLOEXEC)
 try:
  a=os.fstat(fd)
  if not stat.S_ISREG(a.st_mode) or stat.S_IMODE(a.st_mode)!=0o600 or a.st_uid!=os.getuid() or a.st_nlink!=1 or not 0<=a.st_size<=maximum:raise ValueError('original child private file rejected')
  chunks=[];remaining=maximum+1
  while remaining:
   part=os.read(fd,min(remaining,65536))
   if not part:break
   chunks.append(part);remaining-=len(part)
  raw=b''.join(chunks);b=os.fstat(fd);c=path.lstat();key=lambda x:(x.st_dev,x.st_ino,x.st_mode,x.st_uid,x.st_nlink,x.st_size,x.st_mtime_ns,x.st_ctime_ns)
  if len(raw)!=a.st_size or key(a)!=key(b) or key(a)!=key(c) or directories()!=before:raise ValueError('original child private file changed')
  return raw
 finally:os.close(fd)

def p20_exit_projection_empty(status='unavailable'):
 return {'version':P20_CHILD_EXIT_POLICY['version'],'status':status,'role':'none','headerClasses':[],'fixedClasses':[],'dependencyFamilies':[],'sourceFrames':[],'scanTruncated':False,'originalOwnerAndProductBinding':False,'diagnosticOnly':True,'rawTextExported':False,'privatePathsPIDsOrContentHashesExported':False,'prefixIsCauseProof':False,'physicalFirstFailureClaimed':False,'runtimeReadyP20Accepted':False}

def project_p20_child_log_streams(streams,chart_bodies):
 policy=P20_CHILD_EXIT_POLICY;result=p20_exit_projection_empty('projected');headers=set();kinds=set();families=set();seen=set();lines=0
 if type(streams) is not dict or set(streams)!={'stdout','stderr'} or any(type(x) is not bytes or len(x)>policy['perStreamBytes'] for x in streams.values()) or sum(map(len,streams.values()))>policy['totalBytes']:raise ValueError('bounded original child streams required')
 gomod=chart_bodies.get('chart/go.mod',b'');dependency_prefixes={}
 for family,(module,version,subdir) in P20_CHILD_EXIT_FAMILIES.items():
  if re.search(rb'^\s*'+re.escape(module.encode())+rb'\s+'+re.escape(version.encode())+rb'\s*(?://[^\r\n]*)?$',gomod,re.M):dependency_prefixes[module+'@'+version+'/'+subdir]=family
 frame_pattern=re.compile(rb'\t([^\s]+\.go):([1-9][0-9]{0,6})(?: \+0x[0-9a-f]{1,16})?')
 fixed=((b'read loacl config file error:','config-read'),(b'parse loacl openIMConfig file error:','config-yaml'),(b'get env error:','config-env'),(b'incomplete MySQL configuration','mysql-config'))
 for stream in ('stderr','stdout'):
  raw=streams[stream];start=0;panic_region=False
  while start<len(raw):
   if lines>=policy['lineLimit']:result['scanTruncated']=True;break
   end=raw.find(b'\n',start)
   if end<0:end=len(raw)
   lines+=1;line=raw[start:end];start=end+1
   if len(line)>policy['lineBytesLimit']:result['scanTruncated']=True;continue
   if line.endswith(b'\r'):line=line[:-1]
   payload=None
   if line.startswith(b'panic: '):headers.add('go-panic');payload=line[7:];panic_region=True
   elif line.startswith(b'fatal error: '):headers.add('go-runtime-fatal');panic_region=True
   elif re.fullmatch(rb'goroutine [0-9]+ \[[^\r\n]{1,160}\]:',line) or line in (b'runtime stack:',b'stack trace:'):panic_region=False
   elif panic_region and line:payload=line[1:] if line.startswith(b'\t') else line
   if payload is not None:
    for prefix,kind in fixed:
     if payload.startswith(prefix):kinds.add(kind)
   if line.startswith(b'flag provided but not defined: '):headers.add('flag-parser');kinds.add('flag-unknown')
   elif line.startswith(b'flag needs an argument: '):headers.add('flag-parser');kinds.add('flag-missing-argument')
   elif line.startswith(b'invalid value ') and b' for flag -' in line:headers.add('flag-parser');kinds.add('flag-invalid-value')
   match=frame_pattern.fullmatch(line)
   if match is None:continue
   try:name=match[1].decode('ascii')
   except UnicodeError:continue
   if name.startswith('/') or '\\' in name or ':' in name or any(x in ('','.','..') for x in name.split('/')):continue
   for prefix,family in dependency_prefixes.items():
    if name.startswith(prefix):families.add(family)
   prefix='github.com/OpenIMSDK/chat/'
   if not name.startswith(prefix):continue
   relative=name[len(prefix):]
   if not re.fullmatch(r'[A-Za-z0-9_./-]+\.go',relative):continue
   target='chart/'+relative;body=chart_bodies.get(target);number=int(match[2])
   if type(body) is not bytes or not 1<=number<=len(body.splitlines()):continue
   key=(target,number)
   if key in seen:continue
   seen.add(key)
   if len(result['sourceFrames'])>=policy['framesLimit']:result['scanTruncated']=True;continue
   result['sourceFrames'].append({'path':target,'line':number,'sourceSHA256':sha(body)})
 result['headerClasses']=sorted(headers) or ['unknown'];result['fixedClasses']=sorted(kinds);result['dependencyFamilies']=sorted(families) or ['unknown'];return result

def project_p20_original_child_exit(root,terminal,expected_plan,plan_sha,products,chart_bodies,c):
 result=p20_exit_projection_empty('not-eligible');phase='fresh-sql-and-six-real-services';boundary=terminal.get('startupBoundary',{});role=boundary.get('role');cause=boundary.get('primaryCause',{})
 if terminal.get('phase')!=phase or boundary.get('substep')!='product-wait-port' or role not in NAMES or cause.get('present') is not True or cause.get('execState') not in ('exited','signaled') or 'exec-exit' not in cause.get('classes',[]) or cause.get('truncated') is not False:return result
 slot=NAMES.index(role)
 if any(type(boundary.get(k)) is not int for k in ('sixForked','sixReady')) or boundary.get('sixForked')!=slot+1 or boundary.get('sixReady')!=slot:raise ValueError('original failed child slot differs')
 if type(cause.get('exitCode')) is not int or type(cause.get('signal')) is not int or (cause['execState']=='exited' and not (0<=cause['exitCode']<=255 and cause['signal']==0)) or (cause['execState']=='signaled' and not (cause['exitCode']==-1 and 1<=cause['signal']<=64)):raise ValueError('original failed child exit shape differs')
 owner=allocation_owner();load_private=lambda relative,maximum:json.loads(p20_exit_private(root,relative,maximum))
 for relative in (('allocation.json',),('private','owner.json')):
  actual=load_private(relative,4096)
  if type(actual) is not dict or set(actual)!={'run','attempt','head','uid'} or type(actual.get('uid')) is not int or any(type(actual.get(k)) is not str for k in ('run','attempt','head')) or actual!=owner:raise ValueError('original child allocation owner differs')
 if type(owner.get('uid')) is not int or owner['uid']!=os.getuid():raise ValueError('original child allocation uid differs')
 raw=p20_exit_private(root,('private','product20-input.json'),128<<10)
 if type(plan_sha) is not str or not re.fullmatch('[0-9a-f]{64}',plan_sha) or sha(raw)!=plan_sha:raise ValueError('original child plan identity differs')
 plan=json.loads(raw)
 if plan!=expected_plan or plan.get('version')!='fresh-product20-http-ws-v1' or plan.get('work')!=str(root/'chain') or plan.get('products')!=products:raise ValueError('original child plan differs')
 intent=load_private(('private','operator-intent.json'),4096)
 if intent!={'pidBindingAtOriginalStart':True,'inputSHA256':plan_sha,'source':core_source(c),'harness':c['derivedHarnessSource']}:raise ValueError('original operator intent differs')
 if type(products) is not list or len(products)!=6 or {p.get('name') for p in products}!=set(NAMES):raise ValueError('six received product bindings required')
 for row in products:
  name=row['name'];module='route' if name=='route-service' else 'chart';folder='historical-six' if module=='route' else 'current-chart5'
  if set(row)!={'name','path','sha256','bytes','sourceSHA256'} or row['path']!=str(root/folder/name) or type(row['bytes']) is not int or not 0<row['bytes']<=128<<20 or type(row['sha256']) is not str or not re.fullmatch('[0-9a-f]{64}',row['sha256']) or row['sourceSHA256']!=c['chartProvider']['sources'][module]['sha256']:raise ValueError('original received product differs')
 raw=p20_exit_private(root,('chain','product20-result.json'),128<<10)
 if sha(raw)!=terminal.get('originalResultSHA256'):raise ValueError('original failed result changed')
 original=json.loads(raw)
 if original.get('version')!='fresh-product20-http-ws-result-v1' or original.get('accepted') is not False or original.get('phase')!=phase or original.get('coreSourceSHA256')!=core_source(c)['digest'] or project_startup_boundary(original)!=boundary:raise ValueError('original startup projection differs')
 pids=original.get('sixOriginalPIDs')
 if type(pids) is not list or len(pids)!=slot+1 or any(type(p) is not int or p<=1 for p in pids) or len(set(pids))!=len(pids):raise ValueError('original child pid slots differ')
 child=load_private(('chain',role,'original-child.json'),4096);product=next(p for p in products if p['name']==role)
 if type(child) is not dict or set(child)!={'pid','startToken','path','sha256','uid'} or type(child['pid']) is not int or child['pid']!=pids[slot] or type(child['uid']) is not int or child['uid']!=os.getuid() or type(child['startToken']) is not str or not re.fullmatch('[0-9]{1,20}',child['startToken']) or child['path']!=product['path'] or child['sha256']!=product['sha256']:raise ValueError('original failed child owner differs')
 if sha(chart_bodies.get('chart/go.mod',b''))!=c['chartProvider']['locks']['chart']['go.mod']:raise ValueError('original Chart dependency identity differs')
 streams={name:p20_exit_private(root,('chain',role,name+'.private'),P20_CHILD_EXIT_POLICY['perStreamBytes']) for name in ('stdout','stderr')}
 result=project_p20_child_log_streams(streams,chart_bodies);result['role']=role;result['originalOwnerAndProductBinding']=True
 typed=project_chart_startup_lines(streams,chart_bodies,c.get('typedStartupDiagnostic'))
 if typed is not None:result['typedStartup']=typed
 return result

def emit_p20_child_exit_observation(root,out,terminal,plan,plan_sha,products,chart_bodies,c):
 try:
  try:value=project_p20_original_child_exit(root,terminal,plan,plan_sha,products,chart_bodies,c)
  except BaseException:value=p20_exit_projection_empty('unavailable')
  write(out/'original-child-exit-observation.json',value);return True
 except BaseException:return False

def acceptance_scope():
 scope=os.environ.get('PRODUCT20_SCOPE') or 'backend'
 if scope!='backend' and scope not in BROWSER_SCOPES:
  raise ValueError('unknown Product20 acceptance scope')
 return scope

def task_user_command(home,temporary):
 groups=','.join(map(str,sorted(set(os.getgroups()+[os.getgid()]))))
 return ['/usr/bin/setpriv','--reuid',str(os.getuid()),'--regid',str(os.getgid()),'--groups',groups,
         '--no-new-privs','--','/usr/bin/env','-i','PATH=/usr/bin:/bin','HOME='+str(home),'TMPDIR='+str(temporary)]

def browser_user_command(work, node, script, argument):
 user=pwd.getpwuid(os.getuid()).pw_name
 return ['/usr/sbin/runuser','-u',user,'--','/usr/bin/env','-i','-C',str(work),'PATH=/usr/bin:/bin',
         'HOME='+str(work),'TMPDIR='+str(work),node,script,argument]

# Both children inherit the fixture network namespace. Only the Chrome child
# uses the installed browser's normal user sandbox launcher.
BROWSER_SIBLING_LAUNCH = '''
count="$1"
shift
browser=("${@:1:count}")
shift "$count"
"${browser[@]}" &
browser_pid=$!
cleanup_browser() {
    status=$?
    trap - EXIT
    kill -TERM %1 2>/dev/null || true
    wait "$browser_pid" 2>/dev/null || true
    exit "$status"
}
trap cleanup_browser EXIT
trap 'exit 143' TERM
trap 'exit 130' INT
"$@"
wait "$browser_pid"
trap - EXIT
'''

def run():
 scope=acceptance_scope()
 c,graph,g,original,bodies,chart_bodies=verify();binding=require_bound(c)
 if any(os.environ.get(k)!=v for k,v in {'GITHUB_ACTIONS':'true','RUNNER_ENVIRONMENT':'github-hosted','RUNNER_OS':'Linux'}.items()) or os.getuid()==0:raise ValueError('fresh hosted nonroot Linux only')
 root=rootdir();out=outdir()
 if root.resolve()!=root or stat.S_IMODE(root.stat().st_mode)!=0o700 or root.stat().st_uid!=os.getuid() or not {'allocation.json'}<={p.name for p in root.iterdir()}<={'allocation.json','controller-private.log'} or json.loads(regular(root/'allocation.json'))!=allocation_owner():raise ValueError('original fresh admission required')
 out.mkdir(mode=0o700,parents=True,exist_ok=False);private=root/'private';private.mkdir(mode=0o700)
 write(private/'owner.json',{'run':os.environ['GITHUB_RUN_ID'],'attempt':os.environ['GITHUB_RUN_ATTEMPT'],'head':os.environ['GITHUB_SHA'],'uid':os.getuid()})
 controls={p:sha(regular(REPO/p)) for p in c['controllers']};tools=graph.tool_snapshot();write(out/'pre.json',{'controls':controls,'tools':tools,'coreProduct':core_source(c),'harness':c['derivedHarnessSource'],'chart':c['chartProvider']['sources']})
 stage=root/'source';stage.mkdir(mode=0o700)
 for target,raw in bodies.items():
  p=stage/target;p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(raw);p.chmod(g['sourceModes'].get(target,0o644))
 (stage/'server/config/config.yaml').write_bytes(bodies['server/config/config.example.yaml']);(stage/'server/config/config.yaml').chmod(0o600)
 env=envbase()
 for key,name in [('GOMODCACHE','modcache'),('GOCACHE','buildcache'),('GOPATH','gopath'),('TMPDIR','tmp')]:
  p=root/name;p.mkdir(mode=0o700);env[key]=str(p)
 env['GOTMPDIR']=env['TMPDIR'];index=0;started=time.monotonic();plane=None;mysql_id=None;primary=None;failure_stage='before-post-aux-records';core_stage=None
 def command(label,argv,timeout,environment=None,cwd=None):
  nonlocal index
  if timeout>10800-(time.monotonic()-started):raise ValueError('controller budget exhausted')
  index+=1;folder=private/(str(index)+'-'+label);folder.mkdir(mode=0o700)
  child=[sys.executable,'-B',str(REPO/'ci/group-effects/capture.py'),'--stdout',str(folder/'stdout'),'--stderr',str(folder/'stderr'),'--']
  if environment is not None:child+=['/usr/bin/env','-i']+[k+'='+v for k,v in environment.items()]
  child+=argv
  wrapped=[sys.executable,'-B',str(REPO/'ci/group-effects/resource_guard.py'),'--cwd',str(cwd or stage/'server'),'--log',str(folder/'guard.log'),'--record',str(folder/'resource.json'),'--min-free-mib','1024','--poll-interval','.2','--allocated-root',str(root),'--max-allocated-mib','16384','--timeout',str(timeout),'--']+child
  proc=subprocess.run(wrapped,timeout=timeout+30,check=False);record=json.loads(regular(folder/'resource.json'))
  write(out/(str(index)+'-phase.json'),{'phase':label,'exitCode':proc.returncode,'resource':record,'stdoutSHA256':sha(regular(folder/'stdout',32<<20)),'stderrSHA256':sha(regular(folder/'stderr',32<<20))})
  failed=proc.returncode!=0 or record.get('status')!='passed' or record.get('exit_code')!=0
  if label=='browser-launch-probe':
   observation={'stage':'unavailable','documentStatus':0,'configStatus':0,'configRequested':False,'chromeFailure':'none'}
   try:
    observation=browser_observation(json.loads(regular(folder/'stdout',4096)))
   except (ValueError,OSError):
    failed=True
   failed=failed or observation['stage']!='complete'
   write(out/'browser-launch-probe.json',{'accepted':not failed,'observation':observation})
  if failed and (label.startswith('compile-') or label in ('minio-source-compile','current-core-mongo-normalization-test',GATEWAY_TEST_PHASE)) and not (root/'plane').exists():
   # Identical boundary to the reviewed fullgraph compiler projection: these
   # are only fixed public sources, before private resource config/credentials.
   raw=regular(folder/'stderr',32<<20)
   write(out/(str(index)+'-public-compile-failure.json'),{'phase':label,'publicSourceOnly':True,'completeBytes':len(raw),'completeSHA256':sha(raw),'diagnostic':raw[:65536].decode('utf-8','replace'),'truncated':len(raw)>65536})
  if label=='actual-fresh-product20-http-ws-once' and (root/'chain/product20-result.json').exists():
   terminal=export_terminal(root/'chain/product20-result.json',out/'actual-terminal.json')
   if failed:
    try:emit_p20_child_exit_observation(root,out,terminal,p,ps,products,chart_bodies,c)
    except BaseException:pass
  if failed:raise ValueError('phase failed; private raw retained')
  return regular(folder/'stdout',32<<20)
 try:
  browser=None
  if scope in BROWSER_SCOPES:
   failure_stage='browser-launch-probe'
   node=shutil.which('node')
   if node is None:
    raise ValueError('installed Node is required for browser acceptance')
   browser={'node':str(Path(node).resolve(strict=True)),'script':str(REPO/'ci/product20/browser-smoke.mjs')}
   probe=root/'browser-probe';probe.mkdir(mode=0o700)
   command('browser-launch-probe',['sudo','-n','/usr/bin/unshare','--net','--','/bin/bash','-euc',
           '/usr/sbin/ip link set lo up; exec "$@"','product20-browser-probe',
           *browser_user_command(probe,browser['node'],browser['script'],'--probe')],45,env,probe)
   failure_stage='before-post-aux-records'
  core=None
  if 'currentCoreBuild' not in c:core=artifact(binding['core14'],graph.ROLES,root/'product14',2<<30,core_source=core_source(c))
  policy=chart_assembly_policy(c)
  five=artifact(binding['currentChart5'],tuple(CHART5),root/'current-chart5',512<<20,chart5=policy['currentChart5'])
  six=artifact(binding['historicalRouteFrom6'],NAMES,root/'historical-six',512<<20,chart_sources=policy['historicalRouteFrom6']['provider']['sources'])
  selected=assemble_chart_products(c,five,six)
  write(out/'chart-assembly.json',{'mode':ASSEMBLY_MODE,'currentChart5':binding['currentChart5'],'historicalRouteFrom6':binding['historicalRouteFrom6'],'selected':selected,'productRebuilds':0,'oldChart258Selected':False,'expectedFiveManifestNowReceivedAndVerified':True})
  # Keep the artifact credential in this process, outside all child environments.
  fixture_token=os.environ.pop('GH_TOKEN',None)
  if browser is not None:
   failure_stage='web-bundle-receive'
   receiver=load('ci/product20/browser_bundle.py','p20_browser_bundle')
   dist=receiver.receive(REPO,root/'web-bundle',fixture_token)
   browser['dist']=str(dist)
   write(out/'web-bundle.json',{'artifact':receiver.ARTIFACT,'source':receiver.SOURCE,'scope':scope})
   failure_stage='before-post-aux-records'
  # Current-core builds use canonical source independently from the derived
  # fixture/auxiliary source. The historical artifact receiver stays separate.
  go=shutil.which('go');version=command('go-version',[go,'version'],15,env).decode().strip()
  if version!='go version go1.27.2 linux/amd64':raise ValueError('exact Go1.27.2 required')
  go_path=Path(go).resolve();go_raw=regular(go_path);go_sha=sha(go_raw)
  actual_go={'version':version,'bytes':len(go_raw),'sha256':go_sha,'mode':stat.S_IMODE(go_path.stat().st_mode)}
  del go_raw
  env['GOROOT']=command('go-root',[go,'env','GOROOT'],15,env).decode().strip()
  command('auxiliary-module-download',[go,'mod','download','-json'],600,dict(env,GOPROXY='https://proxy.golang.org',GOSUMDB='sum.golang.org'))
  offline=dict(env,GOPROXY='off',GOSUMDB='off');binroot=root/'aux';binroot.mkdir(mode=0o700)
  if 'currentCoreBuild' in c:
   core_stage=root/'current-core-source';core_stage.mkdir(mode=0o700)
   for target,body in original.items():
    path=core_stage/target;path.parent.mkdir(parents=True,exist_ok=True);path.write_bytes(body);path.chmod(g['sourceModes'].get(target,0o644))
   (core_stage/'server/config/config.yaml').write_bytes(original['server/config/config.example.yaml']);(core_stage/'server/config/config.yaml').chmod(0o600)
   if scope=='backend':
    if c['currentCoreBuild']['mode']==CORE_GATEWAY_BUILD_MODE:
     events=command(GATEWAY_TEST_PHASE,[go,'test','-json','-mod=readonly','-p=1','-count=1','-run','^'+GATEWAY_TEST_NAME+'$','./internal/msgtransfer'],600,offline,core_stage/'server')
     write(out/'current-core-test.json',check_gateway_core_test_events(events))
    elif 'coreMongoTestReuse' in c:
     write(out/'current-core-test-reuse.json',core_mongo_test_reuse_receipt(c,original,actual_go))
    else:
     events=command('current-core-mongo-normalization-test',[go,'test','-json','-mod=readonly','-p=1','-count=1','-run','^'+MONGO_TEST_NAME+'$','./pkg/common/db/unrelation'],600,offline,core_stage/'server')
     write(out/'current-core-test.json',check_current_core_test_events(events))
   productdir=root/'product14';productdir.mkdir(mode=0o700)
   rows=[]
   entries=g['entrypointContract']['entrypoints']
   if len(entries)!=14 or {e['role'] for e in entries}!=set(graph.ROLES):raise ValueError('canonical fourteen entrypoints required')
   for entry in entries:
    name=entry['role'];binary=productdir/name
    command('compile-current-core-'+name,[go,'build','-mod=readonly','-p=1','-o',str(binary),entry['package']],1200,offline,core_stage/'server')
    raw=regular(binary,256<<20)
    if raw[:6]!=b'\x7fELF\x02\x01' or raw[18:20]!=b'\x3e\x00':raise ValueError('actual Linux amd64 core ELF required')
    binary.chmod(0o700)
    receipt={'schema':1,'sourceSHA256':core_source(c)['digest'],'binarySHA256':sha(raw),'binaryBytes':len(raw),'toolchainSHA256':go_sha}
    receipt_bytes=(json.dumps(receipt,separators=(',',':'),ensure_ascii=False)+'\n').encode()
    rows.append({'name':name,'bytes':len(raw),'sha256':sha(raw),'buildReceiptSHA256':sha(receipt_bytes),'mode':0o700,'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'1'})
   core={'source':core_source(c),'locks':{n:sha(original['server/'+n]) for n in ('go.mod','go.sum')},'toolSHA256':{'go':go_sha},'binaries':rows}
   write(out/'current-core-build.json',{'kind':'product20-local-core14-build-only-v1',**core,'freshBuilds':14,'historicalGraphScenariosRerun':0,'runtimeAccepted':False})
  if core['locks']!={n:sha(original['server/'+n]) for n in ('go.mod','go.sum')}:raise ValueError('original module locks differ from actual artifacts')
  for name,pkg in [('msgtransfer-legacy','./cmd/openim-msgtransfer'),('product20-acceptance','./cmd/tools/product20-acceptance')]:command('compile-'+name,[go,'build','-mod=readonly','-p=1','-o',str(binroot/name),pkg],1200,offline)
  failure_stage='core-receipt-reconstruction'
  selectors=load('ci/backend-fullgraph/service_selectors.py','p20_selectors');builds=[]
  for row in core['binaries']:
   receipt={'schema':1,'sourceSHA256':core['source']['digest'],'binarySHA256':row['sha256'],'binaryBytes':row['bytes'],'toolchainSHA256':core['toolSHA256']['go']}
   rp=private/(row['name']+'-receipt.json');rs=write(rp,receipt)
   if rs!=row['buildReceiptSHA256']:raise ValueError('original product receipt cannot be reconstructed exactly')
   builds.append({'role':row['name'],'path':str(root/'product14'/row['name']),'sha256':row['sha256'],'sourceSHA256':core['source']['digest'],'receiptPath':str(rp),'receiptSHA256':rs,'services':selectors.SERVICES[row['name']],'components':selectors.COMPONENTS[row['name']]})
  failure_stage='legacy-elf-read'
  legacy=binroot/'msgtransfer-legacy';legacy_raw=regular(legacy,256<<20)
  failure_stage='legacy-receipt-write'
  rp=private/'legacy-receipt.json';rs=write(rp,{'schema':1,'sourceSHA256':core['source']['digest'],'binarySHA256':sha(legacy_raw),'binaryBytes':len(legacy_raw),'toolchainSHA256':go_sha})
  failure_stage='build-ledger-write'
  buildpath=private/'builds.json';bs=write(buildpath,{'version':'source-bound-fullgraph-builds-v1','sourceSHA256':core['source']['digest'],'binaries':builds,'legacyMessageTransfer':{'role':'msgtransfer-legacy','path':str(legacy),'sha256':sha(legacy_raw),'sourceSHA256':core['source']['digest'],'receiptPath':str(rp),'receiptSHA256':rs}})
  failure_stage='fixture-modules-and-catalog'
  resources=load('ci/backend-fullgraph/resources.py','p20_resources');builder=load('ci/backend-fullgraph/minio_build.py','p20_minio');catalog=json.loads(regular(REPO/'ci/backend-fullgraph/image-catalog.json'))
  # A bound fixture uses the unchanged full API/ZIP/manifest/ELF receiver.
  # Only an explicit null provider selects a new original source build.
  provider=c['fixtureMinIOProvider']
  try:
   if provider is None:
    failure_stage='minio-source-build'
    minio,proof=builder.build(root,catalog,command,go,go_sha,env)
    acquisition={'mode':'original-source-build','fixtureOnly':True,'productRuntimeAccepted':False,'freshMinioCompile':True}
   else:
    failure_stage='minio-artifact-receive'
    minio,proof=builder.receive(root,catalog,provider,go_sha,fixture_token,os.environ['GITHUB_REPOSITORY'])
    acquisition={'mode':'root-accepted-fixture-artifact','fixtureOnly':True,'productRuntimeAccepted':False,'freshMinioCompile':False,'artifactID':provider['artifactID'],'rootAcceptanceSHA256':provider['rootAcceptanceSHA256']}
  finally:fixture_token=None
  failure_stage='minio-public-receipts'
  write(out/'fixture-minio.json',proof);write(out/'fixture-minio-acquisition.json',acquisition)
  failure_stage='post-aux-boundary-complete'
  for image in sorted(set(catalog['images'].values())|{c['mysql']['image']}):command('fixture-image-'+sha(image.encode())[:8],['/usr/bin/docker','--host','unix:///var/run/docker.sock','pull','--platform','linux/amd64',image],600)
  plane=resources.Plane(root/'plane',catalog['images'],minio_source=minio);claim=plane.prepare();keeper=next(r for r in claim['observations'] if r['role']=='keeper')
  mysql_id=create_mysql(plane,c['mysql'],secrets.token_hex(32))
  corework=root/'core';corework.mkdir(mode=0o700);work=root/'chain';work.mkdir(mode=0o700)
  template=REPO/'chart/config/config.example.yaml'
  product_roots={'currentChart5':root/'current-chart5','historicalRouteFrom6':root/'historical-six'}
  products=[{'name':x['binary']['name'],'path':str(product_roots[x['provider']]/x['binary']['name']),'sha256':x['binary']['sha256'],'bytes':x['binary']['bytes'],'sourceSHA256':x['binary']['sourceVector']['sha256']} for x in selected]
  p={'version':'fresh-product20-http-ws-v1','work':str(work),'core':{'version':'owned-fullgraph-acceptance-v1','scenario':'ack','work':str(corework),'planePath':str(plane.private/'plane.json'),'planeSHA256':sha(regular(plane.private/'plane.json')),'credentialsPath':str(plane.private/'credentials.json'),'credentialsSHA256':sha(regular(plane.private/'credentials.json')),'buildsPath':str(buildpath),'buildsSHA256':bs},'chartTemplatePath':str(template),'chartTemplateSHA256':c['helpers']['chart/config/config.example.yaml'],'sqlRootPassword':json.loads(regular(plane.private/'product20-mysql.json'))['rootPassword'],'products':products}
  if browser is not None:
   p['browser']={'socket':str(work/'browser-smoke.sock'),'dist':browser['dist'],'chat':scope=='web-chat'}
  pp=private/'product20-input.json';ps=write(pp,p)
  backend=[*task_user_command(os.environ['HOME'],work),str(binroot/'product20-acceptance'),
           '--private-input',str(pp),'--private-input-sha256',ps]
  if browser is not None:
   launch=browser_user_command(work,browser['node'],browser['script'],'--listen')
   backend=['/bin/bash','-euc',BROWSER_SIBLING_LAUNCH,'product20-browser',str(len(launch)),*launch,*backend]
  argv=['sudo','-n','/usr/bin/nsenter','--net=/proc/'+str(keeper['PID'])+'/ns/net','--',*backend]
  write(private/'operator-intent.json',{'pidBindingAtOriginalStart':True,'inputSHA256':ps,'source':core['source'],'harness':c['derivedHarnessSource']})
  command('actual-fresh-product20-http-ws-once',argv,1380)
  result=json.loads(regular(work/'product20-result.json',128<<10));check_result(result,c)
  write(out/'actual-result.json',result)
 except BaseException as e:
  primary=e
  try:emit_p20_failure_observation(out,failure_stage,e,c,controls)
  except BaseException:pass
  raise
 finally:
  try:
   if plane is not None:
    cleanup_error=None
    try:cleanup_mysql(plane)
    except Exception as e:cleanup_error=e
    try:write(out/'resource-cleanup.json',graph.public_cleanup(plane.cleanup()))
    finally:
     if cleanup_error is not None:raise cleanup_error
  finally:
   checks={};failures={}
   if core_stage is not None:
    try:
     expected=set(original)|{'server/config/config.yaml'}
     checks['canonicalCoreSource']=(all(regular(core_stage/n)==body and stat.S_IMODE((core_stage/n).stat().st_mode)==g['sourceModes'].get(n,0o644) for n,body in original.items())
       and regular(core_stage/'server/config/config.yaml')==original['server/config/config.example.yaml']
       and {str(p.relative_to(core_stage)) for p in core_stage.rglob('*') if p.is_file() or p.is_symlink()}==expected)
    except BaseException as e:checks['canonicalCoreSource']=False;failures['canonicalCoreSource']=type(e).__name__
   for name,fn in [('source',lambda: all(regular(stage/n)==b and stat.S_IMODE((stage/n).stat().st_mode)==g['sourceModes'].get(n,0o644) for n,b in bodies.items()) and regular(stage/'server/config/config.yaml')==bodies['server/config/config.example.yaml']),('locks',lambda: all(regular(stage/'server'/n)==original['server/'+n] for n in ('go.mod','go.sum'))),('controllers',lambda:{p:sha(regular(REPO/p)) for p in c['controllers']}==controls),('tools',lambda:graph.tool_snapshot()==tools),('providers',lambda:verify()[0]==c)]:
    try:checks[name]=fn()
    except BaseException as e:checks[name]=False;failures[name]=type(e).__name__
   write(out/'post.json',{'exact':all(checks.values()),'checks':checks,'exceptions':failures,'coreProduct':core_source(c),'harness':c['derivedHarnessSource']})
   if not all(checks.values()) and primary is None:raise ValueError('original source/tool/controller post failed')
 return 0

def create_mysql(plane,policy,password):
 # This seventh DB container is fresh and owned alongside the original six,
 # but outside Plane's finite role enum. Exact separate owner is never adopted.
 image=policy['image'];meta=json.loads(plane.call(['image','inspect',image]))
 if len(meta)!=1:raise ValueError('one MySQL image required')
 resources=load('ci/backend-fullgraph/resources.py','p20_mysql_identity');resources.require_repo_digest(meta[0],image)
 if meta[0]['Id']!=policy['configDigest'] or (meta[0]['Config'].get('Entrypoint') or [])!=['docker-entrypoint.sh']:raise ValueError('MySQL actual image config differs')
 data=plane.root/'mysql-data';data.mkdir(mode=0o700);envfile=plane.private/'mysql.env';envfile.write_text('MYSQL_ROOT_PASSWORD='+password+'\nMYSQL_ROOT_HOST=127.0.0.1\n');envfile.chmod(0o600)
 cidfile=plane.private/'product20-mysql.cid';write(plane.private/'product20-mysql.json',{'nonce':plane.nonce,'uid':os.getuid(),'image':image,'rootPassword':password,'configDigest':policy['configDigest']})
 argv=['create','--pull=never','--name','luobo-product20-'+plane.nonce+'-mysql','--cidfile',str(cidfile),'--label','luobo.product20.owner='+plane.nonce,'--label','luobo.product20.role=mysql','--user',str(os.getuid())+':'+str(os.getgid()),'--cap-drop=ALL','--security-opt=no-new-privileges','--pids-limit=512','--network','container:'+plane.ids[0],'--mount',f'type=bind,src={data},dst=/var/lib/mysql','--tmpfs','/var/run/mysqld:rw,nosuid,nodev,noexec,size=16777216,mode=0700,uid='+str(os.getuid())+',gid='+str(os.getgid()),'--env-file',str(envfile),image,'mysqld','--bind-address=127.0.0.1','--port=3306','--character-set-server=utf8mb4','--collation-server=utf8mb4_unicode_ci','--socket=/var/run/mysqld/mysqld.sock','--pid-file=/var/run/mysqld/mysqld.pid','--tmpdir=/var/run/mysqld','--secure-file-priv=NULL','--log-error=/var/lib/mysql/task-error.log']
 plane.call(argv)
 cid=regular(cidfile,1024).decode().strip()
 if not re.fullmatch('[0-9a-f]{64}',cid):raise ValueError('original MySQL id absent')
 write(plane.private/'product20-mysql-owner.json',{'id':cid,'nonce':plane.nonce,'uid':os.getuid(),'imageID':meta[0]['Id'],'keeperID':plane.ids[0]})
 plane.call(['start',cid]);return cid

def cleanup_mysql(plane):
 intent=plane.private/'product20-mysql.json'
 if not intent.exists():return
 value=json.loads(regular(intent));name='luobo-product20-'+plane.nonce+'-mysql'
 if value['nonce']!=plane.nonce or value['uid']!=os.getuid():raise ValueError('original MySQL intent required')
 ids=plane.call(['ps','--all','--no-trunc','--filter','name=^/'+name+'$','--format','{{.ID}}']).decode().splitlines()
 if not ids:return
 if len(ids)!=1 or not re.fullmatch('[0-9a-f]{64}',ids[0]):raise ValueError('ambiguous MySQL owner')
 cid=ids[0];row=json.loads(plane.call(['inspect',cid]))[0];labels=row['Config']['Labels']
 if row['Id']!=cid or row['Name']!='/'+name or labels.get('luobo.product20.owner')!=plane.nonce or labels.get('luobo.product20.role')!='mysql' or row['Image']!=value['configDigest'] or row['HostConfig']['NetworkMode']!='container:'+plane.ids[0]:raise ValueError('foreign MySQL refused')
 if row['State']['Running']:plane.call(['stop','--time','30',cid],45)
 plane.call(['rm','--volumes',cid])
 if plane.call(['ps','--all','--no-trunc','--filter','id='+cid,'--format','{{.ID}}']).strip():raise ValueError('original MySQL absence unconfirmed')

STARTUP_AUX_STEPS=frozenset(('aux-key','aux-serial','aux-ca','aux-leaf','aux-tls','aux-ca-write','aux-trust-pool','aux-port-floor-read','aux-port-floor-parse','aux-port-floor-policy','aux-https-listen','aux-smtp-listen'))
STARTUP_CHART_STEPS=frozenset(('chart-template-read','chart-template-decode','chart-secret','chart-directory','chart-encode','chart-write'))
STARTUP_ROUTE_STEPS=frozenset(('route-secret','route-upstream-encode','route-migrate-start','route-migrate-wait','route-tenant-seed'))
STARTUP_CAUSE_CLASSES=frozenset(('other','exec-exit','syscall-errno','os-path','os-link','os-syscall','net-op','net-dns','number-parse','mysql','x509','yaml','json','context-canceled','context-deadline'))
def project_startup_boundary(v):
 o=v.get('startupBoundary')
 if not isinstance(o,dict) or set(o)!={'version','entered','substep','role','sixForked','sixReady','primaryCause'} or o['version']!='product20-startup-boundary-v1' or type(o['entered']) is not bool:raise ValueError('finite startup observation shape rejected')
 step,role=o['substep'],o['role']
 if type(step) is not str or type(role) is not str:raise ValueError('finite startup step and role required')
 roles={'none'} if step in ('not-entered','complete') else {'aux'} if step in STARTUP_AUX_STEPS else {'chart'} if step in STARTUP_CHART_STEPS else {'route-service'} if step in STARTUP_ROUTE_STEPS else set(NAMES) if step in ('product-start','product-wait-port') else set()
 if role not in roles or o['entered']!=(step!='not-entered'):raise ValueError('finite startup step and role rejected')
 if any(type(o[k]) is not int or not 0<=o[k]<=6 for k in ('sixForked','sixReady')) or o['sixReady']>o['sixForked']:raise ValueError('finite fork and ready counts rejected')
 if step=='not-entered' and (o['sixForked']!=0 or o['sixReady']!=0) or step=='complete' and (o['sixForked']!=6 or o['sixReady']!=6):raise ValueError('startup lifecycle counts differ')
 if step not in ('product-start','product-wait-port','complete') and (o['sixForked']!=0 or o['sixReady']!=0):raise ValueError('startup pre-fork counts differ')
 if o['entered'] and v['productProcesses']!=14+o['sixReady']:raise ValueError('original ready count differs')
 cause=o['primaryCause']
 if not isinstance(cause,dict) or set(cause)!={'present','classes','truncated','errno','mysqlNumber','execState','exitCode','signal'} or any(type(cause[k]) is not bool for k in ('present','truncated')):raise ValueError('finite startup cause shape rejected')
 classes=cause['classes']
 if type(classes) is not list or len(classes)>8 or any(type(k) is not str or k not in STARTUP_CAUSE_CLASSES for k in classes) or len(set(classes))!=len(classes) or cause['present']!=bool(classes):raise ValueError('finite startup cause classes rejected')
 if type(cause['errno']) is not int or not 0<=cause['errno']<=4095 or cause['errno'] and 'syscall-errno' not in classes:raise ValueError('finite startup errno rejected')
 if type(cause['mysqlNumber']) is not int or not 0<=cause['mysqlNumber']<=65535 or cause['mysqlNumber'] and 'mysql' not in classes:raise ValueError('finite startup mysql number rejected')
 state=cause['execState'];code=cause['exitCode'];signal=cause['signal']
 if type(state) is not str or state not in ('none','exited','signaled','other') or type(code) is not int or type(signal) is not int:raise ValueError('finite startup exec shape rejected')
 if state=='exited':valid=0<=code<=255 and signal==0
 elif state=='signaled':valid=code==-1 and 1<=signal<=64
 else:valid=code==-1 and signal==0
 if not valid or state!='none' and 'exec-exit' not in classes:raise ValueError('finite startup exec values rejected')
 if not cause['present'] and (cause['truncated'] or cause['errno']!=0 or cause['mysqlNumber']!=0 or state!='none') or step in ('not-entered','complete') and cause['present']:raise ValueError('startup primary failure boundary differs')
 cleanup=('auxiliaryListenersJoined','originalSixChildrenExited','originalCoreCleanupComplete')
 if any(type(v.get(k)) is not bool for k in cleanup):raise ValueError('original cleanup observations required')
 return {'version':o['version'],'entered':o['entered'],'substep':step,'role':role,'sixForked':o['sixForked'],'sixReady':o['sixReady'],'primaryCause':{k:cause[k] for k in ('present','classes','truncated','errno','mysqlNumber','execState','exitCode','signal')},'cleanup':{k:v[k] for k in cleanup},'diagnosticOnly':True,'primaryCauseCapturedBeforeCleanup':True,'physicalFirstFailureClaimed':False,'rawErrorTextExported':False,'privatePathsOrPIDsExported':False,'runtimeReadyP20Accepted':False}

HTTP_OPERATIONS=('config','verify','register','verify','register','login','login','self','self','wrong-password')
HTTP_DETAIL_CLASSES=frozenset((
 'none','route-default-unavailable','route-domain','route-upstream-unavailable',
 'route-upstream-http','route-upstream-envelope','route-registration-pending',
 'route-registration-recovered','route-registration-unconfigured',
))
def project_http_observation(v):
 if 'httpExchanges' not in v and 'configProxyStatus' not in v:return None
 rows=v.get('httpExchanges');proxy=v.get('configProxyStatus')
 scope=v.get('scope')
 operations=('verify','register')*BROWSER_SCOPES[scope][2] if scope in BROWSER_SCOPES else HTTP_OPERATIONS
 if type(rows) is not list or len(rows)>len(operations) or type(proxy) is not int or (proxy!=0 and (not rows or not 100<=proxy<=999)):raise ValueError('finite HTTP observation required')
 for operation,row in zip(operations,rows):
  if type(row) is not dict or set(row)!={'operation','outcome','status','appCode','detailClass'} or row['operation']!=operation:raise ValueError('finite original HTTP operation required')
  outcome,status,code,detail=(row[k] for k in ('outcome','status','appCode','detailClass'))
  if type(outcome) is not str or outcome not in ('request','transport','http','malformed','envelope') or type(status) is not int or type(detail) is not str or detail not in HTTP_DETAIL_CLASSES:raise ValueError('finite original HTTP values required')
  if outcome in ('request','transport'):valid=status==0
  elif outcome=='http':valid=100<=status<=999
  else:valid=status==200
  if not valid or code is not None and (outcome!='envelope' or type(code) is not int or not 0<=code<=0xffffffff) or detail!='none' and (outcome!='envelope' or code==0):raise ValueError('original HTTP outcome differs')
 return {'version':'product20-http-observation-v1','configProxyStatus':proxy,'exchanges':rows,'diagnosticOnly':True,'rawBodiesOrHeadersExported':False}

def project_registration_failure(v,http):
 if 'registrationFailure' not in v:
  return None
 if (v.get('accepted') is not False or v.get('phase')!='actual-route-config-and-registration'
     or http is None or not http['exchanges'] or http['exchanges'][-1]['operation']!='register'):
  raise ValueError('registration observation requires the original failed registration boundary')

 failure=v['registrationFailure']
 keys={'attempt','routeState','chartState','chartReason','imProvisioned','runScopedHops','hopsTruncated'}
 if type(failure) is not dict or set(failure)!=keys:
  raise ValueError('finite registration observation shape required')
 attempt,completed=failure['attempt'],v.get('realRegistrations')
 if (type(attempt) is not int or attempt not in (1,2) or type(completed) is not int
     or attempt!=completed+1 or attempt!=sum(row['operation']=='register' for row in http['exchanges'])):
  raise ValueError('registration attempt differs from the original count')

 states={
  'routeState':('unavailable','missing','reserved','confirmed','bound','other'),
  'chartState':('not-checked','unavailable','missing','receipt-only','awaiting_im','pending','conflict','direct_only','bound','other'),
  'chartReason':('none','other','defaults_unconfirmed','payload_integrity','unknown_creation_source','identity_unconfirmed','identity_changed','public_receipt_unconfirmed','im_owner_unconfirmed','delivery_unconfirmed'),
 }
 if any(type(failure[key]) is not str or failure[key] not in values for key,values in states.items()):
  raise ValueError('finite registration state and reason required')
 if any(type(failure[key]) is not bool for key in ('imProvisioned','hopsTruncated')):
  raise ValueError('finite registration flags required')

 hops=failure['runScopedHops']
 if type(hops) is not list or len(hops)>16:
  raise ValueError('bounded registration hops required')
 allowed_hops=('chart-register','chart-receipt','core-token','core-user-read','core-user-create')
 for row in hops:
  if type(row) is not dict or set(row)!={'hop','status','appCode'}:
   raise ValueError('finite registration hop shape required')
  hop,status,code=row['hop'],row['status'],row['appCode']
  if type(hop) is not str or hop not in allowed_hops or type(status) is not int or not 100<=status<=999:
   raise ValueError('finite registration hop and HTTP status required')
  if code is not None and (type(code) is not int or not 0<=code<=0xffffffff):
   raise ValueError('finite registration application code required')

 return {'version':'product20-registration-observation-v1',**failure,
         'diagnosticOnly':True,'rawBodiesHeadersOrIdentifiersExported':False}

def project_message_observation(v):
 if 'messageObservation' not in v:return None
 phases=('actual-gateway-ordinary-message-once','ordinary-peer-history-accepted-local-cleanup-required')
 if v.get('phase') not in phases:
  raise ValueError('message observation requires the original message boundary')
 observation=v['messageObservation']
 keys={'step','ackAccepted','peerAccepted','historyAccepted','readOutcome','readTimeout','readCloseCode'}
 if type(observation) is not dict or set(observation)!=keys:
  raise ValueError('finite message observation shape required')
 step=observation['step'];outcome=observation['readOutcome'];code=observation['readCloseCode']
 steps=('sender-handshake','peer-handshake','send-write','ack-read','peer-read','history-write','history-read','complete')
 if type(step) is not str or step not in steps or type(outcome) is not str or outcome not in ('none','read-error','nonbinary','decode-error'):
  raise ValueError('finite message step and read outcome required')
 if any(type(observation[key]) is not bool for key in ('ackAccepted','peerAccepted','historyAccepted','readTimeout')):
  raise ValueError('finite message flags required')
 if type(code) is not int or code!=0 and not 1000<=code<=4999:
  raise ValueError('finite message close code required')
 ack,peer,history=(observation[key] for key in ('ackAccepted','peerAccepted','historyAccepted'))
 if history and not peer or peer and not ack:
  raise ValueError('message observations must preserve completion order')
 if (step in ('sender-handshake','peer-handshake','send-write') and (ack or peer or history)
     or step=='ack-read' and (peer or history)
     or step=='peer-read' and (not ack or history)
     or step in ('history-write','history-read') and not (ack and peer)
     or step=='complete' and not (ack and peer and history)):
  raise ValueError('message completion flags differ from the original step')
 if (v.get('accepted') is True or v['phase']==phases[1]) and step!='complete':
  raise ValueError('completed result requires completed message observation')
 if outcome!='read-error' and (observation['readTimeout'] or code!=0) or observation['readTimeout'] and code!=0:
  raise ValueError('message read error values differ')
 if outcome!='none' and step not in ('ack-read','peer-read','history-read'):
  raise ValueError('read outcomes require an original read step')
 count=v.get('ordinaryDispatches')
 if (type(count) is not int or count not in (0,1)
     or step in ('sender-handshake','peer-handshake') and count!=0
     or step not in ('sender-handshake','peer-handshake','send-write') and count!=1):
  raise ValueError('message observation differs from the original dispatch count')
 return {'version':'product20-message-observation-v1',**observation,
         'diagnosticOnly':True,'rawBodiesHeadersOrIdentifiersExported':False}

def project_post_ack_snapshots(v,message):
 names=('gatewayPresence','deliverySnapshot','managedDrain')
 if not any(key in v for key in names):return {}
 if ('gatewayPresence' in v or 'deliverySnapshot' in v) and (
     v.get('accepted') is not False or v.get('phase')!='actual-gateway-ordinary-message-once'
     or message is None or message.get('step')!='peer-read'
     or message.get('ackAccepted') is not True or message.get('peerAccepted') is not False
     or message.get('historyAccepted') is not False
     or type(v.get('ordinaryDispatches')) is not int or v['ordinaryDispatches']!=1):
  raise ValueError('post-ACK snapshots require the original failed peer-read boundary')
 if 'gatewayPresence' in v:
  presence=v['gatewayPresence']
  if type(presence) is not dict or set(presence)!={'available','senderOnline','peerOnline'} or any(type(presence[key]) is not bool for key in presence):
   raise ValueError('finite gateway presence shape required')
  if not presence['available'] and (presence['senderOnline'] or presence['peerOnline']):
   raise ValueError('unavailable gateway presence must not claim online accounts')
 if 'deliverySnapshot' in v:
  delivery=v['deliverySnapshot']
  flags=('kafkaReceipt','sourceReceipt','maxPending','senderReadPending','onlinePending','pushPending','onlineUnconfirmed','peerDenied')
  keys={'state','dutyState','onlineAttempts','attemptsTruncated',*flags}
  if type(delivery) is not dict or set(delivery)!=keys:
   raise ValueError('finite delivery snapshot shape required')
  for key,allowed in (('state',('unavailable','absent','ambiguous','observed')),('dutyState',('unknown','ready','done','other'))):
   if type(delivery[key]) is not str or delivery[key] not in allowed:
    raise ValueError('finite delivery snapshot states required')
  if any(delivery[key] is not None and type(delivery[key]) is not bool for key in flags):
   raise ValueError('nullable delivery flags required')
  attempts=delivery['onlineAttempts'];truncated=delivery['attemptsTruncated']
  if attempts is not None and (type(attempts) is not int or not 0<=attempts<=255):
   raise ValueError('bounded nullable online attempt count required')
  if type(truncated) is not bool or truncated and attempts!=255:
   raise ValueError('bounded delivery truncation required')
  if delivery['state']!='observed' and (delivery['dutyState']!='unknown' or any(delivery[key] is not None for key in (*flags,'onlineAttempts')) or truncated):
   raise ValueError('unobserved delivery must not claim observations')
 if 'managedDrain' in v:
  drain=v['managedDrain']
  if 'deliverySnapshot' not in v or v.get('originalCoreCleanupComplete') is not True:
   raise ValueError('managed drain requires delivery snapshot and original core cleanup')
  if type(drain) is not dict or set(drain)!={'available','sourceOutcomeUnconfirmed'} or any(type(drain[key]) is not bool for key in drain):
   raise ValueError('finite managed drain shape required')
  if not drain['available'] and drain['sourceOutcomeUnconfirmed']:
   raise ValueError('unavailable managed drain must not claim an outcome')
 versions=('product20-gateway-presence-v1','product20-delivery-snapshot-v1','product20-managed-drain-v1')
 return {key:{'version':version,**v[key],'diagnosticOnly':True,'rawBodiesHeadersOrIdentifiersExported':False}
         for key,version in zip(names,versions) if key in v}

def export_terminal(path,output):
 raw=regular(path,128<<10);v=json.loads(raw)
 phases={'fresh-core-prepare','fresh-sql-and-six-real-services','actual-route-config-and-registration','actual-route-password-login-dual-token','actual-gateway-ordinary-message-once','ordinary-peer-history-accepted-local-cleanup-required'}
 phases.update(phase for plan in BROWSER_SCOPES.values() for phase in plan[:2])
 if v.get('version')!='fresh-product20-http-ws-result-v1' or v.get('phase') not in phases or type(v.get('accepted')) is not bool or any(x and not re.fullmatch('[0-9a-f]{64}',x) for x in (v.get('failureSHA256',''),v.get('coreSourceSHA256',''))):raise ValueError('finite original terminal shape rejected')
 keys=('productProcesses','realRegistrations','realPasswordLogins','ordinaryDispatches','peerMessages','historyMessages')
 if any(type(v.get(k)) is not int or not 0<=v[k]<=20 for k in keys):raise ValueError('finite original terminal counts rejected')
 value={'version':v['version'],'accepted':v['accepted'],'phase':v['phase'],'failureSHA256':v.get('failureSHA256',''),'coreSourceSHA256':v.get('coreSourceSHA256',''),'originalResultSHA256':sha(raw),'counts':{k:v[k] for k in keys},'startupBoundary':project_startup_boundary(v),'rawLogsOrDataExported':False}
 http=project_http_observation(v)
 if http is not None:value['httpObservation']=http
 registration=project_registration_failure(v,http)
 if registration is not None:value['registrationFailure']=registration
 message=project_message_observation(v)
 if message is not None:value['messageObservation']=message
 value.update(project_post_ack_snapshots(v,message))
 if v.get('scope') in BROWSER_SCOPES:
  value['scope']=v['scope']
 if 'browser' in v:
  value['browser']=browser_result(v)
  value['browserObservation']=browser_observation(v['browser']['observation'])
  if v['scope']=='web-chat':
   value['browserInteraction']=browser_interaction(v['browser']['interaction'])
 if 'coreStopFailure' in v:
  value['coreStopFailure']=project_browser_core_stop(v)
 write(output,value)
 return value

BROWSER_SCOPES={
 'web-login':('actual-web-browser-login','web-browser-login-accepted-local-cleanup-required',1),
 'web-chat':('actual-web-browser-chat','web-browser-chat-accepted-local-cleanup-required',2),
}
BROWSER_RESULT_KEYS={'configAccepted','loginAccepted','gatewayConnected','selfAccepted','workspaceVisible'}
BROWSER_INTERACTION_KEYS={'peerReady','conversationOpened','senderToPeer','peerToSender','logoutRevoked',
                          'logoutSocketClosed','loginRestored','oldChatTokenDenied','oldIMTokenDenied','peerStillConnected'}
BROWSER_CHAT_STAGES={'peer-login','contact-search','contact-card','conversation-ready','compose',
                     'send-ack','peer-receive','logout','logout-token-check'}
BROWSER_STAGES={'unavailable','input','tls-leaf','chrome-start','devtools','navigation','page-form',
                'configuration','sdk-ready','login-form','workspace','cleanup','complete'}

def browser_observation(value):
 keys={'stage','documentStatus','configStatus','configRequested','chromeFailure'}
 if (type(value) is not dict or set(value)!=keys or type(value['stage']) is not str or value['stage'] not in BROWSER_STAGES|BROWSER_CHAT_STAGES
     or type(value['chromeFailure']) is not str or value['chromeFailure'] not in ('none','sandbox','shared-library','executable')
     or type(value['configRequested']) is not bool
     or any(type(value[key]) is not int or not (value[key]==0 or 100<=value[key]<=599) for key in ('documentStatus','configStatus'))):
  raise ValueError('finite browser observation required')
 return value

def browser_result(v):
 result=v.get('browser')
 scope=v.get('scope')
 expected=BROWSER_RESULT_KEYS|({'observation','interaction'} if scope=='web-chat' else {'observation'})
 if (scope not in BROWSER_SCOPES or v.get('phase') not in BROWSER_SCOPES[scope][:2]
     or type(result) is not dict or set(result)!=expected
     or any(type(result[key]) is not bool for key in BROWSER_RESULT_KEYS)):
  raise ValueError('finite browser result required')
 stage=browser_observation(result['observation'])['stage']
 if scope=='web-chat':
  browser_interaction(result['interaction'])
 elif stage in BROWSER_CHAT_STAGES:
  raise ValueError('browser chat stage requires its own scope')
 return {key:value for key,value in result.items() if key in BROWSER_RESULT_KEYS}

def browser_interaction(value):
 if (type(value) is not dict or set(value)!=BROWSER_INTERACTION_KEYS
     or any(type(flag) is not bool for flag in value.values())):
  raise ValueError('finite browser interaction required')
 return value

def browser_complete(v):
 scope=v.get('scope')
 return (all(browser_result(v).values()) and v['phase']==BROWSER_SCOPES[scope][1]
         and v['browser']['observation']['stage']=='complete'
         and (scope!='web-chat' or all(v['browser']['interaction'].values())))

CORE_STOP_ROLES=frozenset(('api','crontask','msggateway','msgtransfer-managed','push','rpc-auth',
 'rpc-conversation','rpc-encryption','rpc-friend','rpc-group','rpc-msg','rpc-rtc','rpc-third','rpc-user'))
GROUP_STOP_HINTS={'group-managed-return-panic','group-redis-source-unconfirmed'}
CORE_STOP_LOG_HINTS={'gateway-lifecycle','managed-ordinary-shutdown','redis-permission','panic'}|GROUP_STOP_HINTS

def project_browser_core_stop(v):
 value=v['coreStopFailure']
 if (v.get('accepted') is not False or not browser_complete(v)
     or type(value) is not dict or set(value)!={'sessionError','bootstrapError','rows'}
     or any(type(value[k]) is not bool for k in ('sessionError','bootstrapError'))
     or not (value['sessionError'] or value['bootstrapError'])
     or type(value['rows']) is not list or len(value['rows'])>len(CORE_STOP_ROLES)
     or (value['rows'] and not value['sessionError'])):
  raise ValueError('finite original browser core stop failure required')
 seen=set()
 for row in value['rows']:
  if (type(row) is not dict or set(row)!={'role','outcome','waitState','exitCode','logHints'}
      or type(row['role']) is not str or row['role'] not in CORE_STOP_ROLES or row['role'] in seen
      or row['outcome'] not in ('unconfirmed-child-stop','deadline','canceled')
      or row['waitState'] not in ('clean','failed','unknown')
      or (row['exitCode'] is not None and (type(row['exitCode']) is not int or not 0<=row['exitCode']<=255))
      or type(row['logHints']) is not list or len(row['logHints'])>len(CORE_STOP_LOG_HINTS)
      or any(type(label) is not str or label not in CORE_STOP_LOG_HINTS for label in row['logHints'])
      or (row['role']!='rpc-group' and GROUP_STOP_HINTS.intersection(row['logHints']))
      or ('group-managed-return-panic' in row['logHints'] and 'panic' not in row['logHints'])
      or len(set(row['logHints']))!=len(row['logHints']) or (row['logHints'] and row['waitState']!='failed')):
   raise ValueError('closed original stop role required')
  seen.add(row['role'])
 return {'version':'product20-core-stop-observation-v1',**value,'diagnosticOnly':True,'rawLogsOrIdentitiesExported':False}

def check_result(v,c):
 common=('accepted','businessPermissions','freshSQLSeedOnly','auxiliaryListenersJoined','originalSixChildrenExited','originalCoreCleanupComplete')
 if (v.get('version')!='fresh-product20-http-ws-result-v1' or any(v.get(k) is not True for k in common)
     or v.get('coreSourceSHA256')!=core_source(c)['digest'] or v.get('productProcesses')!=20
     or any(v.get(k) is not False for k in ('chartCanonicalDrainClaimed','deploymentAccepted'))):
  raise ValueError('original live services or cleanup incomplete')
 scope=acceptance_scope()
 if v.get('scope','backend')!=scope:
  raise ValueError('original runtime scope differs')
 if scope in BROWSER_SCOPES:
  if (v.get('webUIAccepted') is not True or not browser_complete(v) or v.get('realRegistrations')!=BROWSER_SCOPES[scope][2]
      or any(v.get(k)!=0 for k in ('realPasswordLogins','ordinaryDispatches','peerMessages','historyMessages'))):
   raise ValueError('actual browser scope incomplete')
 else:
  flags=('dualTokens','chatTokenSelfRead','configHTTP','wrongPasswordRejected')
  if (any(v.get(k) is not True for k in flags) or v.get('realRegistrations')!=2 or v.get('realPasswordLogins')!=2
      or v.get('ordinaryDispatches')!=1 or v.get('peerMessages')!=1 or v.get('historyMessages')!=1
      or v.get('messageSeq',0)<=0 or v.get('webUIAccepted') is not False or 'browser' in v):
   raise ValueError('actual HTTP+WS result incomplete')
 pids=v.get('coreOriginalPIDs',[])+v.get('sixOriginalPIDs',[])
 if len(v.get('coreOriginalPIDs',[]))!=14 or len(v.get('sixOriginalPIDs',[]))!=6 or len(set(pids))!=20 or any(type(p) is not int or p<=1 or Path('/proc',str(p)).exists() for p in pids):raise ValueError('all original twenty process exits required')

def collect():
 # Always inspection/cleanup, never promotes an unknown runtime to canonical.
 c,graph,*_=verify();root=rootdir();out=outdir();out.mkdir(mode=0o700,parents=True,exist_ok=True)
 if not root.exists():write(out/'collection.json',{'accepted':False,'started':False});return 1
 owner=json.loads(regular(root/'private/owner.json'))
 if owner!={'run':os.environ['GITHUB_RUN_ID'],'attempt':os.environ['GITHUB_RUN_ATTEMPT'],'head':os.environ['GITHUB_SHA'],'uid':os.getuid()}:raise ValueError('original allocation owner required')
 errors=[];resources=load('ci/backend-fullgraph/resources.py','p20_always_cleanup')
 if (root/'plane/private/owner.json').exists():
  plane=resources.Plane.reopen(root/'plane')
  for name,call in [('mysql',lambda:cleanup_mysql(plane)),('original-six',plane.cleanup)]:
   try:call()
   except Exception as e:errors.append(name+':'+type(e).__name__)
 result_ok=False
 try:check_result(json.loads(regular(root/'chain/product20-result.json',128<<10)),c);result_ok=True
 except Exception as e:errors.append('original-process-result:'+type(e).__name__)
 post=json.loads(regular(out/'post.json')) if (out/'post.json').exists() else {}
 controller_path=REPO/'ci-results/product20-controller.resource.json';controller=json.loads(regular(controller_path)) if controller_path.exists() else {}
 accepted=not errors and result_ok and post.get('exact') is True and controller.get('status')=='passed' and controller.get('exit_code')==0
 scope=acceptance_scope()
 write(out/'collection.json',{'accepted':accepted,'finiteFailures':errors,'actualHTTPWS':result_ok and scope=='backend','canonicalChartDrainClaimed':False,'webUIAccepted':result_ok and scope in BROWSER_SCOPES,'deploymentAccepted':False})
 return 0 if accepted else 1
if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--verify',action='store_true');p.add_argument('--collect',action='store_true');p.add_argument('--admit',action='store_true');a=p.parse_args()
 if a.verify:
  c,*_=verify();print(json.dumps({'sourceOnly':True,'executionLocked':c.get('executionBinding') is None,'core':core_source(c),'harness':c['derivedHarnessSource'],'productRebuilds':c['productRebuilds']}));sys.exit(0)
 if a.admit:sys.exit(admit())
 if a.collect:sys.exit(collect())
 sys.exit(run())
