#!/usr/bin/env python3
"""Fresh, exact-owned cloud resources. Never imports runtime Ready/hold DTOs.

Image acquisition belongs to an earlier reviewed cloud phase. This module
requires local digest-pinned images and uses --pull=never; it never installs,
downloads, mounts the Docker socket, or changes the runner's HOME.
"""
import hashlib
import errno
import base64
import json
import os
from pathlib import Path
import re
import secrets
import stat
import subprocess
import time
import uuid

ROLES = ('keeper', 'mongo', 'redis', 'zookeeper', 'kafka', 'minio')

def sha(raw): return hashlib.sha256(raw).hexdigest()


def canonical_repo_digest(value):
    # Docker normalizes official-library spellings in RepoDigests. Only these
    # Docker Hub aliases are equivalent; repository and platform digest remain
    # exact. A tag, registry port, upper-case name or non-digest is not accepted.
    if not isinstance(value,str) or not re.fullmatch(r'[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*@sha256:[0-9a-f]{64}',value):
        raise ValueError('canonical reviewed repository digest required')
    repository,digest=value.split('@',1)
    parts=repository.split('/')
    if parts[0] in ('docker.io','index.docker.io'):
        parts=parts[1:]
        if len(parts)==1:parts=['library',*parts]
        return 'docker.io/'+'/'.join(parts)+'@'+digest
    if len(parts)==1:return 'docker.io/library/'+repository+'@'+digest
    # An unqualified two-part repository is Docker Hub. Dotted first labels
    # denote another registry and are preserved, never treated as Docker Hub.
    if '.' not in parts[0]:return 'docker.io/'+repository+'@'+digest
    return repository+'@'+digest

def require_repo_digest(inspected,reviewed):
    expected=canonical_repo_digest(reviewed)
    observed=inspected.get('RepoDigests')
    if not isinstance(observed,list) or not any(canonical_repo_digest(row)==expected for row in observed):
        raise ValueError('local reviewed image digest differs')

def private_write(path, raw):
    if not isinstance(raw, bytes): raw = json.dumps(raw, separators=(',', ':')).encode()+b'\n'
    fd = os.open(path, os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW, 0o600)
    try:
        with os.fdopen(fd, 'wb') as out:
            out.write(raw); out.flush(); os.fsync(out.fileno())
    except BaseException:
        raise
    directory = os.open(Path(path).parent, os.O_RDONLY|os.O_DIRECTORY)
    try: os.fsync(directory)
    finally: os.close(directory)
    return sha(raw)

def private_dir(path):
    path = Path(path)
    if not path.is_absolute() or path.resolve() != path: raise ValueError('canonical owned path required')
    path.mkdir(mode=0o700)
    if stat.S_IMODE(path.stat().st_mode) != 0o700 or path.stat().st_uid != os.getuid(): raise ValueError('private owned path required')
    return path

def stable_source_file(path, maximum, expected_size=None, private=False):
    path=Path(path)
    if not path.is_absolute() or path.resolve()!=path:raise ValueError('canonical source file required')
    before=path.lstat()
    if not stat.S_ISREG(before.st_mode) or before.st_uid!=os.getuid() or before.st_nlink!=1 or not 0<before.st_size<=maximum or expected_size is not None and before.st_size!=expected_size:raise ValueError('source file custody differs')
    if private and (stat.S_IMODE(before.st_mode)!=0o600 or stat.S_IMODE(path.parent.stat().st_mode)!=0o700):raise ValueError('private source receipt required')
    fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW)
    try:
        opened=os.fstat(fd)
        if (opened.st_dev,opened.st_ino)!=(before.st_dev,before.st_ino):raise ValueError('source open identity differs')
        chunks=[];left=maximum+1
        while left:
            part=os.read(fd,min(1<<20,left))
            if not part:break
            chunks.append(part);left-=len(part)
        body=b''.join(chunks);after=os.fstat(fd);current=path.lstat()
        if len(body)!=before.st_size or (before.st_dev,before.st_ino,before.st_size,before.st_mtime_ns,before.st_mode)!=(after.st_dev,after.st_ino,after.st_size,after.st_mtime_ns,after.st_mode) or (current.st_dev,current.st_ino)!=(before.st_dev,before.st_ino):raise ValueError('source body changed')
        return body
    finally:os.close(fd)

