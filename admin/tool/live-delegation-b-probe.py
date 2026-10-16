"""Task-only B guarded deployment acceptance. No business writes or new users.

Uses existing actual B password/MFA and a dedicated private session checkpoint.
Never prints credentials. Requires the exact B HTTPS /complete_admin fixture map.
"""
from pathlib import Path
import base64, hashlib, hmac, json, os, secrets, socket, ssl, struct, time
import urllib.request, urllib.error, urllib.parse

project = Path(__file__).resolve().parents[2]
integration = project.parent / 'references/integration'
base = integration / 'tenant-b'
state_path = base / 'delegation-live-session.json'
public_path = project / 'docs/architecture/admin-delegation-b-live.json'
context = ssl.create_default_context(cafile=str(integration/'tls/ca.pem'))
opener = urllib.request.build_opener(urllib.request.ProxyHandler({'https':'http://127.0.0.1:38081'}),
                                    urllib.request.HTTPSHandler(context=context))
credentials = json.loads((base/'credentials.json').read_text())
old = json.loads((base/'admin-session.json').read_text())
a = json.loads((integration/'stack-config/parity-delegation-probe.json').read_text())['super']
users = json.loads((base/'business-users.json').read_text())['users']
state = json.loads(state_path.read_text()) if state_path.exists() else {'phase':'started','fixtureOnly':True}
results = []
if public_path.exists(): results=json.loads(public_path.read_text()).get('results',[])

def save():
    with os.fdopen(os.open(state_path,os.O_CREAT|os.O_TRUNC|os.O_WRONLY,0o600),'w') as f:
        json.dump(state,f)

def call(case,host,path,body,token=None,code=0):
    headers={'Content-Type':'application/json','operationID':secrets.token_hex(16)}
    if token: headers['token']=token
    request=urllib.request.Request('https://'+host+path,json.dumps(body).encode(),headers,method='POST')
    start=time.monotonic()
    try: response=opener.open(request,timeout=20)
    except urllib.error.HTTPError as error: response=error
    with response:
        raw=response.read()
        try: value=json.loads(raw)
        except ValueError: raise RuntimeError(case+': HTTPS endpoint did not return API JSON') from None
        results.append({'case':case,'host':host,'path':path,'httpStatus':response.status,
                        'errCode':value.get('errCode'),'elapsedSeconds':round(time.monotonic()-start,3)})
    actual=value.get('errCode')
    if code=='denied': assert actual in [1002,1501,1502,1503,1504,1505,1506,1507],case+' must deny'
    else: assert actual==code,case+': unexpected API code '+str(actual)
    return value.get('data') or {}

