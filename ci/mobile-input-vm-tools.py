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

if __name__=='__main__':unittest.main(verbosity=2)