def validate_source_minio(value):
    keys={'version','path','SHA256','bytes','receiptPath','receiptSHA256','repository','commit','sourceDigestSHA256','goModSHA256','goSumSHA256','toolchainSHA256','cgoEnabled'}
    if set(value)!=keys or value['version']!='owned-minio-source-elf-v1' or value['repository']!='https://github.com/minio/minio' or value['cgoEnabled']!='0' or not re.fullmatch('[0-9a-f]{40}',value['commit']):raise ValueError('reviewed source-MinIO identity required')
    for key in ('SHA256','receiptSHA256','sourceDigestSHA256','goModSHA256','goSumSHA256','toolchainSHA256'):
        if not re.fullmatch('[0-9a-f]{64}',value[key]):raise ValueError('source-MinIO SHA required')
    body=stable_source_file(value['path'],256<<20,value['bytes'])
    if sha(body)!=value['SHA256'] or len(body)<20 or body[:6]!=b'\x7fELF\x02\x01' or body[18:20]!=b'\x3e\x00':raise ValueError('actual source-MinIO ELF differs')
    receipt=stable_source_file(value['receiptPath'],65536,private=True)
    if sha(receipt)!=value['receiptSHA256']:raise ValueError('source-MinIO build receipt differs')
    proof={'version':'owned-minio-source-build-receipt-v1'}
    proof.update({key:value[key] for key in ('repository','commit','sourceDigestSHA256','goModSHA256','goSumSHA256','toolchainSHA256','cgoEnabled')})
    proof.update(binarySHA256=value['SHA256'],binaryBytes=value['bytes'])
    if json.loads(receipt)!=proof or receipt!=json.dumps(proof,separators=(',',':')).encode()+b'\n':raise ValueError('source-MinIO receipt fields differ')
    return value

def redis_acl(credentials, nonce):
    # This is the finite fresh-graph fixture account policy, never a production
    # default. Only admin can manage users/server state. These two read-only
    # subcommands retain the original physical identity/exclusion proof.
    if not isinstance(nonce, str) or not re.fullmatch('[0-9a-f]{32}', nonce):
        raise ValueError('exact fresh plane nonce required')
    suffix = nonce[:16]
    for role in ('Admin', 'Legacy', 'Runtime'):
        if credentials.get('redis'+role+'User') != 'owned_'+role.lower()+'_'+suffix or not isinstance(credentials.get('redis'+role+'Password'), str) or not re.fullmatch('[0-9a-f]{48}', credentials['redis'+role+'Password']):
            raise ValueError('original fresh Redis principal shape required')
    manager, token = 'fullgraph_manager_'+nonce, 'fullgraph_token_'+nonce
    keys = [prefix+user for prefix in ('USER_INFO:', 'USER_GLOBAL_RECV_MSG_OPT_KEY:', 'ACCOUNT_DISABLED:') for user in (manager, token)]
    keys += ['FCM_TOKEN:'+token+':5', 'cron_clear_msg_and_fix_seq', 'cron_conversations_destruct_msgs']
    commands = ('auth', 'hello', 'ping', 'quit', 'info', 'acl|whoami',
                'client|info', 'client|setinfo', 'get', 'set', 'del', 'exists',
                'hget', 'hmget', 'hgetall', 'hset', 'hdel', 'expire', 'eval',
                'subscribe', 'unsubscribe', 'publish')
    permissions = 'resetkeys resetchannels -@all '+' '.join('~'+key for key in keys)
    permissions += ' &sensitive_word_reload '+' '.join('+'+name for name in commands)
    lines = ['user default off', 'user '+credentials['redisAdminUser']+' reset on >'+credentials['redisAdminPassword']+' ~* &* +@all']
    for role in ('Legacy', 'Runtime'):
        lines.append('user '+credentials['redis'+role+'User']+' reset on >'+credentials['redis'+role+'Password']+' '+permissions)
    return ('\n'.join(lines)+'\n').encode()

def _prepare_error_class(error):
    if isinstance(error, OSError):
        if error.errno in (errno.EACCES, errno.EPERM):return 'permission'
        if error.errno == errno.ENOSPC:return 'no-space'
        if error.errno == errno.ENOENT:return 'not-found'
    return 'contract' if isinstance(error, ValueError) else 'other'


