"""Task-only actual HTTPS delegation checks; invoke only after new binaries are ready.
No production target, account creation, role mutation, or credential output.
--logout-probe revokes only a newly logged-in disposable test session, never the UI session.
"""
from pathlib import Path
import base64, hashlib, hmac, json, os, secrets, ssl, struct, sys, time
import urllib.request, urllib.error, urllib.parse, socket

project = Path(__file__).resolve().parents[2]
private = project.parent / 'references/integration/stack-config'
opener = urllib.request.build_opener(urllib.request.ProxyHandler({'https':'http://127.0.0.1:38081'}),
    urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(project.parent/'references/integration/tls/ca.pem'))))
normal = json.loads((private/'parity-normal-admin.json').read_text())
super_saved = json.loads((private/'parity-admin-session.json').read_text())
bootstrap = json.loads((private/'credentials.json').read_text())
users = json.loads((private/'root-business-accounts.json').read_text())['users']
results = []
checkpoint = private/'parity-delegation-probe.json'
public = project/'docs/architecture/admin-delegation-live.json'
resume = '--resume-current' in sys.argv[1:]
if checkpoint.exists() and not resume:
    raise SystemExit('Private probe sessions already exist; use --resume-current to avoid repeated logins.')
state = json.loads(checkpoint.read_text()) if resume else {'phase':'started','fixtureOnly':True}
if resume and public.exists(): results = json.loads(public.read_text()).get('results',[])

def save():
    with os.fdopen(os.open(checkpoint,os.O_CREAT|os.O_TRUNC|os.O_WRONLY,0o600),'w') as f: json.dump(state,f)

def call(label,path,body,token=None):
    headers={'Content-Type':'application/json','operationID':secrets.token_hex(16)}
    if token: headers['token']=token
    request=urllib.request.Request('https://admin.luobo.fixture.test'+path,json.dumps(body).encode(),headers,method='POST')
    start=time.monotonic()
    try: response=opener.open(request,timeout=20)
    except urllib.error.HTTPError as error: response=error
    with response:
        value=json.load(response)
        results.append({'case':label,'path':path,'httpStatus':response.status,'errCode':value.get('errCode'),'elapsedSeconds':round(time.monotonic()-start,3)})
    return value

def expect(label,path,body,token,code=0):
    value=call(label,path,body,token)
    assert value.get('errCode')==code, f'{label}: expected{code}, observed{value.get("errCode")}'
    return value.get('data') or {}

def denied(label,path,body,token):
    value=call(label,path,body,token)
    assert value.get('errCode') in [1002,1501,1502,1503,1504,1505,1506,1507], label+' must return an authorization denial'

def websocket_denied(label,session):
    # Real TLS Upgrade request via the exact task CONNECT proxy; no message frame is sent.
    context=ssl.create_default_context(cafile=str(project.parent/'references/integration/tls/ca.pem'))
    sock=socket.create_connection(('127.0.0.1',38081),timeout=5)
    try:
        sock.sendall(b'CONNECT luobo.fixture.test:443 HTTP/1.1\r\nHost: luobo.fixture.test:443\r\n\r\n')
        connect=b''
        while b'\r\n\r\n' not in connect: connect+=sock.recv(1024)
        assert connect.split(b'\r\n',1)[0].split()[1]==b'200'
        with context.wrap_socket(sock,server_hostname='luobo.fixture.test') as channel:
            query=urllib.parse.urlencode({'sendID':session['imUserID'],'platformID':10,'token':session['imToken'],'operationID':secrets.token_hex(16)})
            request=('GET /msg_gateway?'+query+' HTTP/1.1\r\nHost: luobo.fixture.test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: '+base64.b64encode(secrets.token_bytes(16)).decode()+'\r\n\r\n')
            channel.sendall(request.encode());response=b''
            while b'\r\n\r\n' not in response and len(response)<16384:
                chunk=channel.recv(4096)
                if not chunk: break
                response+=chunk
            headers,body=response.split(b'\r\n\r\n',1)
            status=int(headers.split(b'\r\n',1)[0].split()[1])
            length=next((int(line.split(b':',1)[1]) for line in headers.split(b'\r\n')[1:] if line.lower().startswith(b'content-length:')),len(body))
            while len(body)<min(length,16384):
                chunk=channel.recv(4096)
                if not chunk: break
                body+=chunk
            try: code=json.loads(body).get('errCode')
            except (ValueError,TypeError): code=None
            results.append({'case':label,'path':'/msg_gateway','httpStatus':status,'errCode':code,'messageFramesSent':0})
            assert status!=101 and code in [1002,1501,1502,1503,1504,1505,1506,1507],label+' must reject management WebSocket'
    finally: sock.close()

