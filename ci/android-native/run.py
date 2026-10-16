#!/usr/bin/env python3
"""Cloud-only explicit frozen257 Android build. Default is metadata read-only.

No live App source alias, credentials, old binary, private CA or device operation.
Stages deliberately alternate online acquisition and offline compilation.
"""
import argparse
import errno
import hashlib
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import sys
import tarfile
from urllib.parse import urlsplit, unquote
import xml.etree.ElementTree as ET

import audit
import support as s
import verify_inputs

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
COHORT = 'current257-auditable-arm64-debug-jobca-v1'
SOURCE_REF = '314ac556fdc4be7f98dbd74370201922c71e67c5'
SOURCE_REPO = 'https://github.com/damaowoaini/LuoboIM.git'
CORE_REF = '7b156fc5557878ad6e6a07e572694af5699c6624'
CORE_REPO = 'https://github.com/openimsdk/openim-sdk-core.git'
CORE_TOOLS = 'packages/flutter_openim_sdk/tool/native_core'
STAGES = ['setup', 'core', 'gradle-setup', 'apk']
COLD = 20 * s.GIB


def metadata():
    registry = json.loads((HERE / 'cohort.json').read_text())
    if registry['cohort'] != COHORT or registry['source_ref'] != SOURCE_REF or registry['source_repository'] != SOURCE_REPO:
        raise ValueError('explicit historical257 cohort only; no latest alias')
    for key in ['inputs', 'toolchain', 'bindings', 'dependency-additions']:
        field = key.replace('-', '_') + '_sha256'
        if s.sha(HERE / (key + '.json')) != registry[field]:
            raise ValueError('frozen controller metadata differs: ' + key)
    inputs = json.loads((HERE / 'inputs.json').read_text())
    if (inputs['members'] != 426 or inputs['cohort'] != COHORT
            or inputs['source_digest'] != registry['source_digest']
            or verify_inputs.digest(inputs['files']) != registry['source_digest']
            or len({x['path'] for x in inputs['files']}) != 426):
        raise ValueError('exact426 historical source manifest required')
    for item in inputs['files']:
        if item['path'].startswith(CORE_TOOLS + '/'):
            p = REPO / item['path']
            if p.is_symlink() or not p.is_file() or p.stat().st_size != item['bytes'] or s.sha(p) != item['sha256']:
                raise ValueError('audited core payload differs')
    tools = json.loads((HERE / 'toolchain.json').read_text())
    tools['cloud_dependency_additions'] = json.loads((HERE / 'dependency-additions.json').read_text())
    return registry, inputs, tools


def work_path(value, fresh):
    p = Path(value).absolute()
    parent = p.parent
    if (p.resolve() != p or parent.is_symlink() or not parent.is_dir()
            or parent.stat().st_uid != os.getuid() or parent.stat().st_mode & 0o077
            or p == REPO or p.is_relative_to(REPO) or p == Path('/') or p == Path.home()):
        raise ValueError('new canonical output below owned0700 parent outside checkout required')
    if fresh:
        if p.exists() or p.is_symlink():
            raise ValueError('fresh output required; no partial resume')
        v = os.statvfs(parent)
        if v.f_bavail * v.f_frsize < COLD:
            raise ValueError('cold20GiB resource gate: zero setup/materialize')
        h = os.statvfs(REPO)
        if h.f_bavail * h.f_frsize < COLD:
            raise ValueError('cold20GiB host resource gate: zero setup/materialize')
    elif p.is_symlink() or not p.is_dir() or p.stat().st_uid != os.getuid() or p.stat().st_mode & 0o077:
        raise ValueError('existing owned0700 claimed workspace required')
    return p


