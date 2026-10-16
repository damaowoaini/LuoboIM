// Test-only launcher for the actual built main/preload. Never packaged.
// Uses a disposable profile and the strict fixture certificate verifier;
// no product trust configuration, user profile or system CA is modified.
const {app,session}=require('electron');
const fs=require('node:fs');
const os=require('node:os');
const path=require('node:path');
const crypto=require('node:crypto');
const {FIXTURE_HOSTS,verifyFixtureCertificate}=require('../../admin/tool/fixture-certificate.cjs');
const project=path.resolve(__dirname,'../..');
const integration=path.resolve(project,'../references/integration');
const tls=path.join(integration,'tls');
const manifest=JSON.parse(fs.readFileSync(path.join(tls,'manifest.json'),'utf8'));
const authority=new crypto.X509Certificate(fs.readFileSync(path.join(tls,'ca.pem')));
const requestedProfile=process.env.LUOBO_FIXTURE_PROFILE;
const profile=(()=>{
 if(!requestedProfile)return fs.mkdtempSync(path.join(os.tmpdir(),'luobo-native-fixture-'));
 const info=fs.lstatSync(requestedProfile);
 if(!info.isDirectory()||info.isSymbolicLink()||(info.mode&0o077)!==0||info.uid!==process.getuid()||
   !/^luobo-native-fixture-[A-Za-z0-9]{6}$/.test(path.basename(requestedProfile))||
   fs.realpathSync(path.dirname(requestedProfile))!==fs.realpathSync(os.tmpdir()))throw new Error('Only a private task-created disposable profile can be resumed');
 return fs.realpathSync(requestedProfile);
})();
app.setPath('userData',profile);
app.setPath('sessionData',profile);
app.setName('萝卜对话桌面验收');
const observation={fixtureOnly:true,profile,requests:[],failures:[],console:[],preloadErrors:[],rendererGone:[],ipc:[]};
const observationPath=path.join(integration,'stack-config/parity-desktop-observation.json');
const save=()=>fs.writeFileSync(observationPath,JSON.stringify(observation),{mode:0o600});
const configured=new WeakMap();
function configure(s){
 if(configured.has(s))return configured.get(s);
 s.setCertificateVerifyProc((request,answer)=>answer(verifyFixtureCertificate({hostname:request.hostname,certificate:request.certificate.data,authority,manifest})));
 s.setPermissionRequestHandler((_webContents,_permission,answer)=>answer(false));
 s.webRequest.onCompleted(details=>{
  const url=new URL(details.url);
  if(FIXTURE_HOSTS.has(url.hostname)){
   observation.requests.push({method:details.method,host:url.hostname,path:url.pathname,status:details.statusCode,resourceType:details.resourceType});save();
  }
 });
 s.webRequest.onErrorOccurred(details=>{
  const url=new URL(details.url);
  if(FIXTURE_HOSTS.has(url.hostname)){
   observation.failures.push({host:url.hostname,path:url.pathname,error:details.error});save();
  }
 });
 const operation=s.setProxy({proxyRules:'http://'+manifest.connect_proxy});
 configured.set(s,operation);operation.catch(()=>app.quit());return operation;
}
app.on('session-created',s=>void configure(s));
app.on('web-contents-created',(_event,contents)=>{
 contents.on('preload-error',(_event,_preload,error)=>{observation.preloadErrors.push({name:error.name});save();});
 contents.on('render-process-gone',(_event,details)=>{observation.rendererGone.push({reason:details.reason,exitCode:details.exitCode});save();});
 contents.on('console-message',event=>{
  const message=event.message||'';
  const categories=['SDK登录失败','WebAssembly','SharedArrayBuffer','SQL close','Failed to fetch','Content Security Policy'].filter(label=>message.includes(label));
  if(categories.length){observation.console.push({level:event.level,categories,sdkErrorCode:message.match(/^SDK登录失败 (-?\d{1,8}|unknown)$/)?.[1]});save();}
 });
 contents.on('ipc-message',(_event,channel)=>{
  if(['main-win-ready','setUserCachePath','updateUnreadCount'].includes(channel)){observation.ipc.push({channel});save();}
 });
});
delete process.env.VITE_DEV_SERVER_URL;
app.whenReady().then(async()=>{
 await configure(session.defaultSession);save();
 require(path.resolve(__dirname,'../dist-electron/main/index.js'));
});