def totp(secret):
    key=base64.b32decode(secret+'='*(-len(secret)%8),casefold=True)
    value=hmac.new(key,struct.pack('>Q',int(time.time())//30),hashlib.sha1).digest();offset=value[-1]&15
    return f'{(struct.unpack(">I",value[offset:offset+4])[0]&0x7fffffff)%1000000:06d}'

def contains_credential(value):
    if isinstance(value,dict):
        return any((key in ['token','imToken','adminToken'] and bool(item)) or contains_credential(item) for key,item in value.items())
    return isinstance(value,list) and any(contains_credential(item) for item in value)

try:
    for role,account,password_wire,secret in ([] if resume else [
        ('normal',normal['account'],normal['passwordWire'],normal['totp_secret']),
        ('super',bootstrap['admin_id'],hashlib.md5(bootstrap['admin_password'].encode()).hexdigest(),super_saved['totp_secret'])]):
        session=expect(role+'_fresh_password_TOTP','/complete_admin/account/login',
            {'account':account,'password':password_wire,'googleAuthCode':totp(secret)},None)
        assert session.get('adminToken') and session.get('imToken')
        state[role]=session;save()
    target=[u['userID'] for u in users]
    search={'sendID':target[1],'recvID':target[0],'sessionType':1,'msgType':101,'sendTime':'','pagination':{'pageNumber':1,'showNumber':10}}
    for role in ['normal','super']:
        session=state[role];token=session['imToken']
        expect(role+'_management_user_read','/api/user/get_users',{'pagination':{'pageNumber':1,'showNumber':10}},token)
        detail=expect(role+'_online_summary','/api/user/get_users_online_token_detail',{'userIDs':target},token)
        assert not contains_credential(detail), 'online summary leaked a credential'
        results[-1]['credentialFree']=True
        expect(role+'_management_sensitive_read','/api/sensitive_word/search',{'keyword':'luobo_qa_sensitive_','pageNumber':1,'showNumber':20},token)
        denied(role+'_direct_history_denied','/api/msg/search_msg',search,token)
        denied(role+'_direct_conversation_denied','/api/conversation/get_conversations_list',{'userID':target[0],'pagination':{'pageNumber':1,'showNumber':10}},token)
        denied(role+'_direct_encryption_denied','/api/encryption/get_encryption_key',{'conversationID':'si_'+ '_'.join(sorted(target))},token)
        expect(role+'_audited_search','/complete_admin/admin/message_search',search,session['adminToken'],1002 if role=='normal' else 0)
        websocket_denied(role+'_management_WebSocket_denied',session)
    denied('old_shared_IM_token_denied','/api/msg/search_msg',search,super_saved['login']['imToken'])
    expect('unrelated_UI_admin_session_retained','/complete_admin/account/info',{},normal['login']['adminToken'])
    if '--logout-probe' in sys.argv[1:]:
        captured=state['normal'];state['phase']='before_exact_probe_logout';save()
        assert expect('revoke_only_probe_session','/complete_admin/account/logout',{},captured['adminToken']).get('revoked') is True
        denied('revoked_chart_session_denied','/complete_admin/account/info',{},captured['adminToken'])
        denied('revoked_delegate_denied','/api/user/get_users',{'pagination':{'pageNumber':1,'showNumber':10}},captured['imToken'])
        assert expect('idempotent_probe_logout','/complete_admin/account/logout',{},captured['adminToken']).get('revoked') is True
        expect('original_UI_session_survives_probe_logout','/complete_admin/account/info',{},normal['login']['adminToken'])
        expect('other_super_session_survives_probe_logout','/complete_admin/account/info',{},state['super']['adminToken'])
    state['phase']='passed';save()
finally:
    public.write_text(json.dumps({'schema_version':1,'date':time.strftime('%Y-%m-%dT%H:%M:%S'),'phase':state['phase'],
        'transport':'real password/TOTP, task CA+hostname HTTPS via exact CONNECT','results':results,
        'scope':'synthetic fixture accounts/conversation only; no account/role/tenant mutation; optional logout affects fresh probe session only',
        'limits':'UI rendering and all89 workflow acceptance remain separate.'},ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'phase':state['phase'],'cases':len(results),'codes':[r['errCode'] for r in results]}))