def environment(w, offline):
    e = {'PATH': str(w / 'go/bin') + ':' + str(w / 'mobile-tools') + ':' + str(w / 'jdk/bin') + ':' + str(w / 'flutter/bin') + ':/usr/bin:/bin',
         'HOME': str(w / 'home'), 'TMPDIR': str(w / 'tmp'), 'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8',
         'XDG_CONFIG_HOME': str(w / 'config'), 'XDG_CACHE_HOME': str(w / 'config/cache'),
         'PUB_CACHE': str(w / 'pub-cache'), 'GOPATH': str(w / 'gopath'), 'GOBIN': str(w / 'mobile-tools'),
         'GOCACHE': str(w / 'go-cache'), 'GOMODCACHE': str(w / 'go-mod'), 'GOENV': 'off',
         'GOTOOLCHAIN': 'local', 'GOMAXPROCS': '2', 'GOFLAGS': '-p=1',
         'GOPROXY': 'off' if offline else 'https://proxy.golang.org,direct',
         'GOSUMDB': 'off' if offline else 'sum.golang.org',
         'JAVA_HOME': str(w / 'jdk'), 'JAVA_TOOL_OPTIONS': '-Duser.home=' + str(w / 'home'),
         'GRADLE_USER_HOME': str(w / 'gradle-cache'), 'ANDROID_HOME': str(w / 'android'),
         'ANDROID_NDK_HOME': str(w / 'android/ndk/28.2.13676358'),
         'ANDROID_USER_HOME': str(w / 'home/.android'), 'ANDROID_NATIVE_WORK': str(w),
         'FLUTTER_SUPPRESS_ANALYTICS': 'true', 'DART_SUPPRESS_ANALYTICS': 'true', 'CI': 'true',
         'GIT_CONFIG_GLOBAL': '/dev/null', 'GIT_CONFIG_NOSYSTEM': '1', 'GIT_TERMINAL_PROMPT': '0'}
    return e


def netns():
    links = subprocess.check_output(['ip', '-o', 'link', 'show'], text=True).splitlines()
    if len(links) != 1 or ': lo:' not in links[0]:
        raise ValueError('offline stages require a fresh loopback-only netns')
    for family in ['-4', '-6']:
        if subprocess.check_output(['ip', family, 'route', 'show', 'default']).strip():
            raise ValueError('external route in native build namespace')
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as c:
        c.settimeout(.2)
        try:
            c.connect(('192.0.2.1', 9))
        except OSError as error:
            if error.errno != errno.ENETUNREACH:
                raise ValueError('offline external no-route proof failed') from error
        else:
            raise ValueError('external network available')


