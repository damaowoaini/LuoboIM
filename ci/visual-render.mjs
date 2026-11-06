// One manual CI renderer: accepted dist47 + canonical App/portable + one test.
// No Playwright, browser download, product mock mode, APK or Web rebuild.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import http from 'node:http';
import {spawn, spawnSync} from 'node:child_process';
import {fileURLToPath, pathToFileURL} from 'node:url';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const RENDER_MODE = process.env.UI_RENDER_MODE || 'all3';
const INPUT = path.join(REPO, RENDER_MODE === 'web2' ? 'ci/visual-render/web2.inputs.json' : 'ci/visual-render/inputs.json');
const rawInput = fs.readFileSync(INPUT);
const cfg = JSON.parse(rawInput);
const CHROME = '/opt/google/chrome/chrome';
const fail = label => { const error = new Error(label); error.uiClosedLabel = true; throw error; };
const check = (value, label) => { if (!value) fail(label); };
const sha = body => crypto.createHash('sha256').update(body).digest('hex');
const canonical = value => JSON.stringify(value, (_, x) => x && !Array.isArray(x) && typeof x === 'object'
  ? Object.fromEntries(Object.keys(x).sort().map(k => [k, x[k]])) : x);
const digest = rows => sha(Buffer.from(canonical([...rows].sort((a,b) => Buffer.compare(Buffer.from(a.path),Buffer.from(b.path))))));
const write = (file, value) => fs.writeFileSync(file, JSON.stringify(value, null, 2)+'\n', {flag:'wx', mode:0o600});
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const safe = name => typeof name === 'string' && name !== '' && !name.startsWith('/')
  && name.split('/').every(p => p !== '' && p !== '.' && p !== '..') && !/[\\\0]/.test(name);
