#!/usr/bin/env python3
"""Direct protocol/record controls only; no Flutter, native VM or business operations."""
import base64, hashlib, importlib.util, json, os
from pathlib import Path
import socket, struct, tempfile, threading, time, types, unittest
from unittest import mock
spec=importlib.util.spec_from_file_location('vm_diag',Path(__file__).with_name('mobile-input-vm.py'))
v=importlib.util.module_from_spec(spec);spec.loader.exec_module(v)


class VMWaitControls(unittest.TestCase):
    def test_actual_machine_uri_syntax_stays_private_and_rejects_nonowned_destinations(self):
        uri='http://127.0.0.1:40123/opaqueAuthCode=/';self.assertEqual(v.endpoint(uri),(40123,'/opaqueAuthCode=/ws'))
        for value in ['http://localhost:40123/opaqueAuthCode=/', 'https://127.0.0.1:40123/opaqueAuthCode=/', 'http://127.0.0.1:40123/', 'http://user@127.0.0.1:40123/opaqueAuthCode=/', uri+'?x=1',uri+'#x','http://127.0.0.2:40123/opaqueAuthCode=/']:
            with self.subTest(value=value),self.assertRaises(ValueError):v.endpoint(value)
        line=json.dumps([{'event':'test.startedProcess','params':{'vmServiceUri':uri}}])+'\n'
        self.assertNotIn('opaqueAuthCode',v.clean_line(line));self.assertNotIn(uri,v.clean_line(line))
        with tempfile.TemporaryDirectory() as folder:
            path=Path(folder)/'private.json';v.write_private(path,{'uri':uri});body=path.read_bytes()
            self.assertEqual(path.stat().st_mode&0o777,0o600)
            with self.assertRaises(FileExistsError):v.write_private(path,{'uri':'changed'})
            self.assertEqual(path.read_bytes(),body)

    def test_real_loopback_websocket_limits_methods_frames_and_projects_no_locals_or_objects(self):
        listener=socket.socket();listener.bind(('127.0.0.1',0));listener.listen(1);port=listener.getsockname()[1];errors=[];seen=[]
        def read(sock,n):
            out=b''
            while len(out)<n:
                part=sock.recv(n-len(out))
                if not part:raise ValueError('fixture closed')
                out+=part
            return out
        def serve():
            try:
                with listener.accept()[0] as sock:
                    sock.settimeout(3);header=b''
                    while not header.endswith(b'\r\n\r\n'):header+=read(sock,1)
                    key=next(x.split(': ',1)[1] for x in header.decode().split('\r\n') if x.startswith('Sec-WebSocket-Key: '))
                    accept=base64.b64encode(hashlib.sha1((key+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest()).decode()
                    sock.sendall(('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: '+accept+'\r\n\r\n').encode())
                    for _ in range(2):
                        a,b=read(sock,2);self.assertTrue(b&128);size=b&127
                        if size==126:size=struct.unpack('!H',read(sock,2))[0]
                        mask=read(sock,4);request=json.loads(bytes(x^mask[i%4] for i,x in enumerate(read(sock,size))));seen.append(request['method'])
                        result={'type':'VM','pid':123,'isolates':[]} if request['method']=='getVM' else {'type':'Stack','frames':[{'function':{'name':'WidgetTester.pumpWidget'},'vars':[{'value':'LOCAL-SECRET'}],'location':{'script':{'uri':'package:flutter_test/src/widget_tester.dart'},'line':600}}],'objects':'LOCAL-SECRET'}
                        data=json.dumps({'jsonrpc':'2.0','id':request['id'],'result':result}).encode();sock.sendall(bytes([129,126])+struct.pack('!H',len(data))+data)
            except Exception as error:errors.append(type(error).__name__)
            finally:listener.close()
        thread=threading.Thread(target=serve);thread.start();client=None
        try:
            checked=[];client=v.ReadOnlyVM(port,'/opaqueAuthCode=/ws',lambda:checked.append(True),time.monotonic()+10)
            client.rpc('getVM',{});stack=client.rpc('getStack',{'isolateId':'isolates/1','limit':32});safe=v.safe_sample(stack)
            self.assertNotIn('LOCAL-SECRET',json.dumps(safe));self.assertEqual(safe['frames'][0]['function'],'WidgetTester.pumpWidget')
            for method,params in [('evaluate',{}),('pause',{}),('getObject',{}),('getIsolate',{'isolateId':'isolates/1'}),('getStack',{'isolateId':'isolates/1','limit':1000})]:
                with self.subTest(method=method),self.assertRaises(ValueError):client.rpc(method,params)
            self.assertEqual(seen,['getVM','getStack']);self.assertFalse(safe['isolateStateObserved']);self.assertNotIn('runnable',safe);self.assertNotIn('pauseKind',safe);self.assertGreaterEqual(len(checked),6)
        finally:
            if client:client.close()
            thread.join(4)
        self.assertFalse(thread.is_alive());self.assertEqual(errors,[])

    def test_production_sampler_writes_only_two_filtered_records_and_never_default_pass(self):
        original={'pid':123,'start':20,'uid':os.getuid(),'pgrp':55,'session':55,'exe':'/owned/flutter_tester','ticks':100}
        ticks=[100]
        def owner(*args,**kwargs):ticks[0]+=1;return dict(original,ticks=ticks[0])
        clients=[];seen=[]
        class Client:
            def __init__(self,port,path,check,deadline):check();self.check=check;self.closed=False;clients.append(self)
            def rpc(self,method,params):
                if self.closed:raise AssertionError('closed sampler connection reused')
                self.check();seen.append(method)
                if method=='getVM':return {'type':'VM','pid':123,'isolates':[{'name':'main','id':'isolates/1'}]}
                if method!='getStack':raise AssertionError('only bounded stack read allowed')
                return {'type':'Stack','frames':[{'function':{'name':'pumpWidget'},'vars':['SECRET']}],'asyncCausalFrames':[]}
            def close(self):self.closed=True
        with tempfile.TemporaryDirectory() as folder:
            work=Path(folder).resolve();private=work/'private';private.mkdir(mode=0o700);(work/'evidence').mkdir();v.write_private(private/'original-child.json',original)
            child=types.SimpleNamespace(pid=123,poll=lambda:None)
            with mock.patch.object(v,'socket_owner',side_effect=owner),mock.patch.object(v,'ReadOnlyVM',Client):
                result=v.collect('http://127.0.0.1:40123/opaqueAuthCode=/',child,work,private,threading.Event())
            self.assertEqual(seen,['getVM','getStack','getStack']);self.assertEqual(len(clients),3);self.assertTrue(all(client.closed for client in clients))
            self.assertEqual(result['serviceRPCsAllowed'],['getVM','getStack']);self.assertFalse(result['isolateStateObserved']);self.assertTrue(result['freshConnectionPerSample'])
            self.assertEqual(result['acceptedSamples'],2);self.assertFalse(result['defaultNoVMServiceAccepted'])
            for index in (1,2):
                raw=(work/'evidence'/f'input-vm-wait-sample{index}.json').read_bytes();self.assertNotIn(b'SECRET',raw);self.assertNotIn(b'opaqueAuthCode',raw)
            v.write_private(work/'evidence/input-vm-wait-diagnostic.json',result)
            phases=[{'accepted':True,'latched_failure':None,'source_post':{'accepted':True}}]
            v.write_profiled_pass(work,phases,'source','portable')
            self.assertTrue((work/'evidence/input-vm-profiled-passed.json').exists());self.assertFalse((work/'evidence/input-scoped-passed.json').exists())
            self.assertFalse(json.loads((work/'evidence/input-vm-profiled-passed.json').read_bytes())['defaultNoVMServiceAccepted'])
        with tempfile.TemporaryDirectory() as folder:
            work=Path(folder).resolve();private=work/'private';private.mkdir(mode=0o700);(work/'evidence').mkdir();v.write_private(private/'original-child.json',original)
            changed=iter([dict(original),dict(original,start=21)])
            with mock.patch.object(v,'socket_owner',side_effect=lambda *a,**k:next(changed)),mock.patch.object(v,'ReadOnlyVM',Client):
                with self.assertRaisesRegex(ValueError,'tester-incarnation'):v.collect('http://127.0.0.1:40123/opaqueAuthCode=/',types.SimpleNamespace(pid=123,poll=lambda:None),work,private,threading.Event())
            self.assertFalse((work/'evidence/input-vm-wait-sample1.json').exists())


        listener=socket.socket();listener.bind(('127.0.0.1',0));listener.listen(3)
        port=listener.getsockname()[1];wire=[];errors=[]
        def read(sock,n):
            out=b''
            while len(out)<n:
                part=sock.recv(n-len(out))
                if not part:raise ValueError('fixture closed')
                out+=part
            return out
        def serve():
            try:
                for index in range(3):
                    with listener.accept()[0] as sock:
                        sock.settimeout(3);header=b''
                        while not header.endswith(b'\r\n\r\n'):header+=read(sock,1)
                        key=next(x.split(': ',1)[1] for x in header.decode().split('\r\n') if x.startswith('Sec-WebSocket-Key: '))
                        accept=base64.b64encode(hashlib.sha1((key+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest()).decode()
                        sock.sendall(('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: '+accept+'\r\n\r\n').encode())
                        a,b=read(sock,2);size=b&127
                        if size==126:size=struct.unpack('!H',read(sock,2))[0]
                        mask=read(sock,4);request=json.loads(bytes(x^mask[i%4] for i,x in enumerate(read(sock,size))))
                        wire.append(request['method'])
                        if index==1:
                            self.assertEqual(request['method'],'getStack')
                            sock.sendall(bytes([129,127])+struct.pack('!Q',262145)+b'unread-tail')
                            continue
                        result=({'type':'VM','pid':123,'isolates':[{'name':'main','id':'isolates/1'}]} if index==0 else
                            {'type':'Stack','frames':[{'function':{'name':'secondFreshSample'},'vars':['WIRE-SECRET']}],'asyncCausalFrames':[]})
                        data=json.dumps({'jsonrpc':'2.0','id':request['id'],'result':result}).encode()
                        sock.sendall(bytes([129,126])+struct.pack('!H',len(data))+data)
            except Exception as error:errors.append(type(error).__name__)
            finally:listener.close()
        thread=threading.Thread(target=serve);thread.start()
        try:
            with tempfile.TemporaryDirectory() as folder:
                work=Path(folder).resolve();private=work/'private';private.mkdir(mode=0o700);(work/'evidence').mkdir();v.write_private(private/'original-child.json',original)
                stop=types.SimpleNamespace(is_set=lambda:False,wait=lambda seconds:False)
                with mock.patch.object(v,'socket_owner',side_effect=owner):
                    result=v.collect(f'http://127.0.0.1:{port}/opaqueAuthCode=/',types.SimpleNamespace(pid=123,poll=lambda:None),work,private,stop)
                one=json.loads((work/'evidence/input-vm-wait-sample1.json').read_bytes());two=json.loads((work/'evidence/input-vm-wait-sample2.json').read_bytes())
                self.assertFalse(one['accepted']);self.assertEqual(one['failureReason'],'service-response-limit')
                self.assertEqual(one['failureStage'],'get-stack');self.assertTrue(two['accepted'])
                self.assertEqual(two['frames'][0]['function'],'secondFreshSample');self.assertFalse(two['isolateStateObserved'])
                self.assertEqual(result['acceptedSamples'],1);self.assertFalse(result['defaultNoVMServiceAccepted'])
                self.assertNotIn('WIRE-SECRET',json.dumps(two));self.assertNotIn('opaqueAuthCode',json.dumps(two))
        finally:
            listener.close();thread.join(4)
        self.assertFalse(thread.is_alive());self.assertEqual(errors,[]);self.assertEqual(wire,['getVM','getStack','getStack'])

    def test_sampler_failure_stage_and_closed_reason_never_export_exception_text_or_rpc_payload(self):
        secret='http://127.0.0.1:40123/PRIVATE-AUTH/ LOCAL-SECRET'
        original={'pid':123,'start':20,'uid':os.getuid(),'pgrp':55,'session':55,'exe':'/owned/flutter_tester','ticks':100}
        class UnprintableError(ValueError):
            def __str__(self):raise AssertionError('exception text must not be read')
        scenarios=[('get-stack',ValueError('service-response-limit'),'service-response-limit'),
                   ('get-stack',ValueError('service-rpc-refused'),'service-rpc-refused'),
                   ('project-stack',None,'frame-shape'),
                   ('get-stack',ValueError(secret),'other'),
                   ('get-stack',ValueError('service-response-limit',secret),'other'),
                   ('get-stack',UnprintableError(secret),'other')]
        for failure_stage,error,reason in scenarios:
            with self.subTest(stage=failure_stage,reason=reason),tempfile.TemporaryDirectory() as folder:
                work=Path(folder).resolve();private=work/'private';private.mkdir(mode=0o700);(work/'evidence').mkdir();v.write_private(private/'original-child.json',original)
                seen=[];clients=[]
                class Client:
                    def __init__(self,port,path,check,deadline):check();self.check=check;self.closed=False;clients.append(self)
                    def rpc(self,method,params):
                        if self.closed:raise AssertionError('closed failed connection reused')
                        self.check();seen.append(method)
                        if method=='getVM':return {'type':'VM','pid':123,'isolates':[{'name':'main','id':'isolates/1'}]}
                        if method=='getStack' and failure_stage=='get-stack':raise error
                        return {'type':'Stack','frames':[None],'vars':[secret],'objects':secret}
                    def close(self):self.closed=True
                stop=types.SimpleNamespace(is_set=lambda:False,wait=lambda seconds:False)
                child=types.SimpleNamespace(pid=123,poll=lambda:None)
                with mock.patch.object(v,'socket_owner',return_value=dict(original)),mock.patch.object(v,'ReadOnlyVM',Client):
                    result=v.collect('http://127.0.0.1:40123/opaqueAuthCode=/',child,work,private,stop)
                self.assertEqual(result['attemptedSamples'],2);self.assertEqual(result['acceptedSamples'],0);self.assertFalse(result['defaultNoVMServiceAccepted'])
                expected=['getVM','getStack','getStack']
                self.assertEqual(len(clients),3);self.assertTrue(all(client.closed for client in clients))
                self.assertEqual(seen,expected)
                for index in (1,2):
                    raw=(work/'evidence'/f'input-vm-wait-sample{index}.json').read_bytes();row=json.loads(raw)
                    self.assertFalse(row['accepted']);self.assertEqual(row['failureStage'],failure_stage);self.assertEqual(row['failureReason'],reason)
                    self.assertNotIn(b'PRIVATE-AUTH',raw);self.assertNotIn(b'LOCAL-SECRET',raw);self.assertNotIn(b'opaqueAuthCode',raw)
                    self.assertNotIn('frames',row);self.assertNotIn('objects',row);self.assertNotIn('vars',row)
        self.assertEqual(v.sample_failure(secret,ValueError(secret)),{'failureStage':'other','failureReason':'other'})

    def test_exact_completed_widget_raw_is_distinct_from_required_hang_samples_and_never_default_acceptance(self):
        # Exact uploaded/redacted machine stream; no Flutter/Popen/VM is invoked.
        raw = (
            b'{"protocolVersion":"0.1.1","runnerVersion":null,"pid":4248,"type":"start","time":0}\n'
            b'{"suite":{"id":0,"platform":"vm","path":"/home/runner/work/_temp/mobile-current363-input/source/app/test/features/chat/chat_typing_workflow_test.dart"},"type":"suite","time":0}\n'
            b'{"test":{"id":1,"name":"loading /home/runner/work/_temp/mobile-current363-input/source/app/test/features/chat/chat_typing_workflow_test.dart","suiteID":0,"groupIDs":[],"metadata":{"skip":false,"skipReason":null},"line":null,"column":null,"url":null},"type":"testStart","time":1}\n'
            b'{"count":1,"time":6,"type":"allSuites"}\n'
            b'Shell: [INFO:flutter/shell/common/switches.cc(263)] VM Service port specified was malformed. Will default to 0\n'
            b'\n'
            b'[{"event":"test.startedProcess","params":{"vmServiceUri":"<diagnostic-uri-redacted>"}}]\n'
            b'{"testID":1,"result":"success","skipped":false,"hidden":true,"type":"testDone","time":19771}\n'
            b'{"group":{"id":2,"suiteID":0,"parentID":null,"name":"","metadata":{"skip":false,"skipReason":null},"testCount":1,"line":null,"column":null,"url":null},"type":"group","time":19773}\n'
            b'{"test":{"id":3,"name":"wrong-peer wrong-recipient malformed and offline typing never enters history or the active header","suiteID":0,"groupIDs":[2],"metadata":{"skip":false,"skipReason":null},"line":174,"column":5,"url":"package:flutter_test/src/widget_tester.dart","root_line":105,"root_column":3,"root_url":"file:///home/runner/work/_temp/mobile-current363-input/source/app/test/features/chat/chat_typing_workflow_test.dart"},"type":"testStart","time":19774}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_typing_negative mounted","type":"print","time":20956}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_typing_negative incoming.complete","type":"print","time":21077}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_typing_negative body.checked","type":"print","time":21090}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_typing_negative events.cancel.begin","type":"print","time":21090}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_typing_negative events.cancel.end","type":"print","time":21091}\n'
            b'{"testID":3,"messageType":"print","message":"group_revoke_cleanup sdk.begin","type":"print","time":21092}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_sdk_dispose session.begin","type":"print","time":21093}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_sdk_dispose session.end","type":"print","time":21096}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_sdk_dispose gateway.begin","type":"print","time":21096}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_sdk_dispose gateway.end","type":"print","time":21098}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_sdk_dispose handler.cleared","type":"print","time":21098}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_sdk_dispose storage.begin","type":"print","time":21098}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_sdk_dispose storage.end","type":"print","time":21102}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_actual sdk.wrapper.end","type":"print","time":21102}\n'
            b'{"testID":3,"messageType":"print","message":"group_revoke_cleanup sdk.end","type":"print","time":21102}\n'
            b'{"testID":3,"messageType":"print","message":"group_revoke_cleanup unmount.begin","type":"print","time":21102}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_unmount pumpWidget.before","type":"print","time":21103}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_unmount pumpWidget.returned","type":"print","time":21151}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_unmount pumpWidget.after","type":"print","time":21151}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_unmount pumpAndSettle.before","type":"print","time":21152}\n'
            b'{"testID":3,"messageType":"print","message":"chat_input_unmount pumpAndSettle.after","type":"print","time":21153}\n'
            b'{"testID":3,"messageType":"print","message":"group_revoke_cleanup unmount.end","type":"print","time":21153}\n'
            b'{"testID":3,"result":"success","skipped":false,"hidden":false,"type":"testDone","time":21166}\n'
            b'{"success":true,"type":"done","time":21201}\n'
        )
        self.assertEqual(hashlib.sha256(raw).hexdigest(),'fd7086564f4b3386fe3dd24c333822819e1b5cdd92588f0bdeb7e167c1cdb242')
        cfg=json.loads((v.ROOT/'ci/mobile-input/inputs.json').read_bytes())
        summary=v.selected_widget_summary(raw.decode(),cfg)
        self.assertEqual(summary['tests']['passed'],1);self.assertEqual(summary['tests']['hidden'],1)
        self.assertTrue(summary['tests']['complete_success_footer']);self.assertTrue(summary['cleanup']['exact_owned_four_marker_sequences'])
        # Historical replay proves this selected widget only. Original72/0/0
        # and ValueError cannot be upgraded with an invented child-exit field.
        historical={'originalChildExitCode':None,'samplerJoined':True,'selectedMachineComplete':True,'defaultNoVMServiceAccepted':False,'samplerTerminal':'failed','attemptedSamples':0,'acceptedSamples':0,'exceptionType':'ValueError'}
        self.assertEqual(v.terminal_state(historical),'refused')
        historical['originalChildExitCode']=0;historical['completionRequested']=True
        self.assertEqual(v.terminal_state(historical),'refused')
        external=threading.Event();complete=threading.Event();stop=v.CompletionStop(external,complete)
        self.assertFalse(stop.is_set());complete.set()
        with self.assertRaises(v._OriginalComplete):stop.is_set()
        external.set();self.assertTrue(stop.is_set())
        with tempfile.TemporaryDirectory() as folder:
            work=Path(folder).resolve();progress={}
            with mock.patch.object(v,'socket_owner') as owner,mock.patch.object(v,'ReadOnlyVM') as client:
                with self.assertRaises(v._OriginalComplete):
                    v.collect('http://127.0.0.1:40123/opaqueAuthCode=/',types.SimpleNamespace(poll=lambda:None),work,work/'unused-private',threading.Event(),complete,progress)
                owner.assert_not_called();client.assert_not_called()
            self.assertFalse(progress['socketOwnerVerified']);self.assertFalse(progress['getVMVerified'])
        # Controlled outcomes are future policy controls, not historical facts.
        completed={'originalChildExitCode':0,'samplerJoined':True,'selectedMachineComplete':True,'defaultNoVMServiceAccepted':False,'samplerTerminal':'completed-stop','captureStarted':False,'completionRequested':True,'attemptedSamples':0,'acceptedSamples':0,'failureLatched':False,'socketOwnerVerified':False,'getVMVerified':False,'externalStopObserved':False}
        self.assertEqual(v.terminal_state(completed),'completed-only')
        self.assertEqual(v.terminal_state(dict(completed,captureStarted=True,attemptedSamples=1,acceptedSamples=1,socketOwnerVerified=True,getVMVerified=True)),'completed-only')
        for changes in ({'originalChildExitCode':1},{'originalChildExitCode':None},{'samplerJoined':False},{'selectedMachineComplete':False},{'defaultNoVMServiceAccepted':True},{'completionRequested':False},{'externalStopObserved':True},{'attemptedSamples':1,'acceptedSamples':0},{'samplerTerminal':'failed'},{'exceptionType':'ValueError'}):
            with self.subTest(changes=changes):self.assertEqual(v.terminal_state(dict(completed,**changes)),'refused')
        original={'pid':123,'start':20,'uid':os.getuid(),'pgrp':55,'session':55,'exe':'/owned/flutter_tester','ticks':100}
        for failure_first in (False,True):
            with self.subTest(failure_first=failure_first),tempfile.TemporaryDirectory() as folder:
                work=Path(folder).resolve();private=work/'private';private.mkdir(mode=0o700);(work/'evidence').mkdir()
                v.write_private(private/'original-child.json',original)
                completion=threading.Event();progress={};clients=[]
                class Client:
                    def __init__(self,port,path,check,deadline):self.check=check;self.closed=False;clients.append(self);check()
                    def rpc(self,method,params):
                        self.check()
                        if method=='getVM':return {'type':'VM','pid':123,'isolates':[{'name':'main','id':'isolates/1'}]}
                        completion.set()
                        if failure_first:raise ValueError('service-closed')
                        # Matches ReadOnlyVM's real post-response safe owner check.
                        self.check()
                        return {'type':'Stack','frames':[],'asyncCausalFrames':[]}
                    def close(self):self.closed=True
                with mock.patch.object(v,'socket_owner',return_value=original),mock.patch.object(v,'ReadOnlyVM',Client):
                    with self.assertRaises(v._SamplerFailure if failure_first else v._OriginalComplete):
                        v.collect('http://127.0.0.1:40123/opaqueAuthCode=/',types.SimpleNamespace(pid=123,poll=lambda:None),work,private,threading.Event(),completion,progress)
                self.assertTrue(all(client.closed for client in clients));self.assertTrue(progress['socketOwnerVerified']);self.assertTrue(progress['getVMVerified'])
                self.assertEqual(progress['failureLatched'],failure_first)
                if failure_first:self.assertEqual(progress['terminalFailureReason'],'service-closed')
                current=dict(completed,captureStarted=True,**progress)
                if failure_first:current['samplerTerminal']='failed'
                self.assertEqual(v.terminal_state(current),'refused' if failure_first else 'completed-only')
        phase={'phase':'input-typing-vm-diagnostic1','expected_names':[v.TARGET],'expected_tests':1,'accepted':True,'latched_failure':None,'summary':summary,
               'source_post':{'accepted':True,'source363':cfg['source_digest'],'portable415':cfg['portable_digest'],'authored_bodies':415,'current_inventory_exact':True}}
        def written(diagnostic,selected):
            with tempfile.TemporaryDirectory() as folder:
                work=Path(folder).resolve();(work/'evidence').mkdir()
                v.write_private(work/'evidence/input-vm-wait-diagnostic.json',diagnostic)
                v.write_terminal_pass(work,[selected],cfg['source_digest'],cfg['portable_digest'])
                evidence=work/'evidence';files={p.name:json.loads(p.read_bytes()) for p in evidence.iterdir()}
                self.assertNotIn('input-scoped-passed.json',files)
                return files
        files=written(completed,phase);record=files['input-vm-completed-test.json']
        self.assertTrue(record['selectedTestPassed']);self.assertEqual(record['vmCapture'],'not_needed_after_complete')
        self.assertFalse(record['profiledTestPassed']);self.assertFalse(record['defaultNoVMServiceAccepted'])
        self.assertEqual((record['vmWait']['attemptedSamples'],record['vmWait']['acceptedSamples']),(0,0));self.assertNotIn('input-vm-profiled-passed.json',files)
        captured=dict(completed,samplerTerminal='captured',captureStarted=True,attemptedSamples=2,acceptedSamples=2,socketOwnerVerified=True,getVMVerified=True)
        files=written(captured,phase);self.assertIn('input-vm-profiled-passed.json',files);self.assertNotIn('input-vm-completed-test.json',files)
        self.assertEqual(v.terminal_state(dict(captured,exceptionType='ValueError')),'refused')
        for selected in [dict(phase,latched_failure='phase_timeout'),dict(phase,accepted=False),dict(phase,phase='input-typing-diagnostic1'),dict(phase,source_post=dict(phase['source_post'],accepted=False)),dict(phase,source_post=dict(phase['source_post'],source363='0'*64))]:
            with self.subTest(phase=selected),self.assertRaises(ValueError):written(completed,selected)
        events=[]
        for line in raw.decode().splitlines():
            try:value=json.loads(line)
            except ValueError:continue
            if isinstance(value,dict):events.append(value)
        variants=[]
        variants.append([e for e in events if e.get('type')!='done'])
        variants.append([dict(e,success=False) if e.get('type')=='done' else e for e in events])
        variants.append([*events[:-1],next(e for e in events if e.get('type')=='testStart' and e.get('test',{}).get('name')==v.TARGET),events[-1]])
        variants.append([dict(e,testID=9999) if e.get('type')=='testDone' and e.get('hidden') is False else e for e in events])
        variants.append([dict(e,skipped=True) if e.get('type')=='testDone' and e.get('hidden') is False else e for e in events])
        variants.append([*events[:-1],{'type':'error','testID':3,'error':'PRIVATE_DO_NOT_EXPORT'},events[-1]])
        variants.append([e for e in events if e.get('message')!='group_revoke_cleanup sdk.end'])
        variants.append([*events[:-1],{'type':'print','testID':3,'message':'A RenderFlex overflowed by 10 pixels'},events[-1]])
        for rows in variants:
            with self.subTest(variant=len(rows)),self.assertRaises(ValueError):v.selected_widget_summary('\n'.join(json.dumps(e) for e in rows),cfg)

if __name__=='__main__':unittest.main(verbosity=2)
