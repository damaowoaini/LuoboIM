#!/usr/bin/env python3
"""Collect real x/mobile module projection metadata, never a full-bind lock."""
import argparse
import difflib
import hashlib
import json
import os
from pathlib import Path
import stat
import sys

import run as parent
import support as s

COHORT = 'current257-generated-module-evidence-v1'
HELPERS = {
    'run.py': 'e714f8de746c46354a674594158a028da1ce9d55390a8ec1e1d454c399e91bcb',
    'support.py': '818ed812deab20b25d4e8352f7d31b637039c0a881c8fc0fa070bc2ce1ea8fbd',
    'audit.py': '1edf23b5ce32258e3e37d7af615b672e764de8193540aaa7de8a6fc03888d974',
    'verify_inputs.py': '266a89fe31cbd9c40aa566a8f8f1404d9a04fed2a9a736a6cafac7e9142eede5',
    'cohort.json': '34ec3ae5007774de773af66de98d07de450ca5a831488e3ab645f3cad9079b93',
}
OFFICIAL = {
    'cmd/gomobile/bind.go': '35904df7228c0d66b95914e7284a6a7b5f1d49934f60fbb4d846fbfc0b5785d3',
    'cmd/gomobile/bind_androidapp.go': '23705f1be8f53f8c95e2ede234ef785cdb3a4780f939d73766b7af862f0c6ad5',
    'cmd/gomobile/build.go': 'bea85d77e4ac091b2c8093ee2b963db62c4a5babf130a646fdb7eb0029d17514',
    'cmd/gomobile/env.go': '0a8be58a499bc36f8fe0dd8da7a44f5c4388d000f12b041d95158d29a359285b',
}
MOBILE_COMMIT = '19777044486779222400e9ca16c4306f9c6dbfa9'
FIXTURE = 'luobo_module_projection_test.go'
FIXTURE_SHA = '8656e3104a902b78b48ff818c1c90b798a915ea6734a57e089347e9615e6422c'
CORE_SUM = '8eb9b73313a7f774bfef48814bfc1b73e7ff7f57bd37f9d40555e8d24f70262f'


def admit_phase(budget):
    if budget.sample() < s.INITIAL:
        raise ValueError('phase4GiB resource gate; no next command dispatched')


def metadata():
    here = Path(__file__).resolve().parent
    for name, digest in {**HELPERS, FIXTURE: FIXTURE_SHA}.items():
        p = here / name
        if p.is_symlink() or not p.is_file() or s.sha(p) != digest:
            raise ValueError('exact reviewed source/helper required: ' + name)
    registry, inputs, tools = parent.metadata()
    if tools['mobile']['commit'] != MOBILE_COMMIT:
        raise ValueError('fixed official x/mobile commit required')
    return registry, inputs, tools


def snapshot(work, directory, label):
    """Keep raw locks even when a command fails; missing is not an empty file."""
    destination = work / 'evidence/locks' / s.name(label)
    rows = {}
    for name in ['go.mod', 'go.sum']:
        p = directory / name
        try:
            info = p.lstat()
        except FileNotFoundError:
            rows[name] = {'exists': False}
            continue
        if (not stat.S_ISREG(info.st_mode) or info.st_nlink != 1
                or info.st_uid != os.getuid() or info.st_size > 1048576):
            raise ValueError('bounded owned single-link module lock required')
        body = p.read_bytes()
        s.put(destination / name, body)
        rows[name] = {'exists': True, 'bytes': len(body), 'sha256': hashlib.sha256(body).hexdigest()}
    s.write_json(destination / 'snapshot.json', {'label': label, 'files': rows})
    return rows


def official_sources(job, mobile, label):
    rows = []
    for path, digest in OFFICIAL.items():
        file = mobile / path
        if file.is_symlink() or not file.is_file() or s.sha(file) != digest:
            raise ValueError('official x/mobile source differs: ' + path)
        s.put(job.w / 'evidence/official' / label / path, file.read_bytes())
        rows.append({'path': path, 'bytes': file.stat().st_size, 'sha256': digest})
    s.write_json(job.w / 'evidence/official' / label / 'sources.json', rows)