class Plane:
    def __init__(self, root, images, docker='/usr/bin/docker', minio_source=None):
        self.root = private_dir(root)
        self.private = private_dir(self.root/'private')
        self.images = images
        self.minio_source = minio_source
        if minio_source is not None:
            validate_source_minio(minio_source)
            if images.get('minio') != images.get('redis'):raise ValueError('source-MinIO requires exact reviewed Redis base')
        self.docker = Path(docker)
        if not self.docker.is_absolute() or self.docker.resolve()!=self.docker or not self.docker.is_file(): raise ValueError('fixed real Docker required')
        if set(images) != set(ROLES) or any(not re.fullmatch(r'[A-Za-z0-9./:_-]+@sha256:[0-9a-f]{64}',v) for v in images.values()): raise ValueError('reviewed exact image digests required')
        self.nonce = uuid.uuid4().hex
        self.ids = []
        self.pending_roles = []
        self.calls = 0
        self.start = time.time()
        self.failed = False
        private_write(self.private/'owner.json', {'version':'fullgraph-resource-owner-v1','nonce':self.nonce,'uid':os.getuid(),'created':self.start,'images':images,'dockerSHA256':sha(self.docker.read_bytes()),'minioSource':self.minio_source})

    def prepare_observation(self):
        value = getattr(self, '_prepare_observation', None)
        return None if value is None else dict(value)

    def _prepare_at(self, role, step):
        value = getattr(self, '_prepare_observation', None)
        if value is not None and value['callState'] == 'in-progress':
            value.update(role=role, step=step, cliExitCode=None)

    def _prepare_cli(self, code, error_class='none'):
        value = getattr(self, '_prepare_observation', None)
        if value is not None and value['callState'] == 'in-progress':
            value['cliExitCode'] = code
            if error_class != 'none':value['errorClass'] = error_class

    def call(self, argv, timeout=60):
        self.calls += 1
        # Logs can contain original tool diagnostics. They remain private and
        # never become a compact artifact. No credentials are supplied in argv.
        log=self.private/f'command-{self.calls:04d}.log'
        with log.open('xb') as output:
            os.chmod(log,0o600)
            p=subprocess.Popen([str(self.docker),'--host','unix:///var/run/docker.sock',*argv],stdin=subprocess.DEVNULL,stdout=output,stderr=output,env={'PATH':'/usr/bin:/bin','HOME':os.environ.get('HOME','')},start_new_session=True)
            try: code=p.wait(timeout)
            except subprocess.TimeoutExpired:
                # Only the original CLI is terminated; container outcome is
                # unknown until exact id/nonce inspection during cleanup.
                p.terminate()
                try:p.wait(5)
                except subprocess.TimeoutExpired:p.kill();p.wait()
                self._prepare_cli(p.returncode, 'command-timeout')
                self.failed=True;raise RuntimeError('owned resource CLI outcome unconfirmed')
            output.flush();os.fsync(output.fileno())
        raw=log.read_bytes()
        self._prepare_cli(code)
        if len(raw)>2<<20 or code!=0:
            self._prepare_cli(code, 'command-output-cap' if len(raw)>2<<20 else 'command-nonzero')
            self.failed=True;raise RuntimeError('owned resource command failed; private evidence retained')
        return raw

    def inspect(self,cid):
        if not re.fullmatch('[0-9a-f]{64}',cid):raise ValueError('exact container id required')
        data=json.loads(self.call(['container','inspect',cid]))
        if len(data)!=1 or data[0].get('Id')!=cid:raise ValueError('container identity differs')
        row=data[0];labels=row['Config'].get('Labels') or {}
        if labels.get('luobo.fullgraph.owner')!=self.nonce or labels.get('luobo.fullgraph.role') not in (*ROLES,'kafka-format'):raise ValueError('foreign resource owner')
        return row

    def create(self, role, argv, environment=None):
        image=self.images[role]
        self._prepare_at(role, 'create-intent')
        self.pending_roles.append(role)
        private_write(self.private/(role+'.create-intent.json'),{'version':'fullgraph-resource-create-intent-v1','nonce':self.nonce,'role':role})
        self._prepare_at(role, 'image')
        inspected=json.loads(self.call(['image','inspect',image]))
        if len(inspected)!=1:raise ValueError('one exact reviewed image required')
        require_repo_digest(inspected[0],image)
        cidfile=self.private/(role+'.cid')
        command=['create','--pull=never','--name','luobo-fullgraph-'+self.nonce+'-'+role,'--cidfile',str(cidfile),'--label','luobo.fullgraph.owner='+self.nonce,'--label','luobo.fullgraph.role='+role,'--user',str(os.getuid())+':'+str(os.getgid()),'--cap-drop=ALL','--security-opt=no-new-privileges','--pids-limit=512']
        command += ['--network','none'] if role=='keeper' else ['--network','container:'+self.ids[0]]
        # Mask image-declared anonymous VOLUMEs with owned disposable tmpfs;
        # actual database/log data still goes only to the explicit owned bind.
        self._prepare_at(role, 'volume')
        for volume in sorted((inspected[0].get('Config') or {}).get('Volumes') or {}):
            if volume not in ('/data','/data/db','/data/configdb','/datalog','/logs','/var/lib/kafka/data','/etc/kafka/secrets','/mnt/shared/config'):
                raise ValueError('unreviewed image volume contract')
            command+=['--tmpfs',volume+':rw,nosuid,nodev,noexec,size=16777216,mode=0700,uid='+str(os.getuid())+',gid='+str(os.getgid())]
        for path,target in ((self.private,'/task'),):command+=['--mount',f'type=bind,src={path},dst={target},readonly']
        self._prepare_at(role, 'data')
        data=self.root/(role+'-data')
        if role=='kafka':
            # The exact freshly formatted directory is reused once by its
            # broker, never re-created/reformatted or adopted from elsewhere.
            if data.resolve()!=data or stat.S_IMODE(data.stat().st_mode)!=0o700 or data.stat().st_uid!=os.getuid():raise ValueError('fresh format directory identity differs')
        else:data=private_dir(data)
        command+=['--mount',f'type=bind,src={data},dst=/owned-data']
        self._prepare_at(role, 'env')
        if environment:
            # Source-generated random ASCII values; env-file parser ambiguity
            # is rejected rather than escaped into a second configuration.
            if any(not re.fullmatch('[A-Z][A-Z0-9_]*',k) or any(c in v for c in '\r\n\x00') for k,v in environment.items()):raise ValueError('invalid private environment')
            envpath=self.private/(role+'.env');private_write(envpath,''.join(k+'='+v+'\n' for k,v in sorted(environment.items())).encode());command+=['--env-file',str(envpath)]
        # CLI options must precede the image. Real default entrypoints are used
        # except for the two explicit fixed-image shell-free executable paths.
        if role=='keeper':command+=['--entrypoint','/bin/sleep']
        if role=='kafka':command+=['--entrypoint','/opt/kafka/bin/kafka-server-start.sh']
        if role=='zookeeper':command+=['--entrypoint','/usr/bin/env']
        if role=='minio' and self.minio_source is not None:
            validate_source_minio(self.minio_source)
            command+=['--mount',f"type=bind,src={self.minio_source['path']},dst=/source-minio/minio,readonly",'--entrypoint','/source-minio/minio']
        command+=[image,*argv]
        self._prepare_at(role, 'create')
        try:self.call(command)
        finally:
            if cidfile.exists():
                cid=cidfile.read_text().strip()
                if re.fullmatch('[0-9a-f]{64}',cid) and cid not in self.ids:self.ids.append(cid)
        self._prepare_at(role, 'inspect')
        cid=cidfile.read_text().strip();row=self.inspect(cid)
        if row['Config']['Labels']['luobo.fullgraph.role']!=role or row['Image']!=inspected[0]['Id']:raise ValueError('created resource identity differs')
        self._prepare_at(role, 'start')
        self.call(['start',cid])
        return cid

    def prepare(self):
        self._prepare_observation = {'role':'none','step':'config','callState':'in-progress','errorClass':'none','cliExitCode':None}
        try:
            return self._prepare_body()
        except BaseException as error:
            if self._prepare_observation['errorClass'] == 'none':
                self._prepare_observation['errorClass'] = _prepare_error_class(error)
            raise
        finally:
            self._prepare_observation['callState'] = 'returned'

    def _prepare_body(self):
        self._prepare_at('none', 'config')
        suffix=self.nonce[:16]
        secret=lambda:secrets.token_hex(24)
        credentials={name:secret() for name in ('mongoAdminPassword','mongoLegacyPassword','mongoRuntimePassword','redisAdminPassword','redisLegacyPassword','redisRuntimePassword','kafkaAdminPassword','kafkaLegacyPassword','kafkaRuntimePassword','minioSecret','serviceSecret','rtcSecret')}
        credentials.update({'mongoAdminUser':'owned_admin_'+suffix,'mongoLegacyUser':'owned_legacy_'+suffix,'mongoRuntimeUser':'owned_runtime_'+suffix,'redisAdminUser':'owned_admin_'+suffix,'redisLegacyUser':'owned_legacy_'+suffix,'redisRuntimeUser':'owned_runtime_'+suffix,'kafkaAdminUser':'owned_admin_'+suffix,'kafkaLegacyUser':'owned_legacy_'+suffix,'kafkaRuntimeUser':'owned_runtime_'+suffix,'minioUser':'ownedadmin'+suffix,'rtcKey':'ownedrtc'+suffix})
        private_write(self.private/'credentials.json',credentials)
        self._prepare_at('mongo', 'config')
        private_write(self.private/'mongo-keyfile',base64.b64encode(os.urandom(256))+b'\n')
        self._prepare_at('redis', 'config')
        private_write(self.private/'redis.conf',('bind 127.0.0.1\nport 6379\nprotected-mode yes\nsave ""\nappendonly no\naclfile /task/redis.acl\ndir /owned-data\n').encode())
        private_write(self.private/'redis.acl',redis_acl(credentials,self.nonce))
        self._prepare_at('kafka', 'config')
        jaas='org.apache.kafka.common.security.plain.PlainLoginModule required username="'+credentials['kafkaAdminUser']+'" password="'+credentials['kafkaAdminPassword']+'" '+ ' '.join('user_'+credentials['kafka'+r+'User']+'="'+credentials['kafka'+r+'Password']+'"' for r in ('Admin','Legacy','Runtime'))+';'
        props={'process.roles':'broker,controller','node.id':'1','controller.quorum.voters':'1@127.0.0.1:9093','listeners':'SASL_PLAINTEXT://127.0.0.1:9092,CONTROLLER://127.0.0.1:9093','advertised.listeners':'SASL_PLAINTEXT://127.0.0.1:9092','listener.security.protocol.map':'SASL_PLAINTEXT:SASL_PLAINTEXT,CONTROLLER:PLAINTEXT','controller.listener.names':'CONTROLLER','inter.broker.listener.name':'SASL_PLAINTEXT','sasl.mechanism.inter.broker.protocol':'PLAIN','sasl.enabled.mechanisms':'PLAIN','listener.name.sasl_plaintext.plain.sasl.jaas.config':jaas,'authorizer.class.name':'org.apache.kafka.metadata.authorizer.StandardAuthorizer','super.users':'User:'+credentials['kafkaAdminUser'],'allow.everyone.if.no.acl.found':'false','auto.create.topics.enable':'false','offsets.topic.replication.factor':'1','transaction.state.log.replication.factor':'1','transaction.state.log.min.isr':'1','group.initial.rebalance.delay.ms':'0','num.partitions':'1','log.dirs':'/owned-data/kafka','log.retention.ms':'86400000','log.cleanup.policy':'delete'}
        # The controller listener must not become an anonymous super-user
        # bypass around the later real legacy-principal ACL exclusion.
        props.update({'listener.security.protocol.map':'SASL_PLAINTEXT:SASL_PLAINTEXT,CONTROLLER:SASL_PLAINTEXT','sasl.mechanism.controller.protocol':'PLAIN','listener.name.controller.sasl.enabled.mechanisms':'PLAIN','listener.name.controller.plain.sasl.jaas.config':jaas})
        private_write(self.private/'kafka.properties',''.join(k+'='+v+'\n' for k,v in props.items()).encode())
        private_write(self.private/'kafka-log4j.properties',b'log4j.rootLogger=INFO,stdout\nlog4j.appender.stdout=org.apache.log4j.ConsoleAppender\nlog4j.appender.stdout.layout=org.apache.log4j.PatternLayout\nlog4j.appender.stdout.layout.ConversionPattern=%d %-5p %m (%c)%n\n')
        self._prepare_at('zookeeper', 'config')
        private_write(self.private/'zoo.cfg',b'tickTime=2000\ndataDir=/owned-data\nclientPort=2181\nclientPortAddress=127.0.0.1\nadmin.enableServer=false\n4lw.commands.whitelist=ruok\n')
        self.create('keeper',['2147483647'])
        # Mongo init entrypoint creates the single real root account. The Go
        # bootstrap later creates the fresh DB, marker and legacy/runtime users.
        self.create('mongo',['mongod','--bind_ip','127.0.0.1','--port','27017','--replSet','luoboFullgraph','--dbpath','/owned-data','--keyFile','/task/mongo-keyfile'],{'MONGO_INITDB_ROOT_USERNAME':credentials['mongoAdminUser'],'MONGO_INITDB_ROOT_PASSWORD':credentials['mongoAdminPassword']})
        self.create('redis',['redis-server','/task/redis.conf'])
        self.create('zookeeper',['zkServer.sh','start-foreground','/task/zoo.cfg'],{'ZOO_LOG_DIR':'/owned-data','ZOO_PID_DIR':'/owned-data','ZOO_LOG4J_PROP':'INFO,CONSOLE'})
        # Storage formatting is a fresh, exact image one-shot in the SAME
        # namespace/data mount, before the real broker is ever started.
        self._prepare_at('kafka-format', 'config')
        cluster=secrets.token_urlsafe(16)
        private_write(self.private/'cluster-id.txt',(cluster+'\n').encode())
        # The reviewed Kafka image must implement its official KRaft format
        # entrypoint. This is a separate exact owned container, not a broker
        # script which reuses or silently reformats existing storage.
        self.format_kafka(cluster)
        self.create('kafka',['/task/kafka.properties'],{'KAFKA_HEAP_OPTS':'-Xms256m -Xmx512m','LOG_DIR':'/owned-data/logs','KAFKA_LOG4J_OPTS':'-Dlog4j.configuration=file:/task/kafka-log4j.properties'})
        self.create('minio',['--certs-dir','/owned-data/certs','server','/owned-data','--address','127.0.0.1:9000','--console-address','127.0.0.1:9001'],{'MINIO_ROOT_USER':credentials['minioUser'],'MINIO_ROOT_PASSWORD':credentials['minioSecret']})
        observations=[]
        for cid in self.ids:
            self._prepare_at('none', 'final-observe')
            row=self.inspect(cid);role=row['Config']['Labels']['luobo.fullgraph.role']
            self._prepare_at(role, 'final-observe')
            if role=='kafka-format':
                if row['State']['Running'] or row['State']['ExitCode']!=0:raise ValueError('actual KRaft format not known successful')
                continue
            if not row['State']['Running'] or row['HostConfig'].get('Privileged') or row['HostConfig']['NetworkMode']!=('none' if role=='keeper' else 'container:'+self.ids[0]):raise ValueError('unsafe/nonrunning resource plane')
            observations.append({'role':role,'id':cid,'imageID':row['Image'],'reviewedImage':self.images[role],'PID':row['State']['Pid'],'created':row['Created'],'networkMode':row['HostConfig']['NetworkMode']})
        claim={'version':'owned-fullgraph-resource-plane-v1','nonce':self.nonce,'uid':os.getuid(),'resourceRoot':str(self.root),'keeperID':self.ids[0],'observations':observations,'kafkaClusterID':cluster,'dockerSHA256':sha(self.docker.read_bytes()),'outsideNamespace':os.readlink('/proc/self/ns/net'),'minioMode':'source-elf' if self.minio_source else 'image'}
        if self.minio_source:claim['minioSource']=self.minio_source
        self._prepare_at('none', 'claim')
        private_write(self.private/'plane.json',claim)
        return claim

    def format_kafka(self,cluster):
        # data directory is made before create('kafka'); the original image
        # formatting result must be actual0, never replaced with a JSON claim.
        self._prepare_at('kafka-format', 'create-intent')
        self.pending_roles.append('kafka-format')
        private_write(self.private/'kafka-format.create-intent.json',{'version':'fullgraph-resource-create-intent-v1','nonce':self.nonce,'role':'kafka-format'})
        self._prepare_at('kafka-format', 'data')
        data=private_dir(self.root/'kafka-data')
        name='luobo-fullgraph-'+self.nonce+'-kafka-format'
        cidfile=self.private/'kafka-format.cid'
        command=['create','--pull=never','--name',name,'--cidfile',str(cidfile),'--label','luobo.fullgraph.owner='+self.nonce,'--label','luobo.fullgraph.role=kafka-format','--user',str(os.getuid())+':'+str(os.getgid()),'--network','container:'+self.ids[0],'--cap-drop=ALL','--security-opt=no-new-privileges','--mount',f'type=bind,src={self.private},dst=/task,readonly','--mount',f'type=bind,src={data},dst=/owned-data','--entrypoint','/opt/kafka/bin/kafka-storage.sh',self.images['kafka'],'format','--cluster-id='+cluster,'--config','/task/kafka.properties']
        self._prepare_at('kafka-format', 'image')
        inspected=json.loads(self.call(['image','inspect',self.images['kafka']]))
        if len(inspected)!=1:raise ValueError('exact reviewed format image required')
        require_repo_digest(inspected[0],self.images['kafka'])
        self._prepare_at('kafka-format', 'volume')
        volumes=set((inspected[0].get('Config') or {}).get('Volumes') or {})
        if volumes!={'/var/lib/kafka/data','/etc/kafka/secrets','/mnt/shared/config'}:raise ValueError('official KRaft volume contract differs')
        extra=[]
        for volume in sorted(volumes):extra+=['--tmpfs',volume+':rw,nosuid,nodev,noexec,size=16777216,mode=0700,uid='+str(os.getuid())+',gid='+str(os.getgid())]
        image_at=command.index(self.images['kafka']);command[image_at:image_at]=extra
        # Limit JVM/log destinations without mutating HOME. The format CLI has
        # no network authorization role and is tracked by exact CID on failure.
        self._prepare_at('kafka-format', 'env')
        envpath=self.private/'kafka-format.env';private_write(envpath,b'KAFKA_HEAP_OPTS=-Xms128m -Xmx256m\nLOG_DIR=/owned-data/logs\nKAFKA_LOG4J_OPTS=-Dlog4j.configuration=file:/task/kafka-log4j.properties\n')
        image_at=command.index(self.images['kafka']);command[image_at:image_at]=['--env-file',str(envpath)]
        self._prepare_at('kafka-format', 'create')
        try:self.call(command)
        finally:
            if cidfile.exists():
                cid=cidfile.read_text().strip()
                if re.fullmatch('[0-9a-f]{64}',cid) and cid not in self.ids:self.ids.append(cid)
        self._prepare_at('kafka-format', 'inspect')
        cid=cidfile.read_text().strip();self.inspect(cid)
        self._prepare_at('kafka-format', 'format-attach')
        self.call(['start','--attach',cid],90)
        self._prepare_at('kafka-format', 'format-result')
        row=self.inspect(cid)
        if row['State']['Running'] or row['State']['ExitCode']!=0:raise RuntimeError('actual KRaft format did not finish successfully')

    @classmethod
    def reopen(cls, root):
        # Always-step recovery reads only the original source-owned receipt.
        # It does not regenerate nonce/credentials/config or start resources.
        root=Path(root)
        if not root.is_absolute() or root.resolve()!=root:raise ValueError('canonical retained plane required')
        for p in (root,root/'private'):
            st=p.lstat()
            if not stat.S_ISDIR(st.st_mode) or stat.S_IMODE(st.st_mode)!=0o700 or st.st_uid!=os.getuid():raise ValueError('retained plane custody differs')
        path=root/'private'/'owner.json';st=path.lstat()
        if not stat.S_ISREG(st.st_mode) or stat.S_IMODE(st.st_mode)!=0o600 or st.st_uid!=os.getuid() or st.st_nlink!=1 or not 0<st.st_size<65536:raise ValueError('retained owner differs')
        row=json.loads(path.read_bytes())
        if row['version']!='fullgraph-resource-owner-v1' or row['uid']!=os.getuid() or not re.fullmatch('[0-9a-f]{32}',row['nonce']):raise ValueError('retained identity differs')
        self=object.__new__(cls);self.root,self.private=root,root/'private';self.docker=Path('/usr/bin/docker');self.images=row['images'];self.nonce=row['nonce'];self.start=row['created'];self.ids=[];self.failed=False;self.minio_source=row.get('minioSource')
        if sha(self.docker.read_bytes())!=row['dockerSHA256'] or set(self.images)!=set(ROLES):raise ValueError('retained Docker/image selectors differ')
        self.calls=max([int(p.stem.split('-')[1]) for p in self.private.glob('command-[0-9][0-9][0-9][0-9].log')]+[0])
        self.pending_roles=[]
        for role in (*ROLES,'kafka-format'):
            intent=self.private/(role+'.create-intent.json')
            if intent.exists():
                st=intent.lstat()
                if not stat.S_ISREG(st.st_mode) or stat.S_IMODE(st.st_mode)!=0o600 or st.st_uid!=os.getuid() or st.st_nlink!=1 or not 0<st.st_size<4096:raise ValueError('retained create intent differs')
                if json.loads(intent.read_bytes())!={'version':'fullgraph-resource-create-intent-v1','nonce':self.nonce,'role':role}:raise ValueError('foreign create intent')
                self.pending_roles.append(role)
            p=self.private/(role+'.cid')
            if p.exists():
                st=p.lstat()
                if not stat.S_ISREG(st.st_mode) or st.st_uid!=os.getuid() or st.st_size>80:raise ValueError('retained CID custody differs')
                cid=p.read_text().strip()
                if not re.fullmatch('[0-9a-f]{64}',cid):raise ValueError('retained CID invalid')
                self.ids.append(cid)
        return self

    def cleanup(self):
        errors=[]
        # A create CLI may have lost its reply after the daemon committed.
        # Resolve only our finite attempted names, then immediately retain/use
        # exact Id after full owner-label verification; never stop/remove name.
        for role in self.pending_roles:
            name='luobo-fullgraph-'+self.nonce+'-'+role
            try:
                found=self.call(['ps','--all','--no-trunc','--filter','name=^/'+name+'$','--format','{{.ID}}']).decode().splitlines()
                if len(found)>1:raise ValueError('ambiguous retained resource')
                if found:
                    cid=found[0];row=self.inspect(cid)
                    if row['Name']!='/'+name or row['Config']['Labels']['luobo.fullgraph.role']!=role:raise ValueError('attempted name owner differs')
                    if cid not in self.ids:self.ids.append(cid)
            except BaseException:errors.append(role)
        for cid in reversed(self.ids):
            try:
                # An earlier completed cleanup may already have removed this
                # exact Id. Query successful empty output rather than treating
                # an arbitrary inspect transport error as absence.
                live=self.call(['ps','--all','--no-trunc','--filter','id='+cid,'--format','{{.ID}}']).decode().splitlines()
                if not live:continue
                if live!=[cid]:raise ValueError('exact retained Id differs')
                row=self.inspect(cid)
                if row['State']['Running']:self.call(['stop','--time','30',cid],45)
                row=self.inspect(cid)
                if row['State']['Running']:raise RuntimeError('owned container is still running')
                self.call(['rm','--volumes',cid])
                left=self.call(['ps','--all','--no-trunc','--filter','id='+cid,'--format','{{.ID}}']).strip()
                if left:raise ValueError('exact removed Id still present')
            except BaseException:errors.append(cid)
        receipt={'version':'owned-fullgraph-resource-cleanup-v1','nonce':self.nonce,'exactContainerIDs':self.ids,'unconfirmedIDs':errors,'accepted':not errors}
        name='cleanup.json' if not (self.private/'cleanup.json').exists() else 'cleanup-inspection-'+str(self.calls)+'.json'
        private_write(self.private/name,receipt)
        if errors:raise RuntimeError('owned resource cleanup unconfirmed; custody retained')
        return receipt