def totp(secret):
    key=base64.b32decode(secret+'='*(-len(secret)%8),casefold=True)
    digest=hmac.new(key,struct.pack('>Q',int(time.time())//30),hashlib.sha1).digest()
    offset=digest[-1]&15
    return f'{(struct.unpack(">I",digest[offset:offset+4])[0]&0x7fffffff)%1000000:06d}'

def claims(token):
    encoded=token.split('.')[1]
    return json.loads(base64.urlsafe_b64decode(encoded+'='*(-len(encoded)%4)))

def no_credentials(value):
    if isinstance(value,dict): return all(not (k in ['token','imToken','adminToken'] and v) and no_credentials(v)for k,v in value.items())
    if isinstance(value,list): return all(no_credentials(v)for v in value)
    return True

def websocket_denied(session):
    host='tenant-b.luobo.fixture.test'; sock=socket.create_connection(('127.0.0.1',38081),timeout=5)
    try:
        sock.sendall(('CONNECT '+host+':443 HTTP/1.1\r\nHost: '+host+':443\r\n\r\n').encode())
        connect=b''
        while b'\r\n\r\n' not in connect: connect+=sock.recv(1024)
        assert connect.split(b'\r\n',1)[0].split()[1]==b'200'
        with context.wrap_socket(sock,server_hostname=host)as channel:
            query=urllib.parse.urlencode({'sendID':session['imUserID'],'platformID':10,'token':session['imToken'],'operationID':secrets.token_hex(16)})
            channel.sendall(('GET /msg_gateway?'+query+' HTTP/1.1\r\nHost: '+host+'\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: '+base64.b64encode(secrets.token_bytes(16)).decode()+'\r\n\r\n').encode())
            response=b''
            while b'\r\n\r\n'not in response and len(response)<16384:
                part=channel.recv(4096)
                if not part:break
                response+=part
            headers,body=response.split(b'\r\n\r\n',1);status=int(headers.split(b'\r\n',1)[0].split()[1])
            size=next((int(line.split(b':',1)[1])for line in headers.split(b'\r\n')[1:]if line.lower().startswith(b'content-length:')),len(body))
            while len(body)<min(size,16384):
                part=channel.recv(4096)
                if not part:break
                body+=part
            code=json.loads(body).get('errCode');results.append({'case':'B_management_WS_denied','host':host,'path':'/msg_gateway','httpStatus':status,'errCode':code,'messageFramesSent':0})
            assert status!=101 and code in [1002,1501,1502,1503,1504,1505,1506,1507]
    finally:sock.close()

try:
    if state['phase']=='passed': raise SystemExit('B dedicated acceptance already passed; do not repeat login or logout.')
    if 'session'not in state:
        state['session']=call('B_actual_password_MFA','tenant-b.luobo.fixture.test','/complete_admin/account/login',
            {'account':credentials['admin_id'],'password':hashlib.md5(credentials['admin_password'].encode()).hexdigest(),'googleAuthCode':totp(old['totp_secret'])})
        state['phase']='fresh_MFA_authenticated';save()
    b=state['session'];bc=claims(b['imToken']);ac=claims(a['imToken'])
    assert bc.get('iss')=='luobo-synthetic-tenant-b' and ac.get('iss')=='luobo-synthetic-tenant-a'
    state['claimSummary']={'Bissuer':bc['iss'],'Aissuer':ac['iss'],'differentIssuers':True};save()
    page={'pagination':{'pageNumber':1,'showNumber':10}}
    call('B_management_user_read','tenant-b.luobo.fixture.test','/api/user/get_users',page,b['imToken'])
    detail=call('B_online_summary','tenant-b.luobo.fixture.test','/api/user/get_users_online_token_detail',{'userIDs':[u['userID']for u in users]},b['imToken'])
    assert no_credentials(detail);results[-1]['credentialFree']=True
    call('B_management_sensitive_read','tenant-b.luobo.fixture.test','/api/sensitive_word/search',{'keyword':'','pageNumber':1,'showNumber':20},b['imToken'])
    search={'sendID':users[1]['userID'],'recvID':users[0]['userID'],'sessionType':1,'msgType':101,'sendTime':'','pagination':{'pageNumber':1,'showNumber':10}}
    call('B_direct_history_denied','tenant-b.luobo.fixture.test','/api/msg/search_msg',search,b['imToken'],'denied')
    call('B_direct_conversation_denied','tenant-b.luobo.fixture.test','/api/conversation/get_conversations_list',{'userID':users[0]['userID'],**page},b['imToken'],'denied')
    call('B_direct_key_denied','tenant-b.luobo.fixture.test','/api/encryption/get_encryption_key',{'conversationID':'si_'+'_'.join(sorted(u['userID']for u in users))},b['imToken'],'denied')
    call('B_super_audited_search','tenant-b.luobo.fixture.test','/complete_admin/admin/message_search',search,b['adminToken'])
    websocket_denied(b)
    call('A_delegate_rejected_by_B','tenant-b.luobo.fixture.test','/api/user/get_users',page,a['imToken'],'denied')
    call('B_delegate_rejected_by_A','luobo.fixture.test','/api/user/get_users',page,b['imToken'],'denied')
    call('A_chart_rejected_by_B','tenant-b.luobo.fixture.test','/complete_admin/account/info',{},a['adminToken'],'denied')
    call('B_chart_rejected_by_A','admin.luobo.fixture.test','/complete_admin/account/info',{},b['adminToken'],'denied')
    call('B_old_shared_IM_bearer_denied','tenant-b.luobo.fixture.test','/api/user/get_users',page,old['login']['imToken'],'denied')
    call('B_original_chart_session_retained','tenant-b.luobo.fixture.test','/complete_admin/account/info',{},old['login']['adminToken'])
    call('A_existing_super_session_retained','admin.luobo.fixture.test','/complete_admin/account/info',{},a['adminToken'])
    state['phase']='passed';save()
finally:
    public_path.write_text(json.dumps({'schema_version':1,'date':time.strftime('%Y-%m-%dT%H:%M:%S'),'state':state['phase'],
        'transport':'Actual existing password+TOTP, task CA+hostname HTTPS via exact CONNECT; real TLS WebSocket Upgrade with0frames',
        'claimSummary':state.get('claimSummary'),'results':results,
        'limits':['B existing superadmin only; normal80 denial is separately accepted on A.','No business writes, new users, credential changes, DB reset or complete89 workflows.','A/B keys and namespaces are independent; bidirectional real issued-token rejection is tested, not a forged same-key issuer experiment.']},ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'state':state['phase'],'cases':len(results),'codes':[r['errCode']for r in results]}))