function pin(file, max = 64 << 20, executable = false) {
  check(path.isAbsolute(file) && fs.realpathSync(file) === file, 'canonical regular input required');
  const fd = fs.openSync(file, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW);
  try {
    const first = fs.fstatSync(fd, {bigint:true});
    const identity = s => Object.fromEntries(['dev','ino','mode','uid','gid','nlink','size','mtimeNs','ctimeNs'].map(k => [k,String(s[k])]));
    check(first.isFile() && first.size >= 0n && first.size <= BigInt(max), 'bounded regular input required');
    if (executable) check((Number(first.mode) & 0o111) !== 0 && (Number(first.mode) & 0o022) === 0
      && [0,process.getuid()].includes(Number(first.uid)), 'trusted installed executable required');
    const hash = crypto.createHash('sha256'), chunk = Buffer.alloc(1 << 20);
    let bytes = 0, header = Buffer.alloc(0);
    for (;;) {
      const count = fs.readSync(fd, chunk, 0, chunk.length, null);
      if (!count) break;
      if (header.length < 64) header = Buffer.concat([header,chunk.subarray(0,Math.min(count,64-header.length))]);
      hash.update(chunk.subarray(0,count)); bytes += count;
      check(bytes <= max, 'input grew beyond bound');
    }
    const after = fs.fstatSync(fd,{bigint:true}), current = fs.lstatSync(file,{bigint:true});
    check(bytes === Number(first.size) && canonical(identity(first)) === canonical(identity(after))
      && canonical(identity(first)) === canonical(identity(current)) && fs.realpathSync(file) === file, 'input changed during pin');
    return {path:file,bytes,sha256:hash.digest('hex'),identity:identity(first),header:header.toString('hex')};
  } finally { fs.closeSync(fd); }
}
function rowPin(root, row) {
  check(safe(row.path), 'unsafe source row');
  const value = pin(path.join(root,row.path));
  check(value.bytes === row.bytes && value.sha256 === row.sha256, 'source/input body differs');
  return value;
}
function command(binary, args, options = {}) {
  const result = spawnSync(binary,args,{timeout:options.timeout ?? 30000,maxBuffer:2<<20,
    encoding:'utf8',cwd:options.cwd ?? REPO,env:options.env ?? process.env});
  check(!result.error && result.status === 0, 'bounded owned command failed');
  return result.stdout;
}
function rootArg(value) {
  const expected = path.join(fs.realpathSync(process.env.RUNNER_TEMP),`ui-render-${process.env.GITHUB_RUN_ID}-${process.env.GITHUB_RUN_ATTEMPT}`);
  check(value === expected && !value.startsWith(REPO+'/'), 'fixed fresh runner allocation required');
  return value;
}
function descriptors() {
  for (const row of cfg.inputPins) rowPin(REPO,row);
  for (const row of cfg.controllerPins) rowPin(REPO,row);
  const native = JSON.parse(fs.readFileSync(path.join(REPO,cfg.nativeInput)));
  const mobile = JSON.parse(fs.readFileSync(path.join(REPO,cfg.mobileInput)));
  const compatibility = RENDER_MODE === 'all3' ? JSON.parse(fs.readFileSync(path.join(REPO,cfg.compatibilityInput))) : null;
  const app = native.files.filter(r => r.path.startsWith('app/lib/') || r.path.startsWith('app/test/')
    || r.path.startsWith('app/tool/') || ['app/analysis_options.yaml','app/pubspec.yaml','app/pubspec.lock'].includes(r.path));
  const helper = native.files.find(r => r.path === 'migration/legacy_personal_export/legacy_personal_export.dart');
  const portable = [...app,...mobile.sdk_portable.files,helper];
  check(app.length === 366 && digest(app) === cfg.canonical.app.digest && portable.length === 418
    && new Set(portable.map(r=>r.path)).size === 418 && digest(portable) === cfg.canonical.portable.digest, 'canonical366/418 metadata differs');
  if (RENDER_MODE === 'web2') {
    check(cfg.web.sourceInputPin.path === cfg.web.sourceInput, 'exact new Web source descriptor required');
    rowPin(REPO,cfg.web.sourceInputPin);
    return {app,portable,web:JSON.parse(fs.readFileSync(path.join(REPO,cfg.web.sourceInput)))};
  }
  const old = new Map(portable.map(r=>[r.path,r]));
  for (const delta of compatibility.source_delta) delta.before === null ? old.delete(delta.path) : old.set(delta.path,delta.before);
  check(old.size === 415 && digest([...old.values()]) === cfg.originalSetup.portable.digest, 'original415 successor boundary differs');
  return {app,portable,old:[...old.values()],web:JSON.parse(fs.readFileSync(path.join(REPO,cfg.web.sourceInput)))};
}
function sourceAdmission() {
  const d = descriptors();
  for (const row of d.portable) rowPin(REPO,row);
  const members = cfg.web.source.members;
  check(Number.isSafeInteger(members) && members > 0 && members <= 8192 && d.web.files.length === members
    && d.web.sourceFiles === members && new Set(d.web.files.map(r=>r.path)).size === members
    && d.web.sourceDigest === cfg.web.source.digest, 'exact configured Web source descriptor differs');
  const sourceDigest = sha(Buffer.concat([...d.web.files].sort((a,b)=>Buffer.compare(Buffer.from(a.path),Buffer.from(b.path)))
    .map(r=>Buffer.from(r.path+'\0'+r.sha256+'\n'))));
  check(sourceDigest === cfg.web.source.digest, 'whole current Web source vector differs');
  for (const row of d.web.files) rowPin(path.join(REPO,'web'),row);
  check(command('/usr/bin/git',['rev-parse','HEAD']).trim() === process.env.GITHUB_SHA, 'event checkout identity differs');
  return d;
}
function chromePin() {
  check(fs.existsSync(CHROME), 'installed Chrome missing; browser download refused');
  const value = pin(CHROME,1<<30,true), h = Buffer.from(value.header,'hex');
  check(h.length === 64 && h.subarray(0,7).equals(Buffer.from([127,69,76,70,2,1,1]))
    && h.readUInt16LE(18) === 62, 'installed Linux64 Chrome required');
  return value;
}
function installedTools() {
  check(process.version === 'v24.21.0' && process.getuid() !== 0, 'fixed ordinary-user Node24.21.0 required');
  const cjk = cfg.cjkPaths.find(p => fs.existsSync(p));
  check(cjk, 'installed CJK font missing; no font download or Ahem fallback');
  const font = pin(cjk,64<<20), h = Buffer.from(font.header,'hex').subarray(0,4).toString('hex');
  check(['74746366','4f54544f','00010000'].includes(h), 'real installed CJK font container required');
  if (RENDER_MODE === 'web2') check(cjk === cfg.cjkBinding.path && font.bytes === cfg.cjkBinding.bytes
    && font.sha256 === cfg.cjkBinding.sha256, 'exact reviewed historical/current CJK binding required');
  return {node:pin(fs.realpathSync(process.execPath),256<<20,true),nodeVersion:process.version,
    chrome:chromePin(),chromeVersion:command(CHROME,['--version']).trim(),cjk:font};
}
function unchanged(actual, expected) { check(canonical(actual) === canonical(expected), 'original tools/fonts changed'); }
function packageMap(work) {
  const file = path.join(work,'source/app/.dart_tool/package_config.json');
  const data = JSON.parse(fs.readFileSync(file)), base = pathToFileURL(file);
  const roots = ['flutter','pub-cache','source/app','source/packages/flutter_openim_sdk'].map(p=>path.join(work,p));
  for (const p of data.packages) {
    const url = new URL(p.rootUri,base); check(url.protocol === 'file:', 'external package URI refused');
    const root = fs.realpathSync(fileURLToPath(url));
    check(roots.some(p=>root === p || root.startsWith(p+'/')), 'package root outside original allocation');
    if (p.name === 'imapp') check(root === path.join(work,'source/app'), 'wrong imapp root');
    if (p.name === 'flutter_openim_sdk') check(root === path.join(work,'source/packages/flutter_openim_sdk'), 'wrong SDK root');
  }
  check(data.packages.filter(p=>p.name==='imapp').length===1 && data.packages.filter(p=>p.name==='flutter_openim_sdk').length===1,'required package identities absent');
  return pin(file,1<<20);
}
function authored(root) {
  const names=[];
  function walk(dir) { for (const e of fs.readdirSync(dir,{withFileTypes:true})) {
    const f=path.join(dir,e.name); check(!e.isSymbolicLink(),'authored source alias refused');
    if (e.isDirectory()) walk(f); else if(e.isFile()) names.push(path.relative(root,f)); else fail('unexpected authored source type');
  }}
  for (const name of ['app/lib','app/test','app/tool']) if(fs.existsSync(path.join(root,name))) walk(path.join(root,name));
  return names.sort();
}
function projectionAdmission(work, rows) {
  for(const row of rows) rowPin(path.join(work,'source'),row);
  const expected=rows.filter(r=>r.path.startsWith('app/lib/')||r.path.startsWith('app/test/')||r.path.startsWith('app/tool/')).map(r=>r.path).sort();
  check(canonical(authored(path.join(work,'source')))===canonical(expected),'derived authored inventory differs');
}
function webInventory() {
  const counts = RENDER_MODE === 'all3' ? {distFiles:47,metadataFiles:2,totalFiles:49} : cfg.web.inventory;
  check(counts && Number.isSafeInteger(counts.distFiles) && counts.distFiles > 0 && counts.distFiles <= 128
    && counts.metadataFiles === 2 && counts.totalFiles === counts.distFiles + 2, 'exact bounded Web inventory required');
  const rows = [...cfg.web.distRows,...cfg.web.metadataRows];
  check(cfg.web.distRows.length === counts.distFiles && cfg.web.metadataRows.length === counts.metadataFiles
    && new Set(rows.map(r=>r.path)).size === counts.totalFiles
    && canonical(cfg.web.metadataRows.map(r=>r.path).sort()) === canonical(['dist-manifest.json','dist-proof.json'])
    && rows.every(r=>safe(r.path) && Number.isSafeInteger(r.bytes) && r.bytes >= 0 && r.bytes <= (64<<20)
      && /^[0-9a-f]{64}$/.test(r.sha256)) && cfg.web.distRows.every(r=>r.path.startsWith('dist/')),
    'fixed declared Web paths and body pins required');
  return counts;
}
function appBaselineReference() {
  const baseline = cfg.historicalAppBaseline;
  check(baseline && baseline.kind === 'reviewed-historical-App-PNG-reference-v1'
    && canonical(baseline.canonical) === canonical(cfg.canonical)
    && Number.isSafeInteger(baseline.run) && baseline.run > 0 && /^[0-9a-f]{40}$/.test(baseline.head)
    && safe(baseline.rootReview.path) && /^[0-9a-f]{64}$/.test(baseline.rootReview.sha256)
    && baseline.PNG.path === '03-app-auth-375.png' && baseline.PNG.width === 375 && baseline.PNG.height === 812
    && baseline.PNG.bytes > 24 && baseline.PNG.bytes <= (4<<20) && /^[0-9a-f]{64}$/.test(baseline.PNG.sha256)
    && canonical(baseline.CJKFont) === canonical(cfg.cjkBinding), 'reviewed same-source App image reference required');
  return {...baseline,currentCanonicalAppAndPortableBodyAdmissionExact:true,renderedInCurrentRun:false,
    copiedIntoCurrentArtifact:false,FlutterSetups:0,WidgetCaptureTests:0,loginOrInteractionAccepted:false};
}
async function captureWeb(root,pre) {
  check(fs.readlinkSync('/proc/self/ns/net') !== process.env.UI_OUTSIDE_NETNS, 'capture requires isolated no-business-network namespace');
  sourceAdmission();webAdmission(root);const baseline=appBaselineReference(),counts=webInventory();
  let web,renderError,postError,postExact=false;
  try { web=await renderWeb(root); } catch(error) { renderError=error; }
  try {sourceAdmission();webAdmission(root);appBaselineReference();unchanged(installedTools(),pre.tools);postExact=true;}
  catch(error) {postError=error;}
  finally {write(path.join(root,'evidence/source-tools-post.json'),{renderMode:'web2',sourceToolsAndFontsExact:postExact,
    declaredWebBodies:counts.totalFiles,declaredWebBodiesPreExact:true,declaredWebBodiesPostExact:postExact,
    currentAppAndPortableBodiesUnchanged:postExact,rendererBodyIncluded:true,renderReturned:!renderError,
    FlutterSetups:0,WidgetCaptureTests:0,runtimeOrDesignAccepted:false});}
  if(renderError)throw renderError;if(postError)throw postError;
  write(path.join(root,'evidence/render-evidence.json'),{schema:1,renderMode:'web2',actualScreenshots:2,web,
    historicalAppReference:baseline,toolsBeforeAfter:pre.tools,currentSourceAndFontsPostExact:true,
    declaredWebBodiesPreAndPostExact:true,declaredWebBodies:counts.totalFiles,rendererBodyIncluded:true,
    FlutterSetups:0,WidgetCaptureTests:0,WebBuilds:0,APKBuilds:0,old34Or39Redispatched:0,productMockModes:0,
    screenshotsInspectedByAgent:false,interactionAuditAccepted:false,Web107Accepted:false,Mobile403Accepted:false,Admin89Accepted:false});
}
function webAdmission(root) {
  const rows=[...cfg.web.distRows,...cfg.web.metadataRows];
  const counts=webInventory();check(rows.length===counts.totalFiles,'fixed declared Web body inventory required');
  for(const row of rows)rowPin(path.join(root,'web'),row);
}
const ZIP_RECEIVER = String.raw`import sys,json,zipfile,hashlib,pathlib,stat
archive,out,config=map(pathlib.Path,sys.argv[1:])
descriptor=json.loads(config.read_bytes()); c=descriptor['web']; expected={r['path']:r for r in c['distRows']+c['metadataRows']}
assert descriptor['cohort'] in ('ui-render-evidence-v1','ui-render-web2-evidence-v1')
counts=c['inventory'] if descriptor['cohort']=='ui-render-web2-evidence-v1' else {'distFiles':47,'metadataFiles':2,'totalFiles':49}
assert counts['metadataFiles']==2 and counts['totalFiles']==counts['distFiles']+2
assert len(c['distRows'])==counts['distFiles'] and len(c['metadataRows'])==2 and len(expected)==counts['totalFiles'] and not out.exists()
with zipfile.ZipFile(archive) as z:
 rows=z.infolist(); assert len(rows)==counts['totalFiles'] and len({r.filename for r in rows})==counts['totalFiles'] and {r.filename for r in rows}==set(expected)
 assert sum(r.file_size for r in rows)==c['decodedBytes']
 for r in rows:
  assert not r.is_dir() and not r.flag_bits&1 and r.compress_type in (0,8)
  assert not stat.S_ISLNK(r.external_attr>>16) and r.file_size==expected[r.filename]['bytes']
 out.mkdir(mode=0o700)
 for r in rows:
  p=pathlib.PurePosixPath(r.filename); assert not p.is_absolute() and all(x not in ('','..','.') for x in p.parts)
  b=z.read(r); assert len(b)==expected[r.filename]['bytes'] and hashlib.sha256(b).hexdigest()==expected[r.filename]['sha256']
  f=out.joinpath(*p.parts); f.parent.mkdir(parents=True,exist_ok=True,mode=0o700)
  with f.open('xb') as s:s.write(b)
`;
async function responseBytes(response, max) {
  check(response.ok,'exact artifact HTTP response required'); const chunks=[]; let size=0;
  for await(const block of response.body){size+=block.length;check(size<=max,'artifact exceeds bound');chunks.push(block);}
  return Buffer.concat(chunks);
}
async function receive(root) {
  const counts=webInventory();
  const token=process.env.UI_ARTIFACT_TOKEN; delete process.env.UI_ARTIFACT_TOKEN;
  check(token,'ephemeral artifact-read token required');
  const headers={Authorization:`Bearer ${token}`,Accept:'application/vnd.github+json','User-Agent':'LuoboIM-owned-ui-renderer'};
  const url=`https://api.github.com/repos/damaowoaini/LuoboIM/actions/artifacts/${cfg.web.artifact.id}`;
  const meta=JSON.parse(await responseBytes(await fetch(url,{headers,signal:AbortSignal.timeout(25000)}),65536));
  check(meta.id===cfg.web.artifact.id && meta.workflow_run.id===cfg.web.artifact.run && meta.workflow_run.head_sha===cfg.web.artifact.head
    && meta.size_in_bytes===cfg.web.artifact.bytes && meta.digest===`sha256:${cfg.web.artifact.sha256}` && meta.expired===false,'original artifact API identity differs');
  let response=await fetch(url+'/zip',{headers,redirect:'manual',signal:AbortSignal.timeout(25000)});
  if([301,302,303,307].includes(response.status)) {
    const location=new URL(response.headers.get('location'));
    check(location.protocol==='https:' && !location.username && !location.password && (!location.port||location.port==='443')
      && ['.blob.core.windows.net','.actions.githubusercontent.com'].some(s=>location.hostname.endsWith(s)),'artifact redirect refused');
    response=await fetch(location,{redirect:'manual',signal:AbortSignal.timeout(25000)});
  }
  const body=await responseBytes(response,16<<20);
  check(body.length===cfg.web.artifact.bytes && sha(body)===cfg.web.artifact.sha256,'original Web ZIP bytes differ');
  const archive=path.join(root,'accepted-web-dist.zip');fs.writeFileSync(archive,body,{flag:'wx',mode:0o600});
  command('/usr/bin/python3',['-c',ZIP_RECEIVER,archive,path.join(root,'web'),INPUT]);
  if(RENDER_MODE==='all3') write(path.join(root,'evidence/web-receive.json'),{artifact:cfg.web.artifact,ZIPAndAll49BodiesChecked:true,distFiles:47,
    manifestSHA256:cfg.web.manifestSHA256,filesDigest:cfg.web.filesDigest,rebuilt:false,backendDeployed:false});
  else write(path.join(root,'evidence/web-receive.json'),{renderMode:'web2',artifact:cfg.web.artifact,ZIPAndAllDeclaredBodiesChecked:true,
    declaredInventory:counts,manifestSHA256:cfg.web.manifestSHA256,filesDigest:cfg.web.filesDigest,rebuilt:false,backendDeployed:false});
}
function prepare(root,legacy) {
  const d=sourceAdmission(),work=path.join(root,'mobile/work');
  check(command('/usr/bin/git',['-C',legacy,'rev-parse','HEAD']).trim()===cfg.originalSetup.head,'fixed original setup checkout required');
  for(const row of cfg.originalSetup.helperPins) rowPin(legacy,row);
  const receipt=JSON.parse(fs.readFileSync(path.join(work,'evidence/tag903-source.json')));
  check(canonical(receipt.source)===canonical(cfg.originalSetup.app) && canonical(receipt.portable)===canonical(cfg.originalSetup.portable),'original one-setup receipt differs');
  projectionAdmission(work,d.old);
  for(const name of ['app/pubspec.yaml','app/pubspec.lock']) {
    const before=d.old.find(r=>r.path===name),after=d.portable.find(r=>r.path===name);
    check(canonical(before)===canonical(after),'new dependency resolution would be required');
  }
  const originalMap=packageMap(work);
  for(const row of d.portable) {
    const f=path.join(work,'source',row.path);fs.mkdirSync(path.dirname(f),{recursive:true,mode:0o700});
    fs.writeFileSync(f,fs.readFileSync(path.join(REPO,row.path)),{mode:0o600});
  }
  const overlay=cfg.overlay, target=path.join(work,'source',overlay.target);
  check(!fs.existsSync(target),'capture overlay already exists');
  fs.mkdirSync(path.dirname(target),{recursive:true,mode:0o700});fs.copyFileSync(path.join(REPO,overlay.source),target,fs.constants.COPYFILE_EXCL);
  const rows=[...d.portable,{path:overlay.target,bytes:overlay.bytes,sha256:overlay.sha256}];
  check(digest(rows)===cfg.derived.portable.digest,'derived419 metadata differs');projectionAdmission(work,rows);unchanged(packageMap(work),originalMap);
  const version=JSON.parse(fs.readFileSync(path.join(work,'evidence/flutter-version.json')));
  for(const [k,v]of Object.entries(cfg.flutter))check(version[k]===v,'original exact Flutter/Dart SDK required');
  check(fs.existsSync(path.join(work,'flutter/bin/cache/artifacts/material_fonts/MaterialIcons-Regular.otf')), 'official SDK MaterialIcons font missing');
  const testerPath=path.join(work,'flutter/bin/cache/artifacts/engine/linux-x64/flutter_tester');
  check(fs.existsSync(testerPath),'official Flutter tester renderer missing');
  const tools={dart:pin(path.join(work,'flutter/bin/cache/dart-sdk/bin/dart'),64<<20,true),flutter:pin(path.join(work,'flutter/bin/flutter'),1<<20,true),
    flutterTester:pin(testerPath,512<<20,true),icons:pin(path.join(work,'flutter/bin/cache/artifacts/material_fonts/MaterialIcons-Regular.otf'),4<<20)};
  write(path.join(root,'evidence/mobile-projection.json'),{canonical:cfg.canonical,derived:cfg.derived,overlay,packageMap:originalMap,tools,
    originalSetupReceiptSHA256:sha(fs.readFileSync(path.join(work,'evidence/tag903-source.json'))),legacySetupHead:cfg.originalSetup.head,
    inheritedSetupCalls:1,newPubGetCalls:0,canonicalAppModified:false,APKBuild:false,fontOverride:'TEST_CAPTURE_FONT via existing captureTheme',runtimeAccepted:false});
}
class CDP {
  constructor(socket){this.socket=socket;this.id=0;this.pending=new Map();this.network=[];
    socket.addEventListener('message',event=>{const m=JSON.parse(event.data);if(m.id){const p=this.pending.get(m.id);if(p){clearTimeout(p.timer);this.pending.delete(m.id);m.error?p.reject(new Error('CDP command rejected')):p.resolve(m.result);}}
      if(m.method==='Network.responseReceived'&&this.network.length<128){const r=m.params.response;const u=new URL(r.url);this.network.push({path:['http:','https:'].includes(u.protocol)?u.pathname.slice(0,256):'[non-http-resource]',status:r.status,type:m.params.type});}});}
  call(method,params={}){return new Promise((resolve,reject)=>{const id=++this.id;const timer=setTimeout(()=>{this.pending.delete(id);reject(new Error('CDP deadline'));},15000);this.pending.set(id,{resolve,reject,timer});this.socket.send(JSON.stringify({id,method,params}));});}
  close(){this.socket.close();for(const p of this.pending.values()){clearTimeout(p.timer);p.reject(new Error('CDP closed'));}this.pending.clear();}
}
async function connect(url){const socket=new WebSocket(url);await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(new Error('CDP open deadline')),10000);socket.addEventListener('open',()=>{clearTimeout(timer);resolve();},{once:true});socket.addEventListener('error',()=>{clearTimeout(timer);reject(new Error('CDP open failed'));},{once:true});});return new CDP(socket);}
function png(file,w,h){const b=fs.readFileSync(file);check(b.length>24&&b.length<=(4<<20)&&b.subarray(0,8).equals(Buffer.from([137,80,78,71,13,10,26,10]))&&b.readUInt32BE(16)===w&&b.readUInt32BE(20)===h,'bounded exact-viewport PNG required');return {path:path.basename(file),bytes:b.length,sha256:sha(b),width:w,height:h};}
async function renderWeb(root) {
  const rows=new Map(cfg.web.distRows.map(r=>['/'+r.path.slice(5),r]));
  const server=http.createServer((req,res)=>{let key;try{key=decodeURIComponent(new URL(req.url,'http://127.0.0.1').pathname);}catch{res.writeHead(400).end();return;}
    if(key==='/')key='/index.html';const row=rows.get(key);if(!['GET','HEAD'].includes(req.method)||!row){res.writeHead(404).end();return;}
    res.setHeader('Cache-Control','no-store');res.setHeader('X-Content-Type-Options','nosniff');res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css','.wasm':'application/wasm','.json':'application/json','.svg':'image/svg+xml','.png':'image/png','.jpg':'image/jpeg','.webp':'image/webp','.woff2':'font/woff2','.woff':'font/woff','.ttf':'font/ttf','.otf':'font/otf'})[path.extname(key)]??'application/octet-stream');
    res.writeHead(200);if(req.method==='HEAD')res.end();else fs.createReadStream(path.join(root,'web',row.path)).pipe(res);});
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const base=`http://127.0.0.1:${server.address().port}`;
  const status=(await fetch(base+'/configuration')).status;check(status===404,'static configuration response must remain actual404');
  const profile=path.join(root,'chrome-profile');fs.mkdirSync(profile,{mode:0o700});
  const chrome=spawn(CHROME,['--headless=new','--remote-debugging-address=127.0.0.1','--remote-debugging-port=0','--user-data-dir='+profile,
    '--no-first-run','--no-default-browser-check','--disable-background-networking','--disable-component-update','--disable-sync'],{stdio:'ignore',env:{PATH:'/usr/bin:/bin',HOME:process.env.HOME,TMPDIR:path.join(root,'tmp')}});
  // Register once immediately. A signal exit has exitCode=null, so only this
  // original child's observed exit/error state determines subsequent cleanup.
  const chromeChild={closed:false,spawnError:false,signalError:false,exitCode:null,signal:null,originalChildHandleOnly:true};
  const chromeExit=new Promise(resolve=>{
    chrome.once('exit',(code,signal)=>{chromeChild.closed=true;chromeChild.exitCode=code;chromeChild.signal=signal;resolve();});
    chrome.on('error',()=>{if(!chrome.pid){chromeChild.spawnError=true;chromeChild.closed=true;resolve();}else chromeChild.signalError=true;});
  });
  const waitChrome=ms=>Promise.race([chromeExit.then(()=>true),sleep(ms).then(()=>false)]);
  const signalChrome=signal=>{if(!chromeChild.closed){try{chrome.kill(signal);}catch{chromeChild.signalError=true;}}};
  let port,endpoint;const end=Date.now()+15000;
  try {
    while(Date.now()<end&&!port){check(!chromeChild.closed,'installed Chrome could not start with its sandbox');const f=path.join(profile,'DevToolsActivePort');if(fs.existsSync(f)){const s=fs.readFileSync(f,'utf8').split('\n');if(/^\d+$/.test(s[0]))port=Number(s[0]);}if(!port)await sleep(100);}
    check(port>0&&port<65536,'owned Chrome debugging endpoint missing');endpoint=`http://127.0.0.1:${port}`;
    const images=[];
    for(const view of cfg.web.viewports){const page=await (await fetch(endpoint+'/json/new?about:blank',{method:'PUT'})).json();const u=new URL(page.webSocketDebuggerUrl);check(u.hostname==='127.0.0.1'&&Number(u.port)===port,'unexpected debugger endpoint');const c=await connect(u);
      try {await c.call('Page.enable');await c.call('Runtime.enable');await c.call('Network.enable');await c.call('Emulation.setDeviceMetricsOverride',{width:view.width,height:view.height,deviceScaleFactor:1,mobile:view.mobile});await c.call('Page.navigate',{url:base+'/#/login'});
        let state;const until=Date.now()+30000;
        while(Date.now()<until){const r=await c.call('Runtime.evaluate',{expression:'document.fonts.ready.then(()=>({ready:document.readyState,fonts:document.fonts.status,brand:document.body.innerText.includes("萝卜对话"),inputs:document.querySelectorAll("input").length,loading:document.querySelectorAll(".ant-spin-spinning").length,configWarning:document.body.innerText.includes("暂时无法获取服务配置")}))',awaitPromise:true,returnByValue:true});state=r.result?.value;if(state?.ready==='complete'&&state.fonts==='loaded'&&state.brand&&state.inputs>0&&state.loading===0&&state.configWarning)break;await sleep(250);}
        check(state?.ready==='complete'&&state.fonts==='loaded'&&state.brand&&state.inputs>0&&state.loading===0&&state.configWarning,'real login/config-unavailable UI did not reach a capturable state');await sleep(500);
        const shot=await c.call('Page.captureScreenshot',{format:'png',captureBeyondViewport:false,fromSurface:true});const f=path.join(root,'evidence',view.name+'.png');fs.writeFileSync(f,Buffer.from(shot.data,'base64'),{flag:'wx',mode:0o600});images.push({...png(f,view.width,view.height),viewport:view,textScale:1,initialFormOnly:true,configurationProbe:{path:'/configuration',status,staticServerOnly:true},configWarningVisible:state.configWarning,network:c.network});
      } finally {c.close();await fetch(endpoint+'/json/close/'+page.id).catch(()=>{});}}
    return {images,chromeChild,configurationProbe:{path:'/configuration',status,staticServerOnly:true},backendDeployed:false,noSuccessfulConfigInjected:true,interactiveAudit:false};
  } finally {
    signalChrome('SIGTERM');
    if(!chromeChild.closed&&!(await waitChrome(5000))){signalChrome('SIGKILL');if(!chromeChild.closed)await waitChrome(5000);}
    server.closeAllConnections();
    const serverClosed=await Promise.race([new Promise(resolve=>server.close(()=>resolve(true))),sleep(5000).then(()=>false)]);
    check(chromeChild.closed,'original Chrome exit not confirmed after bounded TERM/KILL');
    check(serverClosed,'owned static server close deadline');
  }
}
function mobileCapture(root,d,receipt) {
  const work=path.join(root,'mobile/work'),evidence=path.join(root,'evidence');
  const env={PATH:'/usr/bin:/bin',HOME:process.env.HOME,PUB_CACHE:path.join(work,'pub-cache'),TMPDIR:path.join(work,'tmp'),
    XDG_CONFIG_HOME:path.join(work,'config'),XDG_CACHE_HOME:path.join(work,'config/cache'),FLUTTER_SUPPRESS_ANALYTICS:'true',DART_SUPPRESS_ANALYTICS:'true',CI:'true'};
  const args=['test','--no-pub','--reporter=json','--concurrency=1','--timeout=60s','--dart-define=TEST_CAPTURE_DIR='+evidence,
    '--dart-define=TEST_CAPTURE_FONT='+receipt.cjk.path,'--dart-define=TEST_CAPTURE_ICONS='+path.join(work,'flutter/bin/cache/artifacts/material_fonts/MaterialIcons-Regular.otf'),
    '--plain-name='+cfg.overlay.testName,'test/visual/auth_capture_test.dart'];
  const result=spawnSync(path.join(work,'flutter/bin/flutter'),args,{cwd:path.join(work,'source/app'),env,timeout:120000,maxBuffer:2<<20,encoding:'utf8'});
  fs.writeFileSync(path.join(evidence,'app-widget-machine.log'),result.stdout??'',{flag:'wx',mode:0o600});
  check(!result.error&&result.status===0,'owned AuthPage capture test failed');
  const tests=new Map(),done=[];let finished=0;
  for(const line of result.stdout.split('\n').filter(Boolean)){const e=JSON.parse(line);if(e.type==='error')fail('widget machine error');if(e.type==='testStart')tests.set(e.test.id,e.test);if(e.type==='testDone'){const t=tests.get(e.testID);check(t&&e.result==='success'&&!e.skipped,'widget result was not successful');if(!e.hidden)done.push(t.name);}if(e.type==='done'){check(e.success===true,'widget suite did not finish');finished++;}}
  check(finished===1&&canonical(done)===canonical([cfg.overlay.testName]),'exact one capture test required; no skipped/unrelated test');
  const rows=[...d.portable,{path:cfg.overlay.target,bytes:cfg.overlay.bytes,sha256:cfg.overlay.sha256}];projectionAdmission(work,rows);
  return {...png(path.join(evidence,'03-app-auth-375.png'),375,812),testName:cfg.overlay.testName,textScale:1,devicePixelRatio:1,
    renderer:'Flutter widget tester',scene:'test-only initial AuthPage state; not a startup traversal',stateInjectedOnlyInTestHarness:true,
    fontOverride:'existing captureTheme using pinned CJK/MaterialIcons',backendRequests:0,backendConfigurationObserved:'not-requested-test-scene',loginOrMFAAccepted:false,canonical:cfg.canonical,derived:cfg.derived};
}
async function main() {
  const [mode,rootValue,legacy]=process.argv.slice(2);
  check(['all3','web2'].includes(RENDER_MODE),'closed renderer cohort required');
  check(cfg.schema===1&&cfg.cohort===(RENDER_MODE==='web2'?'ui-render-web2-evidence-v1':'ui-render-evidence-v1')
    && cfg.dispatchReady===true,'frozen UI renderer requires Root-admitted descriptor');
  if(RENDER_MODE==='web2') {
    check(canonical(cfg.web.viewports)===canonical([{name:'01-web-login-desktop',width:1440,height:1000,mobile:false},{name:'02-web-login-375',width:375,height:812,mobile:true}]), 'exact two Web viewport outputs required');
    check(cfg.web.sourceInputPin && cfg.web.sourceInputPin.path===cfg.web.sourceInput && /^[0-9a-f]{64}$/.test(cfg.web.sourceInputPin.sha256)
      && Number.isSafeInteger(cfg.web.sourceInputPin.bytes) && cfg.web.sourceInputPin.bytes>0 && Number.isSafeInteger(cfg.web.source.members)
      && /^[0-9a-f]{64}$/.test(cfg.web.source.digest), 'new source binding must be filled after source freeze');
    const artifact=cfg.web.artifact;check(artifact && ['id','run','bytes'].every(k=>Number.isSafeInteger(artifact[k])&&artifact[k]>0)
      && artifact.bytes<=(16<<20) && /^[0-9a-f]{40}$/.test(artifact.head) && /^[0-9a-f]{64}$/.test(artifact.sha256)
      && Number.isSafeInteger(cfg.web.decodedBytes) && cfg.web.decodedBytes>0 && cfg.web.decodedBytes<=(64<<20)
      && /^[0-9a-f]{64}$/.test(cfg.web.manifestSHA256) && /^[0-9a-f]{64}$/.test(cfg.web.filesDigest), 'actual new Web artifact binding required');
    webInventory();appBaselineReference();
  }
  check(process.version==='v24.21.0','fixed Node24.21.0 required');
  for(const row of cfg.controllerPins)rowPin(REPO,row);
  if(mode==='normalize-chrome'){
    check(process.getuid()===0&&fs.realpathSync(CHROME)===CHROME,'fixed root-owned Chrome normalization only');const fd=fs.openSync(CHROME,fs.constants.O_RDONLY|fs.constants.O_NOFOLLOW);
    try{const a=fs.fstatSync(fd,{bigint:true}),m=Number(a.mode)&0o777;check(a.isFile()&&a.uid===0n&&[0o777,0o755].includes(m),'unexpected installed Chrome owner/mode');if(m===0o777)fs.fchmodSync(fd,0o755);const b=fs.fstatSync(fd,{bigint:true}),p=fs.lstatSync(CHROME,{bigint:true});check(a.dev===b.dev&&a.ino===b.ino&&a.size===b.size&&a.mtimeNs===b.mtimeNs&&b.ino===p.ino&&(Number(b.mode)&0o777)===0o755,'Chrome normalization identity changed');console.log(JSON.stringify({event:'fixed-installed-Chrome-mode',bytes:Number(b.size),beforeMode:m,afterMode:0o755}));}finally{fs.closeSync(fd);}return;
  }
  const root=rootArg(rootValue);
  if(mode==='preflight'){check(!fs.existsSync(root),'fresh allocation required');sourceAdmission();const tools=installedTools();fs.mkdirSync(root,{mode:0o700});fs.mkdirSync(path.join(root,'evidence'),{mode:0o700});fs.mkdirSync(path.join(root,'tmp'),{mode:0o700});write(path.join(root,'evidence/preflight.json'),{inputsSHA256:sha(rawInput),tools,source:cfg.canonical,head:process.env.GITHUB_SHA,run:process.env.GITHUB_RUN_ID});return;}
  const pre=JSON.parse(fs.readFileSync(path.join(root,'evidence/preflight.json')));check(pre.inputsSHA256===sha(rawInput),'renderer input changed');unchanged(installedTools(),pre.tools);
  if(mode==='receive'){await receive(root);return;}
  if(mode==='legacy'){
    check(RENDER_MODE==='all3','legacy setup is not permitted in Web-only mode');
    check(command('/usr/bin/git',['-C',legacy,'rev-parse','HEAD']).trim()===cfg.originalSetup.head,'original setup checkout differs');
    for(const row of cfg.originalSetup.helperPins)rowPin(legacy,row);
    check(command('/usr/bin/git',['-C',legacy,'rev-parse','refs/tag903/source-parent']).trim()===cfg.originalSetup.sourceParent,'original setup source parent differs');return;
  }
  if(mode==='prepare'){check(RENDER_MODE==='all3','Flutter preparation is not permitted in Web-only mode');prepare(root,legacy);return;}
  if(mode==='capture-web'){check(RENDER_MODE==='web2','Web-only capture requires explicit cohort');await captureWeb(root,pre);return;}
  check(RENDER_MODE==='all3'&&mode==='capture','fixed renderer mode required');
  check(fs.readlinkSync('/proc/self/ns/net')!==process.env.UI_OUTSIDE_NETNS,'capture requires isolated no-business-network namespace');
  const d=sourceAdmission(),projection=JSON.parse(fs.readFileSync(path.join(root,'evidence/mobile-projection.json'))),work=path.join(root,'mobile/work');
  unchanged(packageMap(work),projection.packageMap);
  webAdmission(root);
  let web,app,renderError;
  try {web=await renderWeb(root);app=mobileCapture(root,d,pre.tools);} catch(error){renderError=error;}
  let postExact=false;
  try {
    sourceAdmission();webAdmission(root);unchanged(installedTools(),pre.tools);unchanged(packageMap(work),projection.packageMap);
    projectionAdmission(work,[...d.portable,{path:cfg.overlay.target,bytes:cfg.overlay.bytes,sha256:cfg.overlay.sha256}]);
    unchanged(pin(path.join(work,'flutter/bin/cache/dart-sdk/bin/dart'),64<<20,true),projection.tools.dart);
    unchanged(pin(path.join(work,'flutter/bin/flutter'),1<<20,true),projection.tools.flutter);
    unchanged(pin(path.join(work,'flutter/bin/cache/artifacts/engine/linux-x64/flutter_tester'),512<<20,true),projection.tools.flutterTester);
    unchanged(pin(path.join(work,'flutter/bin/cache/artifacts/material_fonts/MaterialIcons-Regular.otf'),4<<20),projection.tools.icons);
    postExact=true;
  } finally {write(path.join(root,'evidence/source-tools-post.json'),{sourceToolsPackageMapAndFontsExact:postExact,originalWeb49BodiesPreExact:true,originalWeb49BodiesPostExact:postExact,rendererBodyIncluded:true,renderReturned:!renderError,originalBeforeSHA256:sha(fs.readFileSync(path.join(root,'evidence/mobile-projection.json'))),runtimeOrDesignAccepted:false});}
  if(renderError)throw renderError;
  write(path.join(root,'evidence/render-evidence.json'),{schema:1,actualScreenshots:3,web,app,toolsBeforeAfter:pre.tools,
    sourceAndPackageMapPostExact:true,originalWeb49BodiesPreAndPostExact:true,rendererBodyIncluded:true,WebBuilds:0,APKBuilds:0,old34Or39Redispatched:0,productMockModes:0,
    screenshotsInspectedByAgent:false,interactionAuditAccepted:false,Web107Accepted:false,Mobile403Accepted:false,Admin89Accepted:false});
}
await main().catch(error=>{console.error(JSON.stringify({event:'ui-renderer-failed',accepted:false,
  reason:error.uiClosedLabel ? error.message : 'infrastructure-error',rawErrorTextExported:false}));process.exitCode=1;});