def source_status(job, mobile, env, label, added=False):
    changed = job.cmd(['git', 'diff', '--name-only', '-z'], mobile, env, label + '-tracked').read_bytes().split(b'\0')
    untracked = job.cmd(['git', 'ls-files', '--others', '--exclude-standard', '-z'], mobile, env, label + '-untracked').read_bytes().split(b'\0')
    if set(filter(None, changed)) - {b'go.sum'}:
        raise ValueError('official source changed outside separately recorded mobile go.sum')
    expected = {('cmd/gomobile/' + FIXTURE).encode()} if added else set()
    if set(filter(None, untracked)) != expected:
        raise ValueError('only the single reviewed projection test may be added')


def execute(work, inputs, tools, budget):
    for name in ['evidence', 'downloads', 'tmp', 'home', 'config', 'go-cache', 'go-mod',
                 'gopath', 'mobile-tools', 'job-private', 'generated-module']:
        s.mkdir(work / name)
    env = parent.environment(work, False)
    env.update({'GOWORK': 'off', 'GOOS': 'linux', 'GOARCH': 'amd64', 'CGO_ENABLED': '1'})
    job = parent.Job(work, inputs, tools, budget)
    job.source(env)
    admit_phase(budget)
    job.tool('go', tools['go'], work / 'go', env)
    go = work / 'go/bin/go'
    log = job.cmd([go, 'version'], work, env, 'go-version')
    if 'go1.27.2 linux/amd64' not in log.read_text():
        raise ValueError('exact host Go1.27.2 required')
    job.cmd([go, 'env', '-json', 'GOOS', 'GOARCH', 'CGO_ENABLED', 'GOFLAGS', 'GOWORK', 'GOVERSION', 'GOMODCACHE'],
            work, env, 'host-go-environment')
    job.git(work / 'core-upstream', parent.CORE_REPO, parent.CORE_REF, env, 'core-upstream')
    # Preserve the real source overlay, including a fresh public job-only CA.
    log = job.cmd(['openssl', 'version'], work, env, 'openssl-version')
    if not log.read_text().startswith('OpenSSL 3.'):
        raise ValueError('OpenSSL3 required')
    key, ca = work / 'job-private/job-ca.key', work / 'job-private/job-ca.pem'
    job.cmd(['openssl', 'req', '-x509', '-newkey', 'rsa:3072', '-sha256', '-noenc', '-days', '2',
             '-keyout', key, '-out', ca, '-subj', '/CN=LuoboIM-generated-module-evidence-job',
             '-addext', 'basicConstraints=critical,CA:TRUE', '-addext', 'keyUsage=critical,keyCertSign,cRLSign'],
            work, env, 'job-CA')
    key.chmod(0o600)
    key.unlink()
    s.sync(key.parent)
    s.write_json(work / 'evidence/job-CA.json', {'public_sha256': s.sha(ca), 'private_key_deleted': True,
        'source_overlay_reproduction_only': True, 'native_or_system_trust_change': False})
    core = work / 'core-prefetch'
    admit_phase(budget)
    job.cmd(job.builder(core, prepare=True), work, env, 'core-prepare')
    original = snapshot(work, core, 'core-source-original')
    if original['go.sum']['sha256'] != tools['locks']['patched_core_go_sum']['sha256']:
        raise ValueError('prepared source must first match original810')
    # Step1 in the real source-lock evidence left the source lock unchanged;
    # step4 downloaded all dependencies and produced reviewed8eb9. No graph is
    # inserted before that acquisition; no Android tools are installed here.
    commands = [
        ('core-mobile-module', [go, 'mod', 'download', '-json', 'golang.org/x/mobile@' + tools['mobile']['version']]),
        ('core-acquire', [go, 'mod', 'download', 'all']),
        ('core-verify', [go, 'mod', 'verify']),
        ('core-graph', [go, 'list', '-m', '-json', 'all']),
    ]
    for label, command in commands:
        admit_phase(budget)
        snapshot(work, core, label + '-before')
        try:
            output = job.cmd(command, core, env, label, 1200)
        finally:
            after = snapshot(work, core, label + '-after')
        if after['go.mod'] != original['go.mod']:
            raise ValueError('core GoMod changed during acquisition')
        if label == 'core-mobile-module':
            module = json.loads(output.read_text())
            if module.get('Sum') != tools['mobile']['sum'] or module.get('GoModSum') != tools['mobile']['go_mod_sum']:
                raise ValueError('official fixed x/mobile module checksum mismatch')
            if after != original:
                raise ValueError('mobile download changed the original source locks')
        elif after['go.sum'].get('sha256') != CORE_SUM or after['go.sum'].get('bytes') != 18776:
            raise ValueError('acquired core lock must remain exact8eb9 at each subsequent core stage')
    baseline = snapshot(work, core, 'core-ready-acquired')
    if baseline['go.sum'].get('sha256') != CORE_SUM or baseline['go.sum'].get('bytes') != 18776:
        raise ValueError('core acquired lock differs from independently reviewed8eb9')

    def step(label, command, cwd, process_env, diagnostic=False):
        admit_phase(budget)
        if snapshot(work, core, label + '-core-before') != baseline:
            raise ValueError('core acquired lock changed before ' + label)
        try:
            return job.cmd(command, cwd, process_env, label, 1200)
        except ValueError:
            # A pre-acquisition offline metadata graph may legitimately fail.
            # Keep its raw nonzero status, never count it as a passed test.
            record = work / 'evidence' / (label + '.resource.json')
            if not diagnostic or budget.reason or not record.is_file():
                raise
            result = json.loads(record.read_text())
            if result['exit_code'] in (None, 0) or result['expected_rejection'] is not None:
                raise
            return None
        finally:
            if snapshot(work, core, label + '-core-after') != baseline:
                raise ValueError('core acquired lock changed after ' + label)

    mobile = work / 'mobile-projector'
    try:
        admit_phase(budget)
        job.git(mobile, 'https://go.googlesource.com/mobile', MOBILE_COMMIT, env, 'official-mobile')
        step('official-mobile-checkout', ['git', 'checkout', '--quiet', '--detach', MOBILE_COMMIT], mobile, env)
        official_sources(job, mobile, 'before')
        source_status(job, mobile, env, 'mobile-pristine')
        mobile_original = snapshot(work, mobile, 'mobile-source-original')
        try:
            step('mobile-host-acquire', [go, 'mod', 'download', 'all'], mobile, env)
        finally:
            mobile_ready = snapshot(work, mobile, 'mobile-host-acquired')
        if mobile_ready['go.mod'] != mobile_original['go.mod']:
            raise ValueError('mobile host acquisition changed official go.mod')
        fixture = (Path(__file__).resolve().parent / FIXTURE).read_bytes()
        s.put(mobile / 'cmd/gomobile' / FIXTURE, fixture)
        s.put(work / 'evidence/mobile-projector-added-test.go', fixture)
        patch = ''.join(difflib.unified_diff([], fixture.decode().splitlines(True), fromfile='/dev/null',
            tofile='b/cmd/gomobile/' + FIXTURE))
        s.put(work / 'evidence/mobile-projector-only-source-addition.patch', patch.encode())
        target = {**env, 'GOOS': 'android', 'GOARCH': 'arm64', 'GOFLAGS': '-p=1 -mod=readonly'}
        acquire = {**target, 'GOFLAGS': '-p=1'}
        offline = {**target, 'GOPROXY': 'off', 'GOSUMDB': 'off'}
        step('target-environment', [go, 'env', '-json', 'GOOS', 'GOARCH', 'CGO_ENABLED', 'GOFLAGS', 'GOWORK', 'GOVERSION'], core, target)
        graph_args = [go, 'list', '-m', '-json', '-tags=luobo_fixture_ca_debug,android', 'all']
        step('projection-source-graph-before', graph_args, core, offline)
        test_env = {**env, 'GOFLAGS': '-p=1 -mod=readonly', 'GOPROXY': 'off', 'GOSUMDB': 'off',
                    'LUOBO_PROJECTION_CORE': str(core), 'LUOBO_PROJECTION_OUT': str(work / 'generated-module')}
        step('real-official-projection', [go, 'test', '-v', '-run', '^TestLuoboProjectAndroidModule$',
             '-count=1', '-p=1', './cmd/gomobile'], mobile, test_env)
        if snapshot(work, mobile, 'mobile-after-projection') != mobile_ready:
            raise ValueError('host projection test changed mobile module locks')
        step('projection-source-graph-after', graph_args, core, offline)
        generated = work / 'generated-module'
        initial = snapshot(work, generated, 'projected-original')
        s.put(work / 'evidence/projection.json', (generated / 'projection.json').read_bytes())
        # No generated Go/Java files exist. This is metadata closure only, and
        # must never be installed as the lock for a real generated-source tidy.
        for label, command, process_env, diagnostic in [
            ('projected-graph-before-offline', [go, 'list', '-m', '-json', 'all'], offline, True),
            ('projected-acquire', [go, 'mod', 'download', 'all'], acquire, False),
            ('projected-verify', [go, 'mod', 'verify'], offline, False),
            ('projected-graph-after-offline', [go, 'list', '-m', '-json', 'all'], offline, False),
        ]:
            before = snapshot(work, generated, label + '-before')
            try:
                step(label, command, generated, process_env, diagnostic)
            finally:
                after = snapshot(work, generated, label + '-after')
            # Go's readonly mode protects go.mod, not every go.sum append.
            # A pre-acquisition graph may record cached sums even on failure;
            # retain both raw versions and continue to the real acquisition.
            if after['go.mod'] != initial['go.mod'] or (label not in {
                    'projected-acquire', 'projected-graph-before-offline'} and after != before):
                raise ValueError('metadata observation changed unexpected projected lock bytes')
        official_sources(job, mobile, 'after')
        source_status(job, mobile, env, 'mobile-final', added=True)
        s.write_json(work / 'evidence/projector-source-difference.json', {
            'commit': MOBILE_COMMIT, 'official_four_sources_unchanged': True,
            'only_added_Go_source': 'cmd/gomobile/' + FIXTURE, 'test_sha256': FIXTURE_SHA,
            'separate_host_dependency_locks': {'before': mobile_original, 'acquired': mobile_ready},
            'binding_Go_or_Java_generated': False})
        job.verify()
        return {'state': 'projection_and_metadata_acquisition_collected_unreviewed',
                'cohort': COHORT, 'projected_locks': snapshot(work, generated, 'projected-final'),
                'source_core_sum_sha256': CORE_SUM, 'official_mobile_commit': MOBILE_COMMIT,
                'generated_Go_or_Java_files': 0, 'empty_directory_tidy': False,
                'full_bind_lock_or_native_authorization': False, 'Android_tools_or_native_builds': 0}
    finally:
        if snapshot(work, core, 'core-final-even-on-failure') != baseline:
            raise ValueError('final core acquired lock differs; raw evidence retained')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cohort', required=True, choices=[COHORT])
    parser.add_argument('--execute', action='store_true')
    parser.add_argument('--work')
    args = parser.parse_args()
    registry, inputs, tools = metadata()
    if not args.execute:
        print(json.dumps({'state': 'source_metadata_only_no_projection_execution', 'cohort': COHORT,
                          'source_ref': parent.SOURCE_REF, 'full_bind_lock': False}))
        return
    if sys.platform != 'linux' or os.geteuid() == 0 or not args.work:
        parser.error('explicit fresh ordinary-user Linux output required')
    work = parent.work_path(args.work, True)
    s.mkdir(work)
    s.mkdir(work / 'evidence')
    s.write_json(work / 'evidence/activation.json', {'cohort': COHORT,
        'controller_commit': os.environ.get('GITHUB_SHA'), 'source_ref': parent.SOURCE_REF,
        'inputs_sha256': registry['inputs_sha256'], 'native_pipeline_unchanged': True,
        'cold_gate_bytes': parent.COLD, 'no_partial_resume': True})
    budget, failure = s.Budget(work, parent.REPO), None
    try:
        budget.start()
        result = execute(work, inputs, tools, budget)
        s.write_json(work / 'evidence/probe-complete-candidate.json', {**result, **budget.record()})
        budget.finish()
        print(json.dumps({**result, **budget.record()}), flush=True)
    except BaseException as error:
        failure = type(error).__name__ + ': ' + str(error)
        budget.latch('probe_or_infrastructure_failed')
        raise
    finally:
        budget.stop.set()
        if budget.thread is not None:
            budget.thread.join()
        if failure or budget.reason:
            s.write_json(work / 'evidence/probe-failure.json', {'state': 'failed', 'error': failure, **budget.record()})


if __name__ == '__main__':
    main()