class Job:
    def __init__(self, work, inputs, tools, budget):
        self.w, self.inputs, self.tools, self.b = work, inputs, tools, budget

    def cmd(self, args, cwd, env, label, timeout=900, reject=None, stdin=None):
        return s.run(args, cwd, env, self.w, self.b, label, timeout, reject, stdin)

    def verify(self):
        return verify_inputs.verify(self.w / 'source', self.inputs)

    def git(self, path, repository, revision, env, label):
        s.mkdir(path)
        self.cmd(['git', 'init', '--quiet', path], self.w, env, label + '-init')
        self.cmd(['git', '-C', path, 'remote', 'add', 'origin', repository], self.w, env, label + '-remote')
        self.cmd(['git', '-C', path, 'fetch', '--depth=1', 'origin', revision], self.w, env, label + '-fetch')
        log = self.cmd(['git', '-C', path, 'rev-parse', 'FETCH_HEAD^{commit}'], self.w, env, label + '-commit')
        if log.read_text().strip() != revision:
            raise ValueError('official fixed commit mismatch')

    def tool(self, key, pin, final, env):
        ext = '.zip' if pin['url'].endswith('.zip') else '.tar.gz'
        archive = self.w / 'downloads' / (key + ext)
        receipt = s.download(pin, archive, self.b)
        extracted = self.w / ('extract-' + key)
        s.unpack(archive, extracted, self.b)
        children = list(extracted.iterdir())
        if len(children) != 1 or not children[0].is_dir() or children[0].is_symlink():
            raise ValueError('single official tool directory required')
        s.mkdir(final.parent)
        children[0].rename(final)
        s.sync(final.parent)
        receipt.update({'owned_archive': str(archive.relative_to(self.w)), 'extracted_root': str(final.relative_to(self.w)),
                        'archive_release_policy': 'only this exact verified downloaded compressed file; no dependency/cache/evidence removal'})
        s.write_json(self.w / 'evidence' / ('download-' + key + '.json'), receipt)
        if s.sha(archive) != receipt['sha256']:
            raise ValueError('download changed before exact archive release')
        archive.unlink()
        s.sync(archive.parent)
        s.write_json(self.w / 'evidence' / ('released-download-' + key + '.json'), {
            'path': receipt['owned_archive'], 'sha256': receipt['sha256'], 'bytes': receipt['bytes'],
            'reason': 'verified exact tool already extracted; cold-job generated archive only', 'source_or_cache_removed': False})

    def source(self, env):
        checkout = self.w / 'source-git'
        self.git(checkout, SOURCE_REPO, SOURCE_REF, env, 'frozen257')
        wanted = [x for x in self.inputs['files'] if not x['path'].startswith(CORE_TOOLS + '/')]
        archive = self.w / 'downloads/frozen257.tar'
        self.cmd(['git', '-C', checkout, 'archive', '--format=tar', '--output=' + str(archive), SOURCE_REF, '--']
                 + [x['path'] for x in wanted], self.w, env, 'source-archive')
        expected = {x['path']: x for x in wanted}
        seen = set()
        with tarfile.open(archive) as t:
            for member in t:
                self.b.check()
                if member.isdir():
                    continue
                if not member.isfile() or member.name not in expected or member.name in seen:
                    raise ValueError('unexpected frozen source archive member')
                body = t.extractfile(member).read()
                row = expected[member.name]
                if len(body) != row['bytes'] or hashlib.sha256(body).hexdigest() != row['sha256']:
                    raise ValueError('frozen source commit body mismatch')
                s.put(self.w / 'source' / s.name(member.name), body)
                seen.add(member.name)
        if seen != expected.keys():
            raise ValueError('missing frozen source members')
        for x in self.inputs['files']:
            if x['path'].startswith(CORE_TOOLS + '/'):
                s.put(self.w / 'source' / x['path'], (REPO / x['path']).read_bytes())
        self.verify()
        s.write_json(self.w / 'evidence/source.json', {'source_repository': SOURCE_REPO, 'source_ref': SOURCE_REF,
            'source_archive_sha256': s.sha(archive), 'members': 426, 'source_digest': self.inputs['source_digest'],
            'business257_not_live_source_alias': True, 'controller_workflow_commit': os.environ.get('GITHUB_SHA'),
            'Root_accepted_Dart_full688': True, 'new_native_acceptance': False})

    def prepare(self):
        w, t = self.w, self.tools
        for name in ['evidence', 'artifacts', 'downloads', 'tmp', 'home', 'config', 'pub-cache',
                     'go-cache', 'go-mod', 'gopath', 'mobile-tools', 'gradle-cache', 'job-private']:
            s.mkdir(w / name)
        e = environment(w, False)
        self.source(e)
        for key in ['go', 'jdk', 'gradle']:
            self.tool(key, t[key], w / key, e)
        self.tool('commandline', t['android_commandline_tools'], w / 'android/cmdline-tools/22.0', e)
        self.tool('ndk', t['ndk'], w / 'android/ndk/28.2.13676358', e)
        for label, args in [('go-version', [w / 'go/bin/go', 'version']), ('java-version', [w / 'jdk/bin/java', '-version']),
                            ('gradle-version', [w / 'gradle/bin/gradle', '--version'])]:
            self.cmd(args, w, e, label)
        if 'go1.27.2 ' not in (w / 'evidence/go-version.log').read_text():
            raise ValueError('Go version mismatch')
        if '17.0.20.1' not in (w / 'evidence/java-version.log').read_text():
            raise ValueError('JDK version mismatch')
        sdk = w / 'android/cmdline-tools/22.0/bin/sdkmanager'
        self.cmd([sdk, '--sdk_root=' + str(w / 'android'), '--licenses'], w, e, 'sdk-licenses', stdin=b'y\n' * 1000)
        extra = t['cloud_dependency_additions']
        packages = t['android_sdk']['required_packages'] + extra['required_SDK_packages']
        catalog = w / 'evidence/official-SDK-repository2-3.xml'
        catalog_receipt = s.download({'url': 'https://dl.google.com/android/repository/repository2-3.xml',
                                     'max_bytes': 16 * 1024 ** 2}, catalog, self.b)
        selected_archives = sdk_catalog(catalog.read_bytes(), packages)
        self.cmd([sdk, '--verbose', '--sdk_root=' + str(w / 'android')] + [x['path'] for x in packages], w, e, 'sdk-packages', 1200)
        sdk_rows = []
        for item in packages:
            root = w / 'android' / item['path'].replace(';', '/')
            prop = (root / 'source.properties').read_text()
            if not re.search(r'^Pkg.Revision\s*=\s*' + re.escape(item['revision']) + r'\s*$', prop, re.M):
                raise ValueError('SDK package revision mismatch: ' + item['path'])
            sdk_rows.append({'package': item['path'], 'revision': item['revision'],
                'source_properties_sha256': s.sha(root / 'source.properties'),
                'installed_package_xml_sha256': s.sha(root / 'package.xml')})
        for pin in t['android_sdk']['same_crossplatform_API_inputs'] + extra['crossplatform_API_inputs']:
            if s.sha(w / 'android' / pin['path']) != pin['sha256']:
                raise ValueError('Android platform API input SHA mismatch')
        s.write_json(w / 'evidence/sdk-packages.json', {'packages': sdk_rows,
            'official_repository_metadata': catalog_receipt, 'selected_official_Linux_archives': selected_archives,
            'source': 'hash-verified sdkmanager22.0 validates archive checksums; verbose log and exact platform bytes corroborate installation',
            'Linux_archive_SHA256_preexisting_lock': False})
        flutter = t['flutter']
        self.git(w / 'flutter', flutter['repository'], flutter['framework_revision'], e, 'flutter')
        self.cmd(['git', '-C', w / 'flutter', 'fetch', '--depth=1', 'origin', 'refs/tags/' + flutter['tag'] + ':refs/tags/' + flutter['tag']], w, e, 'flutter-tag')
        self.cmd(['git', '-C', w / 'flutter', 'checkout', '--quiet', '--detach', 'refs/tags/' + flutter['tag']], w, e, 'flutter-checkout')
        if (w / 'flutter/bin/internal/engine.version').read_text().strip() != flutter['engine_revision']:
            raise ValueError('Flutter engine revision mismatch')
        f = w / 'flutter/bin/flutter'
        self.cmd([f, 'config', '--no-analytics'], w, e, 'flutter-config')
        version = self.cmd([f, '--version', '--machine'], w, e, 'flutter-version')
        v = json.loads(version.read_text())
        for key, want in [('frameworkVersion', flutter['tag']), ('frameworkRevision', flutter['framework_revision']),
                          ('engineRevision', flutter['engine_revision']), ('dartSdkVersion', flutter['dart_version'])]:
            if v.get(key) != want:
                raise ValueError('official Flutter version mismatch: ' + key)
        self.cmd([f, 'precache', '--android'], w, e, 'android-engine', 1200)
        self.cmd([f, 'pub', 'get', '--enforce-lockfile'], w / 'source/app', e, 'pub-get', 1200)
        self.check_packages()
        for pin in extra['locked_pub_source_evidence']:
            if s.sha(w / 'pub-cache/hosted/pub.dev' / pin['path']) != pin['sha256']:
                raise ValueError('locked native dependency build source differs')
        asset = t['sqlite_Android_ARM64_if_hook_reachable']
        package = w / 'pub-cache/hosted/pub.dev/sqlite3-3.5.2'
        if ("'" + asset['filename'] + "': '" + asset['sha256'] + "'") not in (package / 'lib/src/hook/asset_hashes.dart').read_text():
            raise ValueError('locked SQLite Android native hash differs')
        target = w / 'source' / asset['fresh_seed_relative']
        s.mkdir(target.parent)
        receipt = s.download(asset, target, self.b)
        s.write_json(w / 'evidence/sqlite-Android-native.json', {**receipt, 'old_hook_snapshot_or_stamp': False})
        s.put(w / 'source/app/android/local.properties', ('sdk.dir=' + str(w / 'android') + '\nflutter.sdk=' + str(w / 'flutter')
            + '\ncmake.dir=' + str(w / 'android/cmake/3.22.1') + '\n').encode())
        ca = w / 'job-private/job-ca.pem'
        key = w / 'job-private/job-ca.key'
        self.cmd(['openssl', 'version'], w, e, 'openssl-version')
        if not (w / 'evidence/openssl-version.log').read_text().startswith('OpenSSL 3.'):
            raise ValueError('OpenSSL3 required for reviewed job CA generation')
        self.cmd(['openssl', 'req', '-x509', '-newkey', 'rsa:3072', '-sha256', '-noenc', '-days', '2',
                  '-keyout', key, '-out', ca, '-subj', '/CN=LuoboIM-current257-cloud-build-job',
                  '-addext', 'basicConstraints=critical,CA:TRUE', '-addext', 'keyUsage=critical,keyCertSign,cRLSign'], w, e, 'job-CA')
        key.chmod(0o600)
        key.unlink()
        s.sync(key.parent)
        s.put(w / 'artifacts/job-ca-public.pem', ca.read_bytes())
        s.write_json(w / 'evidence/job-CA.json', {'public_SHA256': s.sha(ca), 'private_key_deleted': True,
            'source': 'fresh job-only2day CA, not old fixture CA', 'system_trust_modified': False})
        s.write_json(w / 'evidence/runner-system.json', {'ImageOS': os.environ.get('ImageOS'),
            'ImageVersion': os.environ.get('ImageVersion'), 'RUNNER_ARCH': os.environ.get('RUNNER_ARCH'),
            'Python': sys.version, 'openssl_version_log_sha256': s.sha(w / 'evidence/openssl-version.log')})
        self.git(w / 'core-upstream', CORE_REPO, CORE_REF, e, 'core-upstream')
        self.cmd(self.builder(w / 'core-prefetch', prepare=True), w, e, 'core-prepare')
        go = w / 'go/bin/go'
        m = self.cmd([go, 'mod', 'download', '-json', 'golang.org/x/mobile@' + t['mobile']['version']], w / 'core-prefetch', e, 'mobile-module')
        module = json.loads(m.read_text())
        if module.get('Sum') != t['mobile']['sum'] or module.get('GoModSum') != t['mobile']['go_mod_sum']:
            raise ValueError('fixed mobile module checksum mismatch')
        for tool in ['gomobile', 'gobind']:
            self.cmd([go, 'install', '-trimpath', 'golang.org/x/mobile/cmd/' + tool + '@' + t['mobile']['version']], w / 'core-prefetch', e, 'install-' + tool, 1200)
        # Do NOT call gomobile init: this pinned revision installs gobind@latest.
        self.cmd([go, 'mod', 'download', 'all'], w / 'core-prefetch', e, 'go-dependencies', 1200)
        self.cmd([go, 'mod', 'verify'], w / 'core-prefetch', e, 'go-verify')
        self.cmd([go, 'list', '-m', '-json', 'all'], w / 'core-prefetch', e, 'go-module-graph')
        if s.sha(w / 'core-prefetch/go.sum') != t['locks']['patched_core_go_sum']['sha256']:
            raise ValueError('reviewed core go.sum changed during acquisition')
        s.write_json(w / 'evidence/mobile-tools.json', {'version': t['mobile']['version'], 'commit': t['mobile']['commit'],
            'gomobile_sha256': s.sha(w / 'mobile-tools/gomobile'), 'gobind_sha256': s.sha(w / 'mobile-tools/gobind'),
            'gomobile_init_or_floating_latest': False})
        self.verify()

    def check_packages(self):
        config = self.w / 'source/app/.dart_tool/package_config.json'
        for item in json.loads(config.read_text())['packages']:
            uri = urlsplit(item['rootUri'])
            if uri.scheme not in ('', 'file') or uri.netloc:
                raise ValueError('local fresh package mapping required')
            path = Path(unquote(uri.path))
            path = (path if path.is_absolute() else config.parent / path).resolve()
            roots = [self.w / 'flutter', self.w / 'pub-cache', self.w / 'source/app',
                     self.w / 'source/packages/flutter_openim_sdk']
            if not any(path == root or path.is_relative_to(root) for root in roots):
                raise ValueError('package mapping outside fresh job')
            if item['name'] == 'imapp' and path != self.w / 'source/app':
                raise ValueError('self package is not fixed257')
            if item['name'] == 'flutter_openim_sdk' and path != self.w / 'source/packages/flutter_openim_sdk':
                raise ValueError('SDK package is not reviewed source')

    def builder(self, directory, prepare=False):
        w = self.w
        command = [sys.executable, '-B', w / 'source' / CORE_TOOLS / 'build_candidate.py',
            '--upstream', w / 'core-upstream', '--protocol', w / 'source/server/pkg/protocol',
            '--work', directory, '--go', w / 'go/bin/go',
            '--qa-fixture-ca', w / 'job-private/job-ca.pem',
            '--qa-fixture-ca-sha256', s.sha(w / 'job-private/job-ca.pem'), '--android-targets', 'android/arm64']
        if prepare:
            return command + ['--prepare-only']
        return command + ['--output', w / 'artifacts/core-current257-arm64.aar', '--mobile-tools', w / 'mobile-tools',
            '--android-sdk', w / 'android', '--ndk', w / 'android/ndk/28.2.13676358', '--java-home', w / 'jdk']

    def build_core(self):
        netns()
        self.verify()
        e = environment(self.w, True)
        self.cmd(self.builder(self.w / 'core-build'), self.w, e, 'core-build', 1800)
        if s.sha(self.w / 'core-build/go.sum') != self.tools['locks']['patched_core_go_sum']['sha256']:
            raise ValueError('reviewed core go.sum changed during offline build')
        aar = self.w / 'artifacts/core-current257-arm64.aar'
        meta = json.loads(aar.with_suffix('.build.json').read_text())
        proof = audit.core(aar, meta, s.sha(self.w / 'job-private/job-ca.pem'))
        contract = json.loads((HERE / 'bindings.json').read_text())
        proof['binding_API'] = audit.bindings(aar.with_name(aar.stem + '-sources.jar'), contract)
        s.write_json(self.w / 'evidence/core-audit.json', proof)
        s.put(self.w / 'artifacts/NOTICE-public-core.txt', (self.w / 'source' / CORE_TOOLS / 'UPSTREAM-LICENSE-APACHE-2.0').read_bytes())
        self.verify()

    def gradle(self, task, offline=True, aar=None, digest=None):
        w = self.w
        aar = aar or w / 'artifacts/core-current257-arm64.aar'
        cmd = [w / 'gradle/bin/gradle', '--no-daemon', '--max-workers=1', '--no-watch-fs', '--console=plain',
            '--project-cache-dir', w / 'gradle-project-cache', '-Dorg.gradle.jvmargs=-Xmx1536m -XX:MaxMetaspaceSize=512m',
            '-Pkotlin.compiler.execution.strategy=in-process', '-Ptarget=lib/main.dart',
            '-Ptarget-platform=android-arm64', '-Psplit-per-abi=true', task,
            '-PluoboQaCoreAar=' + str(aar)]
        if digest is not False:
            cmd.append('-PluoboQaCoreSha256=' + (digest or s.sha(aar)))
        if offline:
            cmd += ['--offline', '--dependency-verification', 'strict']
        return cmd

    def gradle_prepare(self):
        self.verify()
        e = environment(self.w, False)
        command = self.gradle('dependencies', offline=False) + ['--info', '-I', HERE / 'resolve.gradle', '--write-verification-metadata', 'sha256']
        log = self.cmd(command, self.w / 'source/app/android', e, 'gradle-dependency-setup', 1800)
        proof = maven_origins(self.w, log.read_text(errors='replace'))
        xml = self.w / 'source/app/android/gradle/verification-metadata.xml'
        if not xml.is_file() or '<sha256 ' not in xml.read_text():
            raise ValueError('new actual dependency verification metadata required')
        s.put(self.w / 'evidence/gradle-verification-metadata.xml', xml.read_bytes())
        proof['verification_metadata_sha256'] = s.sha(xml)
        proof['preexisting_hermetic_lock'] = False
        s.write_json(self.w / 'evidence/dependency-origin-audit.json', proof)
        self.verify()

    def check_java_lock(self):
        expected = json.loads((self.w / 'evidence/dependency-origin-audit.json').read_text())
        if s.sha(self.w / 'source/app/android/gradle/verification-metadata.xml') != expected['verification_metadata_sha256']:
            raise ValueError('actual dependency verification metadata changed')

    def build_apk(self):
        netns()
        w = self.w
        self.verify()
        self.check_java_lock()
        self.check_packages()
        e = environment(w, True)
        cwd = w / 'source/app/android'
        aar = w / 'artifacts/core-current257-arm64.aar'
        alias = w / 'negative/missing-provenance.aar'
        s.put(alias, aar.read_bytes())
        cases = [(task.lower(), self.gradle(task), 'QA core requires assembleDebug')
                 for task in ['assembleRelease', 'assembleProfile', 'assemble']]
        cases += [('missing-sha', self.gradle('assembleDebug', digest=False), 'QA core requires an explicit AAR path and lowercase SHA256.'),
                  ('wrong-sha', self.gradle('assembleDebug', digest='0' * 64), 'QA core AAR checksum mismatch.'),
                  ('missing-manifest', self.gradle('assembleDebug', aar=alias, digest=s.sha(aar)), 'QA core requires its bounded, auditable build manifest.')]
        for label, args, reason in cases:
            self.check_java_lock()
            self.cmd(args, cwd, e, 'gate-' + label, 180, reason)
            self.verify()
        altered = json.loads(aar.with_suffix('.build.json').read_text())
        altered['qa_overlay_sha256'] = '0' * 64
        s.write_json(alias.with_suffix('.build.json'), altered)
        self.cmd(self.gradle('assembleDebug', aar=alias, digest=s.sha(aar)), cwd, e, 'gate-wrong-overlay', 180,
                 'QA core build manifest does not match the reviewed debug trust artifact.')
        if list((w / 'source/app/build').rglob('*.apk')):
            raise ValueError('negative gate unexpectedly emitted APK')
        self.check_java_lock()
        log = self.cmd(self.gradle('assembleDebug'), cwd, e, 'assemble-debug', 1800)
        text = log.read_text(errors='replace')
        if not re.search(r'^BUILD SUCCESSFUL in .+$', text, re.M) or re.search(r'^BUILD FAILED', text, re.M):
            raise ValueError('complete positive Gradle success marker missing')
        for task in [':flutter_openim_sdk:compileDebugJavaWithJavac', ':app:compileDebugKotlin']:
            if not re.search(r'^> Task ' + re.escape(task) + r'\s*$', text, re.M):
                raise ValueError('actual uncached wrapper/native bridge compilation not observed: ' + task)
        outputs = list((w / 'source/app/build/app/outputs/apk/debug').glob('*.apk'))
        if len(outputs) != 1:
            raise ValueError('exactly one arm64 debug APK required')
        final = w / 'artifacts/luobo-current257-auditable-arm64-debug.apk'
        s.put(final, outputs[0].read_bytes())
        def execute(args, directory, environment_, label, timeout):
            return self.cmd(args, directory, environment_, label, timeout)
        proof = audit.apk(final, w, execute, e, self.tools, aar)
        proof.update({'negative_gates': 7, 'all29_actual_wrapper_sources_compiled_by_real_app': True,
                      'source_members': 426, 'source_digest': self.inputs['source_digest'], 'entry': 'lib/main.dart',
                      'business_defines_or_old_CA_or_device_operations': False})
        s.write_json(w / 'evidence/APK-audit.json', proof)
        self.verify()
        self.check_java_lock()
        self.check_packages()
        if (w / 'job-private/job-ca.key').exists() or any(p.is_symlink() for p in (w / 'artifacts').rglob('*')):
            raise ValueError('private key or artifact symlink must not be published')


