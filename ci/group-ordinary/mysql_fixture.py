#!/usr/bin/env python3
"""Exact disposable MySQL fixture for source1133; no product or local service use."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import stat
import subprocess
import sys
import time
import uuid

IMAGE = 'mysql:8.4.11@sha256:1f8411cc085d2f2f92060fca926746cbf97f87b76c3008e9741ad62e604c4ffa'
SOCKET = '/var/run/mysqld/mysqld.sock'
REPO = Path(__file__).resolve().parents[2]

def write(path, value):
    data = (json.dumps(value, sort_keys=True, indent=2) + '\n').encode()
    temporary = path.with_name('.' + path.name + '-' + secrets.token_hex(8))
    with temporary.open('xb') as out:
        temporary.chmod(0o600)
        out.write(data)
        out.flush()
        os.fsync(out.fileno())
    os.replace(temporary, path)
    fd = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)

def private_bytes(path):
    info = path.lstat()
    parent = path.parent.lstat()
    if (path.resolve() != path or not stat.S_ISREG(info.st_mode)
            or stat.S_IMODE(info.st_mode) != 0o600 or info.st_uid != os.getuid()
            or info.st_nlink != 1 or info.st_size > 16384
            or not stat.S_ISDIR(parent.st_mode) or stat.S_IMODE(parent.st_mode) != 0o700
            or parent.st_uid != os.getuid()):
        raise ValueError('private fixture ownership mismatch')
    with path.open('rb') as source:
        opened = os.fstat(source.fileno())
        if (opened.st_dev, opened.st_ino) != (info.st_dev, info.st_ino):
            raise ValueError('private fixture identity changed')
        return source.read(16385)

class Fixture:
    def __init__(self):
        required = {'GITHUB_ACTIONS':'true', 'RUNNER_ENVIRONMENT':'github-hosted', 'RUNNER_OS':'Linux'}
        if any(os.environ.get(k) != v for k, v in required.items()):
            raise ValueError('GitHub-hosted disposable Linux fixture only')
        run, attempt = os.environ.get('GITHUB_RUN_ID', ''), os.environ.get('GITHUB_RUN_ATTEMPT', '')
        if not re.fullmatch('[0-9]+', run) or not re.fullmatch('[0-9]+', attempt):
            raise ValueError('exact run identity required')
        commit = os.environ.get('GITHUB_SHA', '')
        if not re.fullmatch('[0-9a-f]{40}', commit):
            raise ValueError('exact commit identity required')
        self.owner = run + ':' + attempt
        self.root = Path('/tmp/luobo-risk-' + run + '-' + attempt)
        self.private = self.root / 'private'
        self.output = REPO / 'ci-results/group-ordinary-mysql'
        self.commit = commit
        self.step = 0

    def command(self, args, *, data=None, timeout=30, check=True):
        self.step += 1
        # Every Docker/client diagnostic is private, including failed startup.
        # Arguments contain only fixed options, paths, names and IDs; SQL and
        # passwords are supplied on stdin/private files, never argv or console.
        log = self.private / ('command-%03d.stderr' % self.step)
        fd = os.open(log, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        env = {k:v for k,v in os.environ.items()
               if not k.startswith(('MYSQL_', 'MARIADB_', 'LIBMYSQL_', 'DOCKER_'))}
        env.update(DOCKER_HOST='unix:///var/run/docker.sock',
                   DOCKER_CONFIG=str(self.private / 'docker-config'))
        with os.fdopen(fd, 'wb') as err:
            result = subprocess.run(args, input=data, stdout=subprocess.PIPE,
                                    stderr=err, timeout=timeout, check=False, env=env)
        if check and result.returncode != 0:
            raise ValueError('owned fixture command unconfirmed')
        return result

    def load(self):
        self.state = json.loads(private_bytes(self.private / 'state.json'))
        if (self.state.get('version') != 1 or self.state.get('owner') != self.owner
                or self.state.get('commit') != self.commit or self.state.get('image') != IMAGE
                or not re.fullmatch('[0-9a-f]{32}', self.state.get('nonce', ''))
                or self.state.get('name') != 'luobo-risk-' + self.owner.replace(':', '-') + '-' + self.state['nonce'][:12]):
            raise ValueError('owned fixture state mismatch')
        if (not re.fullmatch('luobo_risk_test_[0-9a-f]{24}', self.state.get('database', ''))
                or not re.fullmatch('lr_[0-9a-f]{16}', self.state.get('user', ''))
                or not re.fullmatch('[0-9a-f]{64}', self.state.get('claim', ''))):
            raise ValueError('SQL identifier/claim state mismatch')
        self.step = len(list(self.private.glob('command-*.stderr')))
        return self.state

    def inspect_owned(self):
        result = self.command(['docker', 'container', 'inspect', self.state['name']], check=False)
        if result.returncode != 0:
            # Never interpret arbitrary inspect errors as absence.
            listing = self.command(['docker', 'container', 'ls', '-a', '--no-trunc', '--filter',
                                    'name=^/' + self.state['name'] + '$', '--format', '{{.ID}}'])
            if listing.stdout.strip():
                raise ValueError('container inspection uncertain')
            return None
        items = json.loads(result.stdout)
        if len(items) != 1:
            raise ValueError('container identity ambiguous')
        item = items[0]
        labels = item['Config']['Labels']
        if (item['Name'] != '/' + self.state['name'] or item['Config']['Image'] != IMAGE
                or labels.get('luobo.ci.run') != self.owner
                or labels.get('luobo.risk.nonce') != self.state['nonce']
                or (self.state.get('containerID') and item['Id'] != self.state['containerID'])):
            raise ValueError('foreign container refused')
        return item

    def sql(self, sql):
        return self.command(['docker', 'exec', '-i', self.state['containerID'], 'mysql',
            '--defaults-file=/run/owned/root.cnf', '--no-login-paths', '--protocol=SOCKET',
            '--socket=' + SOCKET,
            '--batch', '--skip-column-names', '--raw'], data=sql.encode()).stdout.decode().strip()

    def binding(self):
        d = self.state
        result = self.sql('SELECT @@server_uuid,VERSION();\nSELECT nonce FROM `' + d['database'] + '`._luobo_risk_claim WHERE id=1;\n')
        lines = result.splitlines()
        if (len(lines) != 2 or lines[0] != d['serverUUID'] + '\t8.4.11'
                or lines[1] != d['claim']):
            raise ValueError('SQL UUID/version/claim binding mismatch')

    def prepare(self):
        if self.root.exists() or self.root.is_symlink() or self.output.exists() or self.output.is_symlink():
            raise ValueError('fresh fixture and output paths required')
        self.root.mkdir(mode=0o700)
        self.private.mkdir(mode=0o700)
        (self.private / 'docker-config').mkdir(mode=0o700)
        self.output.mkdir(mode=0o700, parents=True)
        nonce = secrets.token_hex(16)
        self.state = {'version':1, 'owner':self.owner, 'commit':self.commit, 'nonce':nonce,
            'image':IMAGE, 'name':'luobo-risk-' + self.owner.replace(':', '-') + '-' + nonce[:12],
            'database':'luobo_risk_test_' + secrets.token_hex(12), 'user':'lr_' + secrets.token_hex(8),
            'claim':secrets.token_hex(32)}
        write(self.private / 'state.json', self.state)
        password, root_password = secrets.token_hex(32), secrets.token_hex(32)
        for name, body in (('root-password', root_password),
                           ('root.cnf', '[client]\nuser=root\npassword=' + root_password + '\n')):
            fd = os.open(self.private / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, 'w') as out:
                out.write(body)
                out.flush()
                os.fsync(out.fileno())
        self.command(['docker', 'pull', '--platform', 'linux/amd64', IMAGE], timeout=300)
        # Exact fresh nonce-tagged container. No host data volume is mounted.
        self.command(['docker', 'run', '-d', '--platform', 'linux/amd64', '--name', self.state['name'],
            '--label', 'luobo.ci.run=' + self.owner, '--label', 'luobo.risk.nonce=' + nonce,
            '--network', 'host', '--memory', '1536m', '--cpus', '2', '--log-driver', 'local',
            '--log-opt', 'max-size=5m', '--log-opt', 'max-file=2',
            '--mount', 'type=tmpfs,destination=/var/lib/mysql,tmpfs-size=1073741824',
            '--mount', 'type=bind,source=' + str(self.private) + ',destination=/run/owned,readonly',
            '--env', 'MYSQL_ROOT_PASSWORD_FILE=/run/owned/root-password', IMAGE,
            '--bind-address=127.0.0.1', '--port=38406', '--mysqlx=OFF', '--socket=' + SOCKET,
            '--innodb-buffer-pool-size=64M', '--innodb-redo-log-capacity=64M', '--skip-log-bin'], timeout=60)
        container = self.inspect_owned()
        if container is None:
            raise ValueError('new container not confirmed')
        self.state['containerID'] = container['Id']
        write(self.private / 'state.json', self.state)
        deadline = time.monotonic() + 120
        while True:
            try:
                version = self.sql('SELECT @@server_uuid,VERSION(),@@port,@@skip_networking;\n').split('\t')
                if len(version) == 4 and version[1:] == ['8.4.11', '38406', '0'] and str(uuid.UUID(version[0])) == version[0]:
                    break
            except (ValueError, subprocess.SubprocessError):
                pass
            if time.monotonic() >= deadline:
                raise ValueError('MySQL startup not confirmed')
            time.sleep(1)
        if self.sql("SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name NOT IN ('mysql','sys','information_schema','performance_schema');\n") != '0':
            raise ValueError('fresh SQL instance is not empty')
        d = self.state
        d['serverUUID'] = version[0]
        write(self.private / 'state.json', d)
        # Never retry this claim/schema creation after an unknown command result.
        self.sql('CREATE DATABASE `' + d['database'] + '`;\n'
            + 'CREATE USER \'' + d['user'] + '\'@\'%\' IDENTIFIED BY \'' + password + '\';\n'
            + 'CREATE TABLE `' + d['database'] + '`.`_luobo_risk_claim` (id INT PRIMARY KEY,nonce VARCHAR(64) NOT NULL);\n'
            + 'INSERT INTO `' + d['database'] + '`.`_luobo_risk_claim` VALUES (1,\'' + d['claim'] + '\');\n'
            + 'CREATE TABLE `' + d['database'] + '`.`client_config` (`key` VARCHAR(255) PRIMARY KEY,value TEXT NOT NULL);\n'
            + 'GRANT SELECT ON `' + d['database'] + '`.`_luobo_risk_claim` TO \'' + d['user'] + '\'@\'%\';\n'
            + 'GRANT SELECT,INSERT,UPDATE,DELETE ON `' + d['database'] + '`.`client_config` TO \'' + d['user'] + '\'@\'%\';\n'
            + 'GRANT LOCK TABLES ON `' + d['database'] + '`.* TO \'' + d['user'] + '\'@\'%\';\n')
        self.binding()
        document = {'version':1, 'address':'127.0.0.1:38406', 'database':d['database'],
                    'user':d['user'], 'password':password, 'claim':d['claim'], 'serverUUID':d['serverUUID']}
        write(self.private / 'fixture.json', document)
        write(self.output / 'ready.json', {'owner':self.owner, 'commit':self.commit,
            'image':IMAGE, 'fixtureSHA256':hashlib.sha256(private_bytes(self.private / 'fixture.json')).hexdigest(),
            'UUIDAndClaimConfirmed':True, 'credentials':'private_outside_artifact_root'})

    def check(self):
        self.load()
        if self.inspect_owned() is None:
            raise ValueError('owned SQL container missing')
        ready = json.loads((self.output / 'ready.json').read_bytes())
        body = private_bytes(self.private / 'fixture.json')
        if (ready.get('owner') != self.owner or ready.get('commit') != self.commit
                or ready.get('image') != IMAGE or ready.get('UUIDAndClaimConfirmed') is not True
                or ready.get('fixtureSHA256') != hashlib.sha256(body).hexdigest()):
            raise ValueError('SQL ready identity mismatch')
        self.binding()

    def collect(self):
        self.load()
        if self.inspect_owned() is None:
            raise ValueError('SQL missing before collection')
        self.check()
        database = self.state['database']
        values = self.sql("SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name NOT IN ('mysql','sys','information_schema','performance_schema');\n"
            + "SELECT COUNT(*) FROM mysql.user WHERE User='" + self.state['user'] + "' AND Host='%';\n"
            + "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='" + database + "';\n").splitlines()
        if values != ['1', '1', '2']:
            raise ValueError('unexpected SQL database/user/table residual')
        write(self.output / 'collected.json', {'ownedDatabaseCount':1, 'ownedUserCount':1,
            'ownedTableCount':2, 'unexpectedDatabases':0, 'UUIDAndClaimConfirmed':True,
            'privateDiagnosticsUploaded':False})

    def cleanup(self):
        self.load()
        container = self.inspect_owned()
        if container is None:
            raise ValueError('cleanup cannot claim an unobserved container')
        identity = container['Id']
        logical = False
        try:
            self.binding()
            self.sql('DROP DATABASE `' + self.state['database'] + '`;\nDROP USER \'' + self.state['user'] + '\'@\'%\';\n')
            logical = self.sql("SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name NOT IN ('mysql','sys','information_schema','performance_schema');\n"
                + "SELECT COUNT(*) FROM mysql.user WHERE User='" + self.state['user'] + "' AND Host='%';\n").splitlines() == ['0', '0']
        except (ValueError, KeyError, subprocess.SubprocessError):
            # Still stop only the exact nonce-tagged container; logical failure
            # remains a rejected cleanup receipt, not fabricated SQL-zero proof.
            logical = False
        stopped = self.command(['docker', 'stop', '--time', '20', identity], timeout=30, check=False)
        after = self.inspect_owned()
        joined = bool(after and after['State']['Running'] is False and after['State']['ExitCode'] == 0 and stopped.returncode == 0)
        self.command(['docker', 'rm', '-f', '-v', identity], timeout=30)
        absent = self.inspect_owned() is None
        write(self.output / 'cleanup.json', {'logicalDatabaseAndUserZero':logical,
            'normalShutdownJoined':joined, 'exactContainerAbsent':absent,
            'state':'passed' if logical and joined and absent else 'failed',
            'scope':'single_exact_id_name_run_nonce_image'})
        if not (logical and joined and absent):
            raise ValueError('owned SQL cleanup incomplete')

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=('prepare', 'check', 'collect', 'cleanup'))
    args = parser.parse_args()
    try:
        fixture = Fixture()
        getattr(fixture, args.mode)()
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError, json.JSONDecodeError):
        # Never print driver/Docker/SQL errors, exception values or private paths.
        print('Owned SQL fixture incomplete; private diagnostics retained outside artifact output.', file=sys.stderr)
        raise SystemExit(2)
