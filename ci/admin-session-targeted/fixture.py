"""Two disposable, exact-owned SQL/Redis fixtures; no product data is selected."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import stat
import subprocess
import time
import uuid

REPO = Path(__file__).resolve().parents[2]

def module(relative, label):
    spec = importlib.util.spec_from_file_location(label, REPO / relative)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value

safe = module('ci/group-ordinary/mysql_fixture.py', 'admin_session_mysql_primitives')
images = module('ci/backend-fullgraph/resources.py', 'admin_session_image_identity')


def container_identity(row, expected):
    labels = row.get('Config', {}).get('Labels') or {}
    if (row.get('Id') != expected['id'] or row.get('Name') != '/' + expected['name']
            or row.get('Image') != expected['config']
            or row['Config'].get('User') != expected['user']
            or labels.get('luobo.admin.owner') != expected['owner']
            or labels.get('luobo.admin.nonce') != expected['nonce']
            or labels.get('luobo.admin.role') != expected['role']
            or row.get('HostConfig', {}).get('NetworkMode') != 'bridge'):
        raise ValueError('exact fixture container owner differs')
    return row


class Fixture:
    def __init__(self, root, policy):
        self.root, self.policy = Path(root), policy
        self.private = self.root / 'private'
        self.owner = os.environ['GITHUB_RUN_ID'] + ':' + os.environ['GITHUB_RUN_ATTEMPT']
        self.head = os.environ['GITHUB_SHA']
        self.state_path = self.private / 'fixture-state.json'
        self.sequence = 0
        self.state = None

    def command(self, args, data=None, timeout=30, check=True):
        self.sequence += 1
        prefix = self.private / ('command-%06d' % self.sequence)
        env = {'PATH': '/usr/bin:/bin', 'HOME': os.environ['HOME'],
               'DOCKER_HOST': 'unix:///var/run/docker.sock',
               'DOCKER_CONFIG': str(self.private / 'docker-config')}
        with prefix.with_suffix('.stdout').open('xb') as out, prefix.with_suffix('.stderr').open('xb') as err:
            result = subprocess.run(args, input=data, stdout=out, stderr=err,
                                    env=env, timeout=timeout, check=False)
        raw = prefix.with_suffix('.stdout').read_bytes()
        if len(raw) > 2 << 20 or prefix.with_suffix('.stderr').stat().st_size > 2 << 20:
            raise ValueError('private fixture command output exceeded cap')
        if check and result.returncode != 0:
            raise ValueError('owned fixture command unconfirmed')
        return result.returncode, raw

    def save(self):
        self.state['sequence'] = self.sequence
        safe.write(self.state_path, self.state)

    def load(self):
        self.state = json.loads(safe.private_bytes(self.state_path))
        if (self.state.get('schema') != 1 or self.state['owner'] != self.owner or self.state['head'] != self.head
                or self.state['uid'] != os.getuid() or self.state['gid'] != os.getgid()
                or not re.fullmatch('[0-9a-f]{32}', self.state['nonce'])):
            raise ValueError('private fixture owner differs')
        for role, row in self.state['containers'].items():
            if (role not in ('mysql', 'redis') or row['config'] != self.policy[role]['configDigest']
                    or row['owner'] != self.owner or row['nonce'] != self.state['nonce']
                    or row['role'] != role or row['user'] != str(os.getuid()) + ':' + str(os.getgid())
                    or row['name'] != 'luobo-admin-' + self.owner.replace(':', '-') + '-' + self.state['nonce'][:12] + '-' + role):
                raise ValueError('original finite container intent required')
        self.sequence = max(self.state['sequence'], len(list(self.private.glob('command-*.stderr'))))

    def inspect(self, role):
        expected = self.state['containers'][role]
        code, raw = self.command(['docker', 'container', 'inspect', expected['name']], check=False)
        if code:
            _, listing = self.command(['docker', 'container', 'ls', '-a', '--no-trunc',
                '--filter', 'name=^/' + expected['name'] + '$', '--format', '{{.ID}}'])
            if listing.strip():
                raise ValueError('fixture absence is uncertain')
            return None
        rows = json.loads(raw)
        if len(rows) != 1:
            raise ValueError('ambiguous fixture identity')
        row = rows[0]
        if not expected['id']:
            if not re.fullmatch('[0-9a-f]{64}', row.get('Id', '')):
                raise ValueError('finite original container ID required')
            expected['id'] = row['Id']
            self.save()
        return container_identity(row, expected)

    def sql(self, text, timeout=5):
        cid = self.state['containers']['mysql']['id']
        return self.command(['docker', 'exec', '-i', cid, 'mysql',
            '--defaults-file=/run/owned/root.cnf', '--no-login-paths', '--protocol=SOCKET',
            '--socket=/var/run/mysqld/mysqld.sock', '--batch', '--skip-column-names', '--raw'],
            data=text.encode(), timeout=timeout)[1].decode().strip()

    def redis(self, args, data=None):
        cid = self.state['containers']['redis']['id']
        return self.command(['docker', 'exec', '-i', cid, 'redis-cli', '--raw', '-n', '15', *args],
                            data=data, timeout=5)[1].decode().strip()

    def prepare(self):
        self.private.mkdir(mode=0o700)
        for child in ['docker-config', 'mysql-owned', 'redis-owned', 'credentials']:
            (self.private / child).mkdir(mode=0o700)
        for child in ['mysql-data', 'redis-data']:
            (self.root / child).mkdir(mode=0o700)
        nonce = secrets.token_hex(16)
        self.state = {'schema': 1, 'owner': self.owner, 'head': self.head, 'uid': os.getuid(), 'gid': os.getgid(),
                      'nonce': nonce, 'containers': {}, 'sequence': 0, 'removed': {},
                      'database': 'luobo_admin_fixture_' + nonce[:24],
                      'sqlUser': 'las_' + nonce[:16], 'claim': secrets.token_hex(32)}
        password, root_password = secrets.token_hex(32), secrets.token_hex(32)
        safe.write(self.state_path, self.state)
        images.private_write(self.private / 'mysql-owned/root-password', root_password.encode())
        images.private_write(self.private / 'mysql-owned/root.cnf',
                             ('[client]\nuser=root\npassword=' + root_password + '\n').encode())
        images.private_write(self.private / 'redis-owned/redis.conf',
            b'bind 0.0.0.0\nport 6379\nprotected-mode no\nsave ""\nappendonly no\ndir /data\n')
        total_image = 0
        for role, port, datadir in [('mysql', 3306, '/var/lib/mysql'), ('redis', 6379, '/data')]:
            policy = self.policy[role]
            self.command(['docker', 'pull', '--platform', 'linux/amd64', policy['image']], timeout=300)
            _, raw = self.command(['docker', 'image', 'inspect', policy['image']])
            entries = json.loads(raw)
            if len(entries) != 1:
                raise ValueError('one fixed image required')
            meta = entries[0]
            images.require_repo_digest(meta, policy['image'])
            if (meta['Id'] != policy['configDigest'] or meta['Os'] != 'linux'
                    or meta['Architecture'] != 'amd64'
                    or meta['Config'].get('Entrypoint') != ['docker-entrypoint.sh']
                    or not 0 < meta['Size'] <= 2 << 30):
                raise ValueError('actual fixed image body/config differs')
            total_image += meta['Size']
            if total_image > 3 << 30:
                raise ValueError('combined fixed image budget exceeded')
            name = 'luobo-admin-' + self.owner.replace(':', '-') + '-' + nonce[:12] + '-' + role
            expected = {'id': '', 'name': name, 'config': policy['configDigest'],
                        'owner': self.owner, 'nonce': nonce, 'role': role,
                        'user': str(os.getuid()) + ':' + str(os.getgid()), 'started': False}
            self.state['containers'][role] = expected
            self.save()
            args = ['docker', 'create', '--pull=never', '--platform', 'linux/amd64', '--name', name,
                '--cidfile', str(self.private / (role + '.cid')),
                '--label', 'luobo.admin.owner=' + self.owner, '--label', 'luobo.admin.nonce=' + nonce,
                '--label', 'luobo.admin.role=' + role, '--user', expected['user'], '--network', 'bridge',
                '--memory', '1536m' if role == 'mysql' else '256m', '--cpus', '2',
                '--read-only', '--tmpfs', '/tmp:rw,nosuid,nodev,noexec,size=67108864,mode=1777',
                '--log-driver', 'local', '--log-opt', 'max-size=1m', '--log-opt', 'max-file=2',
                '--publish', '127.0.0.1::' + str(port),
                '--mount', 'type=bind,src=' + str(self.root / (role + '-data')) + ',dst=' + datadir,
                '--mount', 'type=bind,src=' + str(self.private / (role + '-owned')) + ',dst=/run/owned,readonly']
            if role == 'mysql':
                args += ['--tmpfs', '/var/run/mysqld:rw,nosuid,nodev,noexec,size=16777216,mode=0700,uid=' + str(os.getuid()) + ',gid=' + str(os.getgid()),
                    '--env', 'MYSQL_ROOT_PASSWORD_FILE=/run/owned/root-password',
                    '--env', 'MYSQL_ROOT_HOST=127.0.0.1', policy['image'], 'mysqld',
                    '--bind-address=0.0.0.0', '--port=3306', '--mysqlx=OFF', '--tmpdir=/var/run/mysqld',
                    '--socket=/var/run/mysqld/mysqld.sock', '--pid-file=/var/run/mysqld/mysqld.pid',
                    '--secure-file-priv=NULL', '--log-error=/var/lib/mysql/task-error.log',
                    '--character-set-server=utf8mb4', '--collation-server=utf8mb4_unicode_ci',
                    '--innodb-buffer-pool-size=64M', '--innodb-redo-log-capacity=64M', '--skip-log-bin']
            else:
                args += [policy['image'], 'redis-server', '/run/owned/redis.conf']
            self.command(args, timeout=60)
            self.inspect(role)
            self.command(['docker', 'start', expected['id']], timeout=30)
            expected['started'] = True
            self.save()
            row = self.inspect(role)
            bindings = row['NetworkSettings']['Ports'].get(str(port) + '/tcp')
            if (not row['State']['Running'] or not isinstance(bindings, list) or len(bindings) != 1
                    or bindings[0].get('HostIp') != '127.0.0.1'
                    or not re.fullmatch('[0-9]{4,5}', bindings[0].get('HostPort', ''))
                    or not 1024 <= int(bindings[0]['HostPort']) <= 65535):
                raise ValueError('only one random loopback port is permitted')
            expected['port'] = bindings[0]['HostPort']
            self.save()
        deadline = time.monotonic() + 120
        while True:
            try:
                fields = self.sql('SELECT @@server_uuid,VERSION();').split('\t')
                if len(fields) == 2 and str(uuid.UUID(fields[0])) == fields[0] and fields[1] == '8.4.11':
                    self.state['serverUUID'] = fields[0]
                    break
            except (ValueError, subprocess.SubprocessError):
                pass
            if time.monotonic() >= deadline:
                raise ValueError('fixed SQL readiness deadline exceeded')
            time.sleep(1)
        if self.sql("SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name NOT IN ('mysql','sys','information_schema','performance_schema');") != '0':
            raise ValueError('SQL fixture was not empty')
        d = self.state
        self.sql('CREATE DATABASE `' + d['database'] + '`;'
            + 'CREATE TABLE `' + d['database'] + '`._claim(id INT PRIMARY KEY,nonce VARCHAR(64) NOT NULL);'
            + "INSERT INTO `" + d['database'] + "`._claim VALUES(1,'" + d['claim'] + "');"
            + "CREATE USER '" + d['sqlUser'] + "'@'%' IDENTIFIED BY '" + password + "';"
            + "GRANT ALL PRIVILEGES ON *.* TO '" + d['sqlUser'] + "'@'%';")
        deadline = time.monotonic() + 30
        while True:
            try:
                info = self.redis(['INFO', 'server'])
                if any(line == 'redis_version:8.2.2' for line in info.splitlines()) and self.redis(['DBSIZE']) == '0':
                    break
            except (ValueError, subprocess.SubprocessError):
                pass
            if time.monotonic() >= deadline:
                raise ValueError('fixed Redis readiness deadline exceeded')
            time.sleep(1)
        d['redisClaimKey'] = 'LUOBO_ADMIN_FIXTURE:' + nonce
        self.redis(['-x', 'SET', d['redisClaimKey']], (d['claim'] + '\n').encode())
        dsn = d['sqlUser'] + ':' + password + '@tcp(127.0.0.1:' + d['containers']['mysql']['port'] + ')/mysql?parseTime=true&charset=utf8mb4'
        images.private_write(self.private / 'credentials/mysql.dsn', dsn.encode())
        self.save()
        self.check()
        return {'images': {k: {'image': v['image'], 'configDigest': v['configDigest']} for k, v in self.policy.items()},
                'SQLVersion': '8.4.11', 'RedisVersion': '8.2.2', 'freshUUIDClaimConfirmed': True,
                'onlyRandomLoopbackPorts': True, 'credentials': 'private_FILE_only',
                'testDataOnlyNoUserData': True, 'firstActualMySQLFixtureNotProduct20Acceptance': True}

    def check(self):
        for role in ('mysql', 'redis'):
            row = self.inspect(role)
            if row is None or not row['State']['Running']:
                raise ValueError('original live fixture required')
        d = self.state
        got = self.sql('SELECT @@server_uuid,VERSION();SELECT nonce FROM `' + d['database'] + '`._claim WHERE id=1;').splitlines()
        if got != [d['serverUUID'] + '\t8.4.11', d['claim']]:
            raise ValueError('original SQL UUID/claim differs')
        if self.redis(['GET', d['redisClaimKey']]) != d['claim']:
            raise ValueError('original Redis claim differs')

    def residuals(self):
        self.check()
        db = self.sql("SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('mysql','sys','information_schema','performance_schema') ORDER BY schema_name;").splitlines()
        if db != [self.state['database']] or self.redis(['DBSIZE']) != '1':
            raise ValueError('test SQL/Redis cleanup not confirmed')
        return {'testSchemasZero': True, 'testRedisKeysZero': True, 'originalClaimsPreserved': True}

    def cleanup(self):
        if self.state is None:
            if not self.state_path.exists():
                return {'accepted': True, 'createdObjects': 0}
            self.load()
        logical = False
        try:
            self.residuals()
            self.sql('DROP DATABASE `' + self.state['database'] + '`;'
                + "DROP USER '" + self.state['sqlUser'] + "'@'%';")
            logical = self.sql("SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name NOT IN ('mysql','sys','information_schema','performance_schema');") == '0'
            logical = logical and self.redis(['DEL', self.state['redisClaimKey']]) == '1' and self.redis(['DBSIZE']) == '0'
        except (ValueError, KeyError, OSError, subprocess.SubprocessError):
            logical = False
        records = []
        for role in ('redis', 'mysql'):
            if role not in self.state['containers']:
                continue
            current = self.inspect(role)
            expected = self.state['containers'][role]
            if current is None:
                if not self.state['removed'].get(role):
                    raise ValueError('unobserved container disappearance')
                records.append(self.state['removed'][role])
                continue
            joined = True
            if current['State']['Running']:
                code, _ = self.command(['docker', 'stop', '--time', '20', expected['id']], timeout=30, check=False)
                after = self.inspect(role)
                joined = code == 0 and after is not None and not after['State']['Running'] and after['State']['ExitCode'] == 0
            self.command(['docker', 'rm', '--volumes', expected['id']], timeout=30)
            absent = self.inspect(role) is None
            record = {'role': role, 'originalContainerID': expected['id'], 'normalShutdownJoined': joined, 'exactAbsent': absent}
            self.state['removed'][role] = record
            self.save()
            records.append(record)
        previous = self.state.get('cleanup')
        if previous is not None and len(records) == len(self.state['removed']):
            return previous
        result = {'accepted': logical and all(r['normalShutdownJoined'] and r['exactAbsent'] for r in records),
                  'SQLAndRedisLogicalZero': logical, 'originalObjects': records,
                  'noGlobalPruneOrForeignObjects': True}
        self.state['cleanup'] = result
        self.save()
        return result