def sdk_catalog(data, wanted):
    def local(e): return e.tag.rsplit('}', 1)[-1]
    def child(e, tag): return next((x for x in e if local(x) == tag), None)
    root = ET.fromstring(data)
    rows = []
    for pin in wanted:
        packages = [e for e in root if local(e) == 'remotePackage' and e.get('path') == pin['path']]
        matches = []
        for package in packages:
            revision = child(package, 'revision')
            values = [int(child(revision, k).text) if child(revision, k) is not None else 0 for k in ['major', 'minor', 'micro']]
            expected = [int(x) for x in pin['revision'].split('.')]
            if values != expected + [0] * (3 - len(expected)):
                continue
            archives = child(package, 'archives')
            for archive in archives:
                host = child(archive, 'host-os')
                if host is not None and host.text != 'linux':
                    continue
                complete = child(archive, 'complete')
                if complete is None:
                    continue
                url = child(complete, 'url').text.strip()
                parsed = urlsplit(url)
                if parsed.scheme:
                    if (parsed.scheme != 'https' or parsed.hostname != 'dl.google.com' or parsed.username
                            or parsed.password or parsed.query or parsed.fragment
                            or not parsed.path.startswith('/android/repository/')):
                        raise ValueError('unexpected SDK archive URL')
                    final_url = url
                elif url.startswith('/') or '..' in url.split('/'):
                    raise ValueError('unexpected SDK archive URL')
                else:
                    final_url = 'https://dl.google.com/android/repository/' + url
                checksum = child(complete, 'checksum')
                kind = checksum.get('type', 'sha1')
                text = (checksum.text or '').strip()
                if (kind not in ('sha1', 'sha256') or not re.fullmatch(r'[0-9a-f]{' + ('40' if kind == 'sha1' else '64') + r'}', text)):
                    raise ValueError('unsupported official SDK archive checksum')
                matches.append({'package': pin['path'], 'revision': pin['revision'],
                    'url': final_url,
                    'size': int(child(complete, 'size').text),
                    'checksum_type': kind, 'checksum': text})
        if len(matches) != 1:
            raise ValueError('exact official Linux SDK revision/archive unavailable: ' + pin['path'])
        rows.extend(matches)
    return rows


