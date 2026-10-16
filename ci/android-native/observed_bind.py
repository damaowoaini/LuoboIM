#!/usr/bin/env python3
"""New explicit native cohort; old427/r1/r2 remain immutable borrowed inputs."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import stat
import sys
import tarfile

import generated_module_probe as projection
import go_observer as observer
import gradle_selector_fixture as selectors
import gradle_variant_fixture as variants
import run as parent
import support as s
import verify_inputs

HERE = Path(__file__).resolve().parent
COHORT = observer.COHORT
BUILDER = parent.CORE_TOOLS + '/build_observed_candidate.py'
REVISION = 'root-r6-strict-module-artifact-view'
STAGES = ['setup', 'core', 'gradle-controls', 'gradle-setup', 'apk']


def metadata():
    registry, _, tools = projection.metadata()
    own = json.loads((HERE / 'observed-cohort.json').read_text())
    if (own['cohort'] != COHORT or own['source_ref'] != parent.SOURCE_REF
            or own['controller_revision'] != REVISION):
        raise ValueError('explicit historical257 observed-bind identity required')
    for row in own['controller_inputs']:
        file = verify_inputs.checked_path(parent.REPO, row['path'])
        if file.stat().st_size != row['bytes'] or s.sha(file) != row['sha256']:
            raise ValueError('observed-bind execution input differs: ' + row['path'])
    inputs = json.loads((HERE / 'observed-inputs.json').read_text())
    if (inputs['members'] != 428 or inputs['cohort'] != COHORT
            or len(inputs['files']) != 428 or len({x['path'] for x in inputs['files']}) != 428
            or inputs['source_digest'] != own['source_digest']
            or verify_inputs.digest(inputs['files']) != own['source_digest']):
        raise ValueError('exact derived428 source identity required')
    for row in inputs['files']:
        if row['path'].startswith(parent.CORE_TOOLS + '/'):
            file = verify_inputs.checked_path(parent.REPO, row['path'])
            if file.stat().st_size != row['bytes'] or s.sha(file) != row['sha256']:
                raise ValueError('derived core tooling differs')
    return own, inputs, tools


class Job(parent.Job):
    def cmd(self, args, cwd, env, label, timeout=900, reject=None, stdin=None):
        if label == 'gradle-dependency-setup':
            if getattr(self, '_typed_resolver_dispatch', None) is not False:
                raise ValueError('one dispatch only inside inherited gradle_prepare')
            original = self.gradle('dependencies', offline=False) + [
                '--info', '-I', parent.HERE / 'resolve.gradle', '--write-verification-metadata', 'sha256']
            if (list(map(str, args)) != list(map(str, original))
                    or cwd != self.w / 'source/app/android'
                    or env != parent.environment(self.w, False)
                    or timeout != 1800 or reject is not None or stdin is not None):
                raise ValueError('inherited dependency command/cwd/env contract differs')
            changed = list(args)
            changed[changed.index('-I') + 1] = HERE / 'resolve-observed.gradle'
            s.write_json(self.w / 'evidence/observed-resolver-dispatch.json', {
                'controller_revision': REVISION, 'original_argv': list(map(str, args)),
                'derived_argv': list(map(str, changed)), 'only_change': '-I value',
                'original_resolver_sha256': s.sha(parent.HERE / 'resolve.gradle'),
                'derived_resolver_sha256': s.sha(HERE / 'resolve-observed.gradle')})
            self._typed_resolver_dispatch = True
            args = changed
        return super().cmd(args, cwd, env, label, timeout, reject, stdin)

    def gradle_prepare(self):
        # Reuse the original method, including all source, Maven-origin and
        # verification-metadata checks. Only its one -I argument is replaced.
        self._typed_resolver_dispatch = False
        try:
            result = super().gradle_prepare()
            if self._typed_resolver_dispatch is not True:
                raise ValueError('inherited dependency dispatch was not observed')
            return result
        finally:
            self._typed_resolver_dispatch = None

    def gradle_controls(self):
        parent.netns()
        self.verify()
        for name in selectors.OUTPUTS:
            if (self.w / 'evidence' / name).exists():
                raise ValueError('fixture may not consume preexisting dependency evidence')
        cases = selectors.prepare(self.w)
        cases += variants.prepare(self.w)
        results = []
        for case in cases:
            for name in selectors.OUTPUTS:
                path = self.w / 'evidence' / name
                if path.exists() or path.is_symlink():
                    raise ValueError('fresh resolver output slots required')
            command = [self.w / 'gradle/bin/gradle', '--no-daemon', '--max-workers=1',
                '--no-watch-fs', '--console=plain', '--offline', '--project-cache-dir',
                self.w / 'selector-project-cache' / case['label'],
                '-Dorg.gradle.jvmargs=-Xmx1536m -XX:MaxMetaspaceSize=512m',
                '-I', HERE / 'resolve-observed.gradle', case.get('task', 'dependencies')]
            captured = {}
            control_env = parent.environment(self.w, True)
            # Synthetic file-Maven artifacts must never contaminate the
            # parent's real Maven-origin inventory in gradle-cache.
            control_env['GRADLE_USER_HOME'] = str(self.w / 'selector-gradle-home')
            try:
                log = self.cmd(command, Path(case['cwd']), control_env,
                    'selector-control-' + case['label'], 120, case['expected_rejection'])
            finally:
                captured = selectors.capture_outputs(self.w, case['cwd'])
            proof = variants.check(case, log, captured) if case.get('variant_contract') else selectors.check(case, log, captured)
            row = {'case': case['label'], 'state': 'passed', 'actual_Gradle': True,
                'expected_rejection': case['expected_rejection'], 'outputs': captured, **proof}
            s.write_json(Path(case['cwd']) / 'evidence/result.json', row)
            results.append(row)
        s.write_json(self.w / 'evidence/selector-controls-complete.json', {
            'state': 'passed', 'cases': results, 'count': len(results),
            'netns': 'loopback-only', 'Gradle_offline': True,
            'actual_APK_compile_or_external_JitPack_transfer': False})
        self.verify()

    def verify(self):
        if verify_inputs.digest(self.inputs['files']) != self.inputs['source_digest']:
            raise ValueError('derived source vector differs')
        for row in self.inputs['files']:
            file = verify_inputs.checked_path(self.w / 'source', row['path'])
            if file.stat().st_size != row['bytes'] or s.sha(file) != row['sha256']:
                raise ValueError('derived source body differs: ' + row['path'])

    def source(self, env):
        checkout = self.w / 'source-git'
        self.git(checkout, parent.SOURCE_REPO, parent.SOURCE_REF, env, 'frozen257')
        wanted = {x['path']: x for x in self.inputs['files'] if not x['path'].startswith(parent.CORE_TOOLS + '/')}
        archive = self.w / 'downloads/frozen257.tar'
        self.cmd(['git', '-C', checkout, 'archive', '--format=tar', '--output=' + str(archive),
                  parent.SOURCE_REF, '--', *wanted], self.w, env, 'source-archive')
        seen = set()
        with tarfile.open(archive) as bundle:
            for member in bundle:
                if member.isdir():
                    continue
                if not member.isfile() or member.name not in wanted or member.name in seen:
                    raise ValueError('unexpected historical source archive entry')
                body = bundle.extractfile(member).read()
                row = wanted[member.name]
                if len(body) != row['bytes'] or hashlib.sha256(body).hexdigest() != row['sha256']:
                    raise ValueError('fixed source commit body differs')
                s.put(self.w / 'source' / s.name(member.name), body)
                seen.add(member.name)
        if seen != wanted.keys():
            raise ValueError('missing historical source')
        for row in self.inputs['files']:
            if row['path'].startswith(parent.CORE_TOOLS + '/'):
                s.put(self.w / 'source' / row['path'], (parent.REPO / row['path']).read_bytes())
        self.verify()
        s.write_json(self.w / 'evidence/source.json', {'cohort': COHORT,
            'source_repository': parent.SOURCE_REPO, 'source_ref': parent.SOURCE_REF,
            'members': 428, 'source_digest': self.inputs['source_digest'],
            'Git_base_members': len(wanted), 'controller_core_members': 428 - len(wanted),
            'source_archive_sha256': s.sha(archive), 'business257': True, 'not_269_or_live_alias': True,
            'controller_commit': os.environ.get('GITHUB_SHA'), 'native_runtime_acceptance': False})

    def builder(self, directory, prepare=False):
        command = super().builder(directory, prepare)
        command[2] = self.w / 'source' / BUILDER
        if not prepare:
            command += ['--go-observer', self.w / 'observer/go', '--go-observer-sha256', s.sha(HERE / 'go_observer.py'),
                        '--observer-config', self.w / 'observer/config.json',
                        '--observer-config-sha256', s.sha(self.w / 'observer/config.json')]
        return command

    def prepare(self):
        super().prepare()
        self.prewarm_generated_metadata()

    def prewarm_generated_metadata(self):
        """Real r2 projection/acquisition in the SAME native job module cache.

        It creates metadata-only output, never the actual bind src directory.
        """
        w, core = self.w, self.w / 'core-prefetch'
        baseline = projection.snapshot(w, core, 'prewarm-core-ready')
        if baseline['go.sum']['sha256'] != projection.CORE_SUM:
            raise ValueError('core8eb9 ready lock required')
        env = {**parent.environment(w, False), 'GOOS': 'linux', 'GOARCH': 'amd64', 'CGO_ENABLED': '1', 'GOWORK': 'off'}
        go, mobile, output = w / 'go/bin/go', w / 'mobile-projector', w / 'generated-module'
        s.mkdir(output)
        def run(label, args, cwd, process_env):
            projection.admit_phase(self.b)
            if projection.snapshot(w, core, label + '-core-before') != baseline:
                raise ValueError('prewarm changed original core lock before command')
            try:
                return self.cmd(args, cwd, process_env, label, 1200)
            finally:
                if projection.snapshot(w, core, label + '-core-after') != baseline:
                    raise ValueError('prewarm changed original core lock after command')
        self.git(mobile, 'https://go.googlesource.com/mobile', projection.MOBILE_COMMIT, env, 'prewarm-mobile')
        run('prewarm-mobile-checkout', ['git', 'checkout', '--quiet', '--detach', projection.MOBILE_COMMIT], mobile, env)
        projection.official_sources(self, mobile, 'prewarm-before')
        projection.source_status(self, mobile, env, 'prewarm-pristine')
        original_mobile = projection.snapshot(w, mobile, 'prewarm-mobile-source')
        try:
            run('prewarm-host-dependencies', [go, 'mod', 'download', 'all'], mobile, env)
        finally:
            mobile_ready = projection.snapshot(w, mobile, 'prewarm-mobile-ready')
        if mobile_ready['go.mod'] != original_mobile['go.mod']:
            raise ValueError('host acquisition changed official mobile GoMod')
        fixture = (HERE / projection.FIXTURE).read_bytes()
        s.put(mobile / 'cmd/gomobile' / projection.FIXTURE, fixture)
        s.put(w / 'evidence/prewarm-projector-test.go', fixture)
        host = {**env, 'GOFLAGS': '-p=1 -mod=readonly', 'GOPROXY': 'off', 'GOSUMDB': 'off',
                'LUOBO_PROJECTION_CORE': str(core), 'LUOBO_PROJECTION_OUT': str(output)}
        run('prewarm-real-projection', [go, 'test', '-v', '-run', '^TestLuoboProjectAndroidModule$',
                                       '-count=1', '-p=1', './cmd/gomobile'], mobile, host)
        if projection.snapshot(w, mobile, 'prewarm-mobile-after-test') != mobile_ready:
            raise ValueError('projection changed host dependency locks')
        original = projection.snapshot(w, output, 'prewarm-projected-original')
        before = (output / 'go.mod').read_bytes()
        projection.expected_projected_acquisition(before, core, w / 'source/server/pkg/protocol')
        target = {**env, 'GOOS': 'android', 'GOARCH': 'arm64'}
        try:
            run('prewarm-metadata-acquire', [go, 'mod', 'download', 'all'], output, target)
        finally:
            acquired = projection.snapshot(w, output, 'prewarm-projected-acquired')
        proof = projection.validate_projected_acquisition(before, (output / 'go.mod').read_bytes(),
            core, w / 'source/server/pkg/protocol')
        if (acquired['go.sum'].get('sha256') != projection.ACQUIRED_SUM_SHA
                or acquired['go.sum'].get('bytes') != 56413):
            raise ValueError('exact observed metadata sum required')
        offline = {**target, 'GOFLAGS': '-p=1 -mod=readonly', 'GOPROXY': 'off', 'GOSUMDB': 'off'}
        for label, args in [('prewarm-metadata-verify', [go, 'mod', 'verify']),
                            ('prewarm-metadata-graph', [go, 'list', '-m', '-json', 'all'])]:
            if projection.snapshot(w, output, label + '-before') != acquired:
                raise ValueError('metadata acquired lock changed before readonly command')
            try:
                run(label, args, output, offline)
            finally:
                if projection.snapshot(w, output, label + '-after') != acquired:
                    raise ValueError('metadata acquired lock changed after readonly command')
        projection.official_sources(self, mobile, 'prewarm-after')
        projection.source_status(self, mobile, env, 'prewarm-final', added=True)
        s.write_json(w / 'evidence/prewarm-complete.json', {'state': 'passed_metadata_only', **proof,
            'original_locks': original, 'acquired_locks': acquired, 'same_job_GOMODCACHE': str(w / 'go-mod'),
            'actual_bind_module_written_or_preseeded': False, 'full_bind_lock': False})
        self.verify()

    def build_apk(self):
        # Preserve the inherited original proof, including its hardcoded427
        # field, and issue an explicit derived-source audit for this428 cohort.
        super().build_apk()
        inherited = self.w / 'evidence/APK-audit.json'
        proof = json.loads(inherited.read_text())
        if proof['source_members'] != 427 or proof['source_digest'] != self.inputs['source_digest']:
            raise ValueError('inherited APK proof identity differs')
        s.write_json(self.w / 'evidence/observed-APK-audit.json', {
            **proof, 'cohort': COHORT, 'source_members': self.inputs['members'],
            'inherited_original_proof': {'path': 'APK-audit.json', 'sha256': s.sha(inherited),
                'historical_hardcoded_source_members': 427},
            'historical257_business_source_not_current287': True})

    def configure_observer(self):
        w = self.w
        s.mkdir(w / 'observer')
        s.put(w / 'observer/go', (HERE / 'go_observer.py').read_bytes())
        (w / 'observer/go').chmod(0o700)
        with (w / 'observer/go').open('rb') as f:
            os.fsync(f.fileno())
        s.sync(w / 'observer')
        base = parent.environment(w, True)['PATH']
        config = {'schema': 1, 'cohort': COHORT, 'job_root': str(w), 'host_root': str(parent.REPO),
            'real_go': str(w / 'go/bin/go'), 'real_go_sha256': s.sha(w / 'go/bin/go'),
            'observer_go': str(w / 'observer/go'), 'observer_sha256': s.sha(HERE / 'go_observer.py'),
            'base_path': str(w / 'go/bin') + ':' + str(w / 'mobile-tools') + ':' + base}
        # The next process requires0600, regardless of this shell's umask.
        observer.record(w / 'observer/config.json', config)
        s.write_json(w / 'evidence/observer-configuration.json', {'config_sha256': s.sha(w / 'observer/config.json'),
            'config_mode': stat.S_IMODE((w / 'observer/config.json').lstat().st_mode),
            **config, 'outer_resource_guard': 'support.Budget dual1GiB/.2s/16GiB; same PG for all observer/Go children',
            'before_after_plus_failure_snapshot_max_body_bytes': 3 * observer.TREE_LIMIT,
            'no_metadata_lock_installed_into_actual_bind': True})

    def build_core(self):
        self.configure_observer()
        success, original_error = False, None
        try:
            super().build_core()
            proof = json.loads((self.w / 'evidence/bind-observer/tidy-result.json').read_text())
            if not proof['tidy_evidence_complete'] or proof['child_exit_code'] != 0 or proof['received_signal']:
                raise ValueError('full actual tidy evidence required, not metadata-only proof')
            claim = json.loads((self.w / 'evidence/bind-observer/tidy-claim.json').read_text())
            actual = Path(claim['cwd'])
            observer.generated_root({'job_root': str(self.w)}, actual)
            observed = projection.snapshot(self.w, actual, 'actual-generated-after-bind')
            target = {**parent.environment(self.w, True), 'GOOS': 'android', 'GOARCH': 'arm64',
                      'CGO_ENABLED': '1', 'GOWORK': 'off', 'GOFLAGS': '-p=1 -mod=readonly'}
            for label, argv in [('actual-generated-verify', ['mod', 'verify']),
                                ('actual-generated-graph', ['list', '-m', '-json', 'all'])]:
                if projection.snapshot(self.w, actual, label + '-before') != observed:
                    raise ValueError('actual generated locks changed before readonly audit')
                try:
                    self.cmd([self.w / 'go/bin/go', *argv], actual, target, label)
                finally:
                    if projection.snapshot(self.w, actual, label + '-after') != observed:
                        raise ValueError('actual generated locks changed during readonly audit')
            success = True
        except BaseException as error:
            original_error = type(error).__name__ + ': ' + str(error)
            raise
        finally:
            locks = self.capture_core_locks(self.w / 'core-build', 'observed-core-final-locks')
            if locks and (locks['go.sum']['sha256'] != projection.CORE_SUM
                          or (self.w / 'core-build/go.mod').read_bytes() != (self.w / 'core-prefetch/go.mod').read_bytes()):
                raise ValueError('actual bind changed original core locks; raw failure evidence retained')
            retained = sorted((self.w / 'tmp').glob('gomobile-work-*'))
            cleanup_error = None
            try:
                if len(retained) > 1:
                    raise ValueError('only one actual ARM64 work directory expected')
                if not success and retained:
                    observer.directory(retained[0])
                    observer.snapshot({'job_root': str(self.w), 'host_root': str(parent.REPO)}, retained[0],
                        self.w / 'evidence/bind-observer/failure-retained', require_complete=False)
            except BaseException as error:
                cleanup_error = type(error).__name__ + ': ' + str(error)
            s.write_json(self.w / 'evidence/actual-bind-observation-final.json', {'actual_core_build_success': success,
                'original_error': original_error, 'retained_capture_error': cleanup_error,
                'retained_work_directories': [str(x.relative_to(self.w)) for x in retained],
                'core_locks': locks, 'metadata_is_not_full_bind_lock': True})
            if cleanup_error:
                raise ValueError('retained generated-source capture failed: ' + cleanup_error)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cohort', required=True, choices=[COHORT])
    parser.add_argument('--stage', choices=['readonly', 'admission', *STAGES], default='readonly')
    parser.add_argument('--work')
    args = parser.parse_args()
    registry, inputs, tools = metadata()
    if args.stage == 'readonly':
        print(json.dumps({'cohort': COHORT, 'controller_revision': REVISION,
                          'source_members': 428, 'source_digest': inputs['source_digest'],
                          'state': 'readonly_no_setup_or_build'}))
        return
    if not args.work or sys.platform != 'linux' or os.geteuid() == 0:
        parser.error('fresh ordinary-user Linux cloud work required')
    w = parent.work_path(args.work, args.stage in ['admission', 'setup'])
    if args.stage == 'admission':
        print('observed-bind cold20GiB admission only')
        return
    if args.stage == 'setup':
        s.mkdir(w)
        s.mkdir(w / 'evidence')
        s.write_json(w / 'activation.json', {'cohort': COHORT, 'source_ref': parent.SOURCE_REF,
            'controller_revision': REVISION, 'registry_sha256': s.sha(HERE / 'observed-cohort.json'),
            'inputs_sha256': s.sha(HERE / 'observed-inputs.json'), 'controller_commit': os.environ.get('GITHUB_SHA')})
    else:
        activation = json.loads((w / 'activation.json').read_text())
        if (activation['cohort'] != COHORT or activation['inputs_sha256'] != s.sha(HERE / 'observed-inputs.json')
                or activation.get('controller_revision') != REVISION
                or activation.get('registry_sha256') != s.sha(HERE / 'observed-cohort.json')):
            raise ValueError('exact observed-bind activation required')
        previous = STAGES[STAGES.index(args.stage) - 1]
        if ((w / 'evidence' / (previous + '-failure.json')).exists()
                or json.loads((w / 'evidence' / (previous + '-complete.json')).read_text())['state'] != 'passed'
                or json.loads((w / 'evidence' / (previous + '-final-resource.json')).read_text())['state'] != 'passed'):
            raise ValueError('complete successful immediately previous stage required')
        for where in [w, parent.REPO]:
            info = os.statvfs(where)
            if info.f_bavail * info.f_frsize < s.INITIAL:
                raise ValueError('next-phase4GiB host/output admission')
    s.write_json(w / 'evidence' / (args.stage + '-claim.json'), {'claimed_once': True})
    budget, failure = s.Budget(w, parent.REPO), None
    try:
        budget.start()
        job = Job(w, inputs, tools, budget)
        {'setup': job.prepare, 'core': job.build_core, 'gradle-controls': job.gradle_controls,
         'gradle-setup': job.gradle_prepare, 'apk': job.build_apk}[args.stage]()
        s.write_json(w / 'evidence' / (args.stage + '-complete.json'), {
            'state': 'passed', 'cohort': COHORT, 'controller_revision': REVISION, **budget.record()})
        s.write_json(w / 'evidence' / (args.stage + '-final-resource.json'), {
            'state': 'passed', 'controller_revision': REVISION, **budget.record()})
        budget.finish()
        print(json.dumps({'cohort': COHORT, 'controller_revision': REVISION,
                          'stage': args.stage, 'state': 'passed', **budget.record()}))
    except BaseException as error:
        failure = type(error).__name__ + ': ' + str(error)
        budget.latch('phase_or_infrastructure_failed')
        raise
    finally:
        budget.stop.set()
        if budget.thread is not None:
            budget.thread.join()
        if failure or budget.reason:
            s.write_json(w / 'evidence' / (args.stage + '-failure.json'), {
                'state': 'failed', 'controller_revision': REVISION, 'error': failure, **budget.record()})


if __name__ == '__main__':
    main()
