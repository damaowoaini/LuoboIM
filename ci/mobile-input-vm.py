#!/usr/bin/env python3
"""Read-only bounded diagnostics for one exact owned Flutter tester; never default acceptance."""
import argparse, base64, hashlib, json, os
from pathlib import Path
import re, signal, socket, struct, subprocess, sys, threading, time
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parent.parent
TARGET = 'wrong-peer wrong-recipient malformed and offline typing never enters history or the active header'
MARKER = 'chat_input_unmount pumpWidget.before'
RULE_SHA = 'bef5fd760ded7beab99f71cd007ace681f001ed6d28bb58ad3dd014e20ba859d'
MAX_LINE = 262144
URI_PATTERN = re.compile(r'(?:https?|wss?)://[^\s<>"\x27\\]+', re.IGNORECASE)


def canonical(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode()


def write_private(path, value):
    raw = canonical(value)+b'\n'
    if len(raw) > 65536:
        raise ValueError('private-record-limit')
    fd = os.open(path, os.O_WRONLY|os.O_CREAT|os.O_EXCL, 0o600)
    with os.fdopen(fd, 'wb') as out:
        out.write(raw);out.flush();os.fsync(out.fileno())


def endpoint(value):
    if not isinstance(value, str) or len(value)>512:
        raise ValueError('uri-shape')
    u = urlsplit(value)
    if (u.scheme != 'http' or u.hostname != '127.0.0.1' or u.username is not None
            or u.password is not None or u.query or u.fragment
            or u.netloc != '127.0.0.1:'+str(u.port) or not 1024 <= u.port <= 65535
            or not re.fullmatch(r'/[A-Za-z0-9_-]{8,128}={0,2}/',u.path)):
        raise ValueError('uri-not-authenticated-loopback')
    return u.port,u.path+'ws'


def clean_line(line):
    # No VM URI/auth path goes to the uploaded reporter stream, including
    # informational lines. Protocol replies never use this output path at all.
    return URI_PATTERN.sub('<diagnostic-uri-redacted>',line)


def process(pid, proc=Path('/proc')):
    p=proc/str(pid);raw=(p/'stat').read_text();end=raw.rfind(') ')
    if end<1 or not raw[:raw.index(' ')].isdigit():raise ValueError('proc-stat-shape')
    f=raw[end+2:].split()
    if len(f)<20:raise ValueError('proc-stat-short')
    return {'pid':int(pid),'ppid':int(f[1]),'pgrp':int(f[2]),'session':int(f[3]),
            'start':int(f[19]),'ticks':int(f[11])+int(f[12]),
            'uid':p.stat().st_uid,'exe':os.readlink(p/'exe')}


def same_owner(a,b):
    return all(a[k]==b[k] for k in ('pid','start','session','pgrp','uid','exe'))


def socket_owner(port, child, work, proc=Path('/proc'), original=None, deadline=None):
    # --no-dds makes this the tester socket itself, never an unrelated service.
    inode=None
    lines=(proc/'net/tcp').read_text().splitlines()
    if len(lines)>8192:raise ValueError('socket-inventory-limit')
    for line in lines[1:]:
        f=line.split()
        if len(f)>9 and f[1]==f'0100007F:{port:04X}' and f[3]=='0A':
            if inode is not None or int(f[7])!=os.getuid():raise ValueError('socket-owner-ambiguous')
            inode=f[9]
    if inode is None:raise ValueError('tester-listener-unavailable')
    root=process(child.pid,proc);session=os.getsid(0)
    if original is not None and any(root[k]!=original[k] for k in ('pid','start','uid','pgrp','session')):
        raise ValueError('original-launcher-incarnation')
    if root['uid']!=os.getuid() or root['session']!=session or root['pgrp']!=os.getpgrp():raise ValueError('original-child-owner')
    wanted=str(work/'flutter/bin/cache/artifacts/engine/linux-x64/flutter_tester')
    candidates=[]
    paths=[p for p in proc.iterdir() if p.name.isdigit()]
    if len(paths)>8192:raise ValueError('process-inventory-limit')
    for p in paths:
        if deadline is not None and time.monotonic()>=deadline:raise TimeoutError('owner-observation-deadline')
        try:
            row=process(int(p.name),proc)
            if row['uid']!=os.getuid() or row['session']!=session or row['pgrp']!=os.getpgrp() or row['exe']!=wanted:continue
            parent=row;visited=set()
            while parent['pid']!=child.pid:
                if parent['pid'] in visited or parent['ppid']<2:raise ValueError('foreign-parent')
                visited.add(parent['pid']);parent=process(parent['ppid'],proc)
                if len(visited)>32:raise ValueError('parent-chain-limit')
            if not same_owner(parent,root):raise ValueError('parent-incarnation')
            fds=list((p/'fd').iterdir())
            if len(fds)>4096:raise ValueError('fd-limit')
            if any(os.readlink(fd)=='socket:['+inode+']' for fd in fds):candidates.append(row)
        except (FileNotFoundError,ProcessLookupError,PermissionError):continue
    if len(candidates)!=1:raise ValueError('tester-socket-not-unique')
    return candidates[0]


class ReadOnlyVM:
    def __init__(self,port,path,owner_check,deadline):
        self.check=owner_check;self.deadline=deadline;self.sock=None;self.serial=0
        self.check();self.sock=socket.create_connection(('127.0.0.1',port),timeout=2)
        try:
            self.sock.settimeout(2);key=base64.b64encode(os.urandom(16)).decode()
            request=(f'GET {path} HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: {key}\r\n\r\n').encode()
            self.sock.sendall(request);header=b''
            while not header.endswith(b'\r\n\r\n'):
                if len(header)>=8192:raise ValueError('upgrade-limit')
                header+=self._read(1)
            lines=header.decode('ascii').split('\r\n');fields={}
            for line in lines[1:]:
                if line:
                    k,v=line.split(':',1);k=k.lower()
                    if k in fields:raise ValueError('duplicate-upgrade-header')
                    fields[k]=v.strip()
            accept=base64.b64encode(hashlib.sha1((key+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest()).decode()
            if (lines[0]!='HTTP/1.1 101 Switching Protocols' or fields.get('sec-websocket-accept')!=accept
                    or fields.get('upgrade','').lower()!='websocket' or fields.get('connection','').lower()!='upgrade'):
                raise ValueError('upgrade-refused')
            self.check()
        except BaseException:
            self.close()
            raise

    def close(self):
        if self.sock is not None:self.sock.close();self.sock=None

    def _read(self,n):
        out=b''
        while len(out)<n:
            remaining=self.deadline-time.monotonic()
            if remaining<=0:raise TimeoutError('diagnostic-deadline')
            self.sock.settimeout(min(2,remaining));part=self.sock.recv(n-len(out))
            if not part:raise ValueError('service-closed')
            out+=part
        return out

    def _send(self,payload,opcode=1):
        if len(payload)>4096:raise ValueError('request-limit')
        mask=os.urandom(4);size=len(payload);header=bytes([0x80|opcode])
        header+=bytes([0x80|size]) if size<126 else bytes([0x80|126])+struct.pack('!H',size)
        self.sock.sendall(header+mask+bytes(b^mask[i%4] for i,b in enumerate(payload)))

    def rpc(self,method,params):
        if method not in ('getVM','getIsolate','getStack'):raise ValueError('non-readonly-method')
        if (method=='getVM' and params or method=='getIsolate' and set(params)!= {'isolateId'}
                or method=='getStack' and (set(params)!={'isolateId','limit'} or params['limit']!=32)):
            raise ValueError('rpc-parameter-scope')
        self.check();self.serial+=1;identity=str(self.serial)
        self._send(canonical({'jsonrpc':'2.0','id':identity,'method':method,'params':params}))
        data=b'';started=False
        for _ in range(128):
            a,b=self._read(2);fin=bool(a&128);opcode=a&15
            if a&112 or b&128:raise ValueError('service-frame-flags')
            size=b&127
            if size==126:size=struct.unpack('!H',self._read(2))[0]
            elif size==127:size=struct.unpack('!Q',self._read(8))[0]
            if size>262144 or len(data)+size>262144:raise ValueError('service-response-limit')
            part=self._read(size)
            if opcode==9:
                if not fin or size>125:raise ValueError('control-frame-shape')
                self._send(part,10);continue
            if opcode not in (0,1) or opcode==0 and not started or opcode==1 and started:raise ValueError('service-frame-type')
            started=True;data+=part
            if not fin:continue
            response=json.loads(data)
            if (not isinstance(response,dict) or response.get('id')!=identity or response.get('jsonrpc')!='2.0'
                    or 'error' in response or not isinstance(response.get('result'),dict)):
                raise ValueError('service-rpc-refused')
            self.check();return response['result']
        raise ValueError('service-frame-count-limit')


def safe_text(value,limit):
    return value if isinstance(value,str) and 0<len(value)<=limit and re.fullmatch(r'[\w.$<>:/ \[\]()=+\-]+',value) else None


def safe_frame(frame):
    if not isinstance(frame,dict):raise ValueError('frame-shape')
    result={};function=frame.get('function') or {};loc=frame.get('location') or {};script=loc.get('script') or {}
    if name:=safe_text(function.get('name'),192):result['function']=name
    if kind:=safe_text(frame.get('kind'),64):result['kind']=kind
    uri=script.get('uri')
    if isinstance(uri,str) and uri.startswith(('dart:','package:','org-dartlang-sdk:','org-dartlang-app:')):
        if path:=safe_text(uri,256):result['script']=path
    elif isinstance(uri,str) and uri.startswith('file:'):
        # Retain only static Dart basename, never absolute paths/query/auth.
        if name:=safe_text(Path(urlsplit(uri).path).name,96):result['scriptBasename']=name
    for key in ('line','column','tokenPos'):
        if type(loc.get(key)) is int and -1<=loc[key]<=10000000:result[key]=loc[key]
    return result


def safe_sample(isolate,stack):
    if isolate.get('type')!='Isolate' or stack.get('type')!='Stack':raise ValueError('service-result-type')
    out={'runnable':isolate.get('runnable') is True,'truncated':stack.get('truncated') is True}
    event=isolate.get('pauseEvent') or {}
    if pause:=safe_text(event.get('kind'),64):out['pauseKind']=pause
    for key in ('frames','asyncCausalFrames'):
        frames=stack.get(key,[])
        if not isinstance(frames,list) or len(frames)>32:raise ValueError('stack-frame-limit')
        out[key]=[safe_frame(frame) for frame in frames]
    return out


def collect(uri,child,work,private,stop):
    started=time.monotonic();deadline=started+20;port,path=endpoint(uri);original=json.loads((private/'original-child.json').read_bytes());owner=socket_owner(port,child,work,original=original,deadline=deadline)
    def checked():
        if stop.is_set() or child.poll() is not None:raise ValueError('original-child-ended')
        current=socket_owner(port,child,work,original=original,deadline=deadline)
        if not same_owner(owner,current):raise ValueError('tester-incarnation-changed')
        return current
    write_private(private/'service-uri.json',{'uri':uri,'owner':owner})
    records=[];client=None
    try:
        client=ReadOnlyVM(port,path,checked,deadline)
        vm=client.rpc('getVM',{})
        candidates=[v for v in vm.get('isolates',[]) if isinstance(v,dict) and v.get('name')=='main' and v.get('isSystemIsolate') is not True]
        if vm.get('pid')!=owner['pid'] or len(candidates)!=1:raise ValueError('vm-process-or-main-isolate')
        isolateID=candidates[0].get('id')
        if not isinstance(isolateID,str) or not re.fullmatch(r'isolates/[0-9]+',isolateID):raise ValueError('isolate-id-shape')
        for index in range(2):
            if index and stop.wait(1):raise ValueError('original-owner-stopped')
            before=checked();row={'sample':index+1,'ownerPID':owner['pid'],'ownerStart':owner['start'],'accepted':False}
            try:
                iso=client.rpc('getIsolate',{'isolateId':isolateID})
                stack=client.rpc('getStack',{'isolateId':isolateID,'limit':32})
                row.update(safe_sample(iso,stack));row['accepted']=True
            except Exception as error:
                row['exceptionType']=type(error).__name__
            after=checked();delta=after['ticks']-before['ticks']
            if delta<0:raise ValueError('cpu-counter-incarnation')
            row['cpuTicksDelta']=delta;row['ticksPerSecond']=os.sysconf('SC_CLK_TCK');records.append(row)
            write_private(work/'evidence'/f'input-vm-wait-sample{index+1}.json',row)
    finally:
        if client is not None:client.close()
    return {'attemptedSamples':len(records),'acceptedSamples':sum(r['accepted'] for r in records),'serviceRPCsAllowed':['getVM','getIsolate','getStack'],'defaultNoVMServiceAccepted':False}


def write_profiled_pass(work, phases, source, portable):
    diagnostic=json.loads((work/'evidence/input-vm-wait-diagnostic.json').read_bytes())
    if (len(phases)!=1 or phases[0].get('accepted') is not True
            or phases[0].get('latched_failure') is not None
            or phases[0].get('source_post',{}).get('accepted') is not True
            or diagnostic.get('acceptedSamples')!=2
            or diagnostic.get('defaultNoVMServiceAccepted') is not False):
        raise ValueError('incomplete-profiled-diagnostic')
    write_private(work/'evidence/input-vm-profiled-passed.json',
                  {'profiledTestPassed':True,'defaultNoVMServiceAccepted':False,
                   'source363':source,'portable415':portable,'phases':phases,
                   'vmWait':diagnostic,'full1329Executed':False})


def run(work):
    if sys.platform!='linux' or os.getuid()==0 or work.resolve()!=work:raise ValueError('owned-linux-only')
    cfg=json.loads((ROOT/'ci/mobile-input/inputs.json').read_bytes());rules=cfg['vm_diagnostic']
    if hashlib.sha256(canonical(rules)).hexdigest()!=RULE_SHA:raise ValueError('diagnostic-contract')
    for pin in rules['flutterSourcePins']:
        p=work/'flutter'/pin['path'];raw=p.read_bytes()
        if p.is_symlink() or len(raw)!=pin['bytes'] or hashlib.sha256(raw).hexdigest()!=pin['sha256']:raise ValueError('fixed-flutter-tool-source')
    private=work/'private-vm-diagnostic';private.mkdir(mode=0o700)
    command=[str(work/'flutter/bin/flutter'),'test','--no-pub','--machine','--concurrency=1','--no-enable-vmservice','--name','^(?:'+re.escape(TARGET)+')$','test/features/chat/chat_typing_workflow_test.dart']
    command[command.index('--no-enable-vmservice')]='--enable-vmservice';command.insert(6,'--no-dds')
    stop=threading.Event();trigger=threading.Event();ready=threading.Event();state={'uri':None,'targetID':None,'seen':False};outcome={'attemptedSamples':0,'acceptedSamples':0,'defaultNoVMServiceAccepted':False}
    os.umask(0o077)  # This fresh diagnostic child only; never mutate the invoking shell.
    child=subprocess.Popen(command,cwd=work/'source/app',stdout=subprocess.PIPE,stderr=subprocess.STDOUT)
    thread=None;previous={}
    try:
        original=process(child.pid);write_private(private/'original-child.json',original)
        def interrupted(signum,frame):stop.set()
        previous={s:signal.signal(s,interrupted) for s in (signal.SIGTERM,signal.SIGINT)}
        def sampler():
            try:
                while not stop.is_set():
                    if trigger.wait(.1):break
                if stop.is_set():raise ValueError('marker-unavailable-before-stop')
                if not ready.wait(2):raise TimeoutError('service-uri-unavailable-after-marker')
                outcome.update(collect(state['uri'],child,work,private,stop))
            except Exception as error:outcome['exceptionType']=type(error).__name__
            finally:
                outcome['pumpWidgetBeforeObserved']=trigger.is_set();outcome['authenticatedServiceURIObserved']=ready.is_set()
                write_private(work/'evidence/input-vm-wait-diagnostic.json',outcome)
        thread=threading.Thread(target=sampler);thread.start()
        while line:=child.stdout.readline(MAX_LINE+1):
            if len(line)>MAX_LINE or not line.endswith(b'\n'):raise ValueError('reporter-line-limit')
            text=line.decode('utf-8','replace')
            try:event=json.loads(text)
            except ValueError:event=None
            if isinstance(event,list) and len(event)==1 and isinstance(event[0],dict) and event[0].get('event')=='test.startedProcess':
                uri=event[0].get('params',{}).get('vmServiceUri');endpoint(uri)
                if state['uri'] is not None:raise ValueError('duplicate-service-event')
                state['uri']=uri;ready.set()
            if isinstance(event,dict) and event.get('type')=='testStart' and event.get('test',{}).get('name')==TARGET:
                if state['targetID'] is not None:raise ValueError('duplicate-target')
                state['targetID']=event['test']['id']
            if (isinstance(event,dict) and state['targetID'] is not None and event.get('type')=='print' and event.get('testID')==state['targetID']
                    and event.get('message')==MARKER):
                if state['seen']:raise ValueError('duplicate-original-await-marker')
                state['seen']=True;trigger.set()
            sys.stdout.write(clean_line(text));sys.stdout.flush()
        code=child.wait();stop.set();thread.join(timeout=3)
        if thread.is_alive():raise ValueError('sampler-not-joined')
        return code if code else 0 if outcome.get('acceptedSamples')==2 else 72
    finally:
        stop.set()
        if child.poll() is None:child.terminate()
        try:child.wait(timeout=2)
        except subprocess.TimeoutExpired:child.kill();child.wait()
        if thread is not None:thread.join(timeout=3)
        for sig,handler in previous.items():signal.signal(sig,handler)
        # Never return while a physical child remains in this outer-guarded PG.
        # A failed private join forces the original PG to fail, never a drain.
        def remaining():
            result=[]
            for p in Path('/proc').iterdir():
                if not p.name.isdigit() or int(p.name)==os.getpid():continue
                try:
                    row=process(int(p.name))
                    if row['session']==os.getsid(0) and row['pgrp']==os.getpgrp() and row['uid']==os.getuid():result.append(row)
                except (FileNotFoundError,ProcessLookupError,PermissionError):continue
            return result
        for sig in (signal.SIGTERM,signal.SIGKILL):
            for row in remaining():
                try:
                    if same_owner(row,process(row['pid'])):os.kill(row['pid'],sig)
                except (FileNotFoundError,ProcessLookupError):pass
            until=time.monotonic()+.5
            while time.monotonic()<until and remaining():time.sleep(.05)
        if remaining():os.killpg(os.getpgrp(),signal.SIGKILL)


if __name__=='__main__':
    parser=argparse.ArgumentParser();parser.add_argument('--work',required=True,type=Path);args=parser.parse_args()
    try:raise SystemExit(run(args.work))
    except Exception as error:
        # Fixed exception class only. Never print socket errors/URI/response.
        print(json.dumps({'event':'vm_diagnostic_refused','exceptionType':type(error).__name__}),flush=True);raise SystemExit(73)