def maven_origins(work, log):
    roots = Path(work) / 'gradle-cache/caches/modules-2/files-2.1'
    # Documentation/warning links are not acquisition evidence. Only Gradle's
    # actual Downloading lines can account for a fresh cached Maven artifact.
    urls = re.findall(r'\bDownloading\s+(https://[^\s<>"\]]+)', log)
    allowed = {'dl.google.com', 'maven.google.com', 'repo.maven.apache.org', 'repo1.maven.org',
               'plugins.gradle.org', 'plugins-artifacts.gradle.org', 'storage.googleapis.com', 'jitpack.io'}
    downloads = []
    for url in urls:
        parsed = urlsplit(url.rstrip(').,'))
        if parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.hostname not in allowed:
            raise ValueError('unapproved actual dependency URL')
        if parsed.hostname == 'jitpack.io' and not parsed.path.startswith('/com/github/davidliu/audioswitch/039a35aefab7747c557242fa216c9ea11743b604/'):
            raise ValueError('unapproved JitPack module/version')
        downloads.append(parsed.geturl())
    rows = []
    for path in sorted(roots.rglob('*')):
        if path.is_file():
            rel = path.relative_to(roots)
            if path.is_symlink() or len(rel.parts) != 5:
                raise ValueError('unexpected fresh Maven cache path')
            group, module, version, content_key, filename = rel.parts
            if group == 'luobo.qa.openim':
                origin = ['owned:new_hash_checked_job_AAR']
            else:
                suffix = '/' + group.replace('.', '/') + '/' + module + '/' + version + '/' + filename
                origin = sorted({u for u in downloads if unquote(urlsplit(u).path).endswith(suffix)})
                if not origin:
                    raise ValueError('fresh Maven artifact has no captured official download URL: ' + str(rel))
            rows.append({'coordinate': ':'.join([group, module, version]), 'filename': filename,
                         'sha256': s.sha(path), 'bytes': path.stat().st_size, 'observed_sources': origin})
    if not rows:
        raise ValueError('actual Maven acquisition evidence missing')
    return {'artifacts': rows, 'all_actual_artifact_sources_accounted': True,
            'sources_are_first_acquisition_evidence_not_prior_hermetic_pins': True}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--cohort', required=True, choices=[COHORT])
    p.add_argument('--stage', choices=['readonly', 'admission'] + STAGES, default='readonly')
    p.add_argument('--work')
    args = p.parse_args()
    registry, inputs, tools = metadata()
    if args.stage == 'readonly':
        print(json.dumps({'state': 'frozen257_metadata_verified_no_materialization', 'cohort': COHORT,
                          'members': 426, 'source_ref': SOURCE_REF, 'live_App_reads': 0}))
        return
    if not args.work or sys.platform != 'linux' or os.geteuid() == 0:
        p.error('cloud Linux ordinary-user stage and explicit fresh work required')
    if args.stage == 'admission':
        work_path(args.work, True)
        print(json.dumps({'state': 'cold20GiB_admission_only_zero_materialization', 'cohort': COHORT}))
        return
    w = work_path(args.work, args.stage == 'setup')
    if args.stage == 'setup':
        s.mkdir(w)
        s.mkdir(w / 'evidence')
        s.write_json(w / 'activation.json', {'cohort': COHORT, 'source_ref': SOURCE_REF,
            'inputs_sha256': registry['inputs_sha256'], 'work': str(w), 'controller_GITHUB_SHA': os.environ.get('GITHUB_SHA')})
    else:
        activation = json.loads((w / 'activation.json').read_text())
        if activation['cohort'] != COHORT or activation['source_ref'] != SOURCE_REF or activation['inputs_sha256'] != registry['inputs_sha256']:
            raise ValueError('foreign activation')
        previous = STAGES[STAGES.index(args.stage) - 1]
        if ((w / 'evidence' / (previous + '-failure.json')).exists()
                or json.loads((w / 'evidence' / (previous + '-complete.json')).read_text())['state'] != 'passed'
                or json.loads((w / 'evidence' / (previous + '-final-resource.json')).read_text())['state'] != 'passed'):
            raise ValueError('successful immediately preceding phase required')
        v = os.statvfs(w)
        if v.f_bavail * v.f_frsize < s.INITIAL:
            raise ValueError('subsequent4GiB phase admission failed')
        h = os.statvfs(REPO)
        if h.f_bavail * h.f_frsize < s.INITIAL:
            raise ValueError('subsequent4GiB host phase admission failed')
    s.write_json(w / 'evidence' / (args.stage + '-claim.json'), {'state': 'claimed_once_no_partial_resume'})
    budget = s.Budget(w, REPO)
    failed = None
    try:
        budget.start()
        job = Job(w, inputs, tools, budget)
        {'setup': job.prepare, 'core': job.build_core, 'gradle-setup': job.gradle_prepare, 'apk': job.build_apk}[args.stage]()
        result = {'state': 'passed', 'cohort': COHORT, 'stage': args.stage, **budget.record()}
        # The success candidate is fsynced while polling, then final sample/join
        # rechecks latched failure. A failure file/nonzero always wins.
        s.write_json(w / 'evidence' / (args.stage + '-complete.json'), result)
        s.write_json(w / 'evidence' / (args.stage + '-final-resource.json'), {'state': 'passed',
            'error': None, 'record_before_final_sample_failure_file_always_wins': True, **budget.record()})
        budget.finish()
        print(json.dumps({'phase': args.stage, 'state': 'passed', **budget.record()}), flush=True)
    except BaseException as error:
        failed = type(error).__name__ + ': ' + str(error)
        budget.latch('phase_or_infrastructure_failed')
        raise
    finally:
        budget.stop.set()
        if budget.thread is not None:
            budget.thread.join()
        if failed or budget.reason:
            s.write_json(w / 'evidence' / (args.stage + '-failure.json'), {'state': 'failed', 'error': failed, **budget.record()})


if __name__ == '__main__':
    main()
