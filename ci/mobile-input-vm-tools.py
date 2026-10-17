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
                    for _ in range(3):
                        a,b=read(sock,2);self.assertTrue(b&128);size=b&127
                        if size==126:size=struct.unpack('!H',read(sock,2))[0]
                        mask=read(sock,4);request=json.loads(bytes(x^mask[i%4] for i,x in enumerate(read(sock,size))));seen.append(request['method'])
                        result={'type':'VM','pid':123,'isolates':[]} if request['method']=='getVM' else {'type':'Isolate','runnable':True,'pauseEvent':{'kind':'Resume','exception':{'valueAsString':'LOCAL-SECRET'}}} if request['method']=='getIsolate' else {'type':'Stack','frames':[{'function':{'name':'WidgetTester.pumpWidget'},'vars':[{'value':'LOCAL-SECRET'}],'location':{'script':{'uri':'package:flutter_test/src/widget_tester.dart'},'line':600}}],'objects':'LOCAL-SECRET'}
                        data=json.dumps({'jsonrpc':'2.0','id':request['id'],'result':result}).encode();sock.sendall(bytes([129,126])+struct.pack('!H',len(data))+data)
            except Exception as error:errors.append(type(error).__name__)
            finally:listener.close()
        thread=threading.Thread(target=serve);thread.start();client=None
        try:
            checked=[];client=v.ReadOnlyVM(port,'/opaqueAuthCode=/ws',lambda:checked.append(True),time.monotonic()+10)
            client.rpc('getVM',{});isolate=client.rpc('getIsolate',{'isolateId':'isolates/1'});stack=client.rpc('getStack',{'isolateId':'isolates/1','limit':32});safe=v.safe_sample(isolate,stack)
            self.assertNotIn('LOCAL-SECRET',json.dumps(safe));self.assertEqual(safe['frames'][0]['function'],'WidgetTester.pumpWidget')
            for method,params in [('evaluate',{}),('pause',{}),('getObject',{}),('getStack',{'isolateId':'isolates/1','limit':1000})]:
                with self.subTest(method=method),self.assertRaises(ValueError):client.rpc(method,params)
            self.assertEqual(seen,['getVM','getIsolate','getStack']);self.assertGreaterEqual(len(checked),6)
        finally:
            if client:client.close()
            thread.join(4)
        self.assertFalse(thread.is_alive());self.assertEqual(errors,[])

    def test_production_sampler_writes_only_two_filtered_records_and_never_default_pass(self):
        original={'pid':123,'start':20,'uid':os.getuid(),'pgrp':55,'session':55,'exe':'/owned/flutter_tester','ticks':100}
        ticks=[100]
        def owner(*args,**kwargs):ticks[0]+=1;return dict(original,ticks=ticks[0])
        class Client:
            def __init__(self,port,path,check,deadline):check();self.check=check
            def rpc(self,method,params):
                self.check()
                if method=='getVM':return {'type':'VM','pid':123,'isolates':[{'name':'main','id':'isolates/1'}]}
                if method=='getIsolate':return {'type':'Isolate','runnable':True,'pauseEvent':{'kind':'Resume','exception':'SECRET'}}
                return {'type':'Stack','frames':[{'function':{'name':'pumpWidget'},'vars':['SECRET']}],'asyncCausalFrames':[]}
            def close(self):pass
        with tempfile.TemporaryDirectory() as folder:
            work=Path(folder).resolve();private=work/'private';private.mkdir(mode=0o700);(work/'evidence').mkdir();v.write_private(private/'original-child.json',original)
            child=types.SimpleNamespace(pid=123,poll=lambda:None)
            with mock.patch.object(v,'socket_owner',side_effect=owner),mock.patch.object(v,'ReadOnlyVM',Client):
                result=v.collect('http://127.0.0.1:40123/opaqueAuthCode=/',child,work,private,threading.Event())
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

if __name__=='__main__':unittest.main(verbosity=2)
