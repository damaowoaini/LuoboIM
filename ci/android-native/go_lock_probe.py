#!/usr/bin/env python3
"""Only collect fresh Go lock evidence; never unlock native build or edit pins."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import sys

import run as parent
import support as s

COHORT = 'current257-go-lock-evidence-v2'
HELPERS = {
    'run.py': '5d1824cf537e04c2967f3776eb848be567fe296964f86056a82cefeee9ad6eb1',
    'support.py': '818ed812deab20b25d4e8352f7d31b637039c0a881c8fc0fa070bc2ce1ea8fbd',
    'audit.py': '1edf23b5ce32258e3e37d7af615b672e764de8193540aaa7de8a6fc03888d974',
    'verify_inputs.py': '3e76f58fdd504abfcb243e2b8f85570b3c476b58cefcf8a41558a4214f882ff8',
    'cohort.json': '7c3b4b9b84049cd4233c6c5db610fc66149cf15ccc64c74bc1b9f945e3c5ff08',
}


def metadata():
    for name, digest in HELPERS.items():
        path = Path(__file__).resolve().parent / name
        if path.is_symlink() or not path.is_file() or s.sha(path) != digest:
            raise ValueError('exact frozen r2 helper required: ' + name)
    return parent.metadata()


def snapshot(work, label):
    directory = work / 'evidence' / ('lock-' + s.name(label))
    s.mkdir(directory)
    rows = []
    for name in ['go.mod', 'go.sum']:
        path = work / 'core-prefetch' / name
        if path.is_symlink() or not path.is_file() or path.stat().st_nlink != 1 or path.stat().st_size > 1048576:
            raise ValueError('bounded ordinary core lock required')
        body = path.read_bytes()
        s.put(directory / name, body)
        rows.append({'path': name, 'bytes': len(body), 'sha256': hashlib.sha256(body).hexdigest()})
    s.write_json(directory / 'snapshot.json', {'label': label, 'files': rows})
    return rows


def execute(work, inputs, tools, budget):
    for name in ['evidence', 'downloads', 'tmp', 'home', 'config', 'go-cache', 'go-mod',
                 'gopath', 'mobile-tools', 'job-private']:
        s.mkdir(work / name)
    env = parent.environment(work, False)
    job = parent.Job(work, inputs, tools, budget)
    job.source(env)
    job.tool('go', tools['go'], work / 'go', env)
    go = work / 'go/bin/go'
    version = job.cmd([go, 'version'], work, env, 'go-version')
    if 'go1.27.2 ' not in version.read_text():
        raise ValueError('exact Go1.27.2 required')
    job.git(work / 'core-upstream', parent.CORE_REPO, parent.CORE_REF, env, 'core-upstream')
    # Reproduce the original TLS overlay as source, using a new public job-only
    # certificate. No Android/JDK/Flutter/Gradle tools or native compilation.
    log = job.cmd(['openssl', 'version'], work, env, 'openssl-version')
    if not log.read_text().startswith('OpenSSL 3.'):
        raise ValueError('OpenSSL3 required')
    key, ca = work / 'job-private/job-ca.key', work / 'job-private/job-ca.pem'
    job.cmd(['openssl', 'req', '-x509', '-newkey', 'rsa:3072', '-sha256', '-noenc', '-days', '2',
             '-keyout', key, '-out', ca, '-subj', '/CN=LuoboIM-Go-lock-evidence-job',
             '-addext', 'basicConstraints=critical,CA:TRUE', '-addext', 'keyUsage=critical,keyCertSign,cRLSign'],
            work, env, 'job-CA')
    key.chmod(0o600); key.unlink(); s.sync(key.parent)
    s.write_json(work / 'evidence/job-CA.json', {'public_sha256': s.sha(ca), 'private_key_deleted': True,
        'only_for_reproducing_exact_source_overlay': True, 'native_or_system_trust_change': False})
    job.cmd(job.builder(work / 'core-prefetch', prepare=True), work, env, 'core-prepare')
    baseline = snapshot(work, '00-prepared')
    if baseline[1]['sha256'] != tools['locks']['patched_core_go_sum']['sha256']:
        raise ValueError('prepared go.sum already differs from frozen lock; evidence retained')
    operations = [
        ('01-mobile-module', [go, 'mod', 'download', '-json', 'golang.org/x/mobile@' + tools['mobile']['version']]),
        ('02-install-gomobile', [go, 'install', '-trimpath', 'golang.org/x/mobile/cmd/gomobile@' + tools['mobile']['version']]),
        ('03-install-gobind', [go, 'install', '-trimpath', 'golang.org/x/mobile/cmd/gobind@' + tools['mobile']['version']]),
        ('04-go-dependencies', [go, 'mod', 'download', 'all']),
        ('05-go-verify', [go, 'mod', 'verify']),
        ('06-original-module-graph', [go, 'list', '-m', '-json', 'all']),
    ]
    for label, command in operations:
        snapshot(work, label + '-before')
        try:
            output = job.cmd(command, work / 'core-prefetch', env, label, 1200)
        finally:
            after = snapshot(work, label + '-after')
        if label == '01-mobile-module':
            module = json.loads(output.read_text())
            if module.get('Sum') != tools['mobile']['sum'] or module.get('GoModSum') != tools['mobile']['go_mod_sum']:
                raise ValueError('exact mobile checksum changed')
        # Do not insert any graph command here: observing a cold graph can
        # download metadata or fail on missing sums before the original step4.
    final = snapshot(work, '07-final')
    before_sum = (work / 'evidence/lock-00-prepared/go.sum').read_bytes()
    after_sum = (work / 'core-prefetch/go.sum').read_bytes()
    s.write_json(work / 'evidence/go-lock-difference.json', {'before': baseline, 'after': final,
        'added_lines': sorted(set(after_sum.decode().splitlines()) - set(before_sum.decode().splitlines())),
        'removed_lines': sorted(set(before_sum.decode().splitlines()) - set(after_sum.decode().splitlines())),
        'expected_original_go_sum_sha256': tools['locks']['patched_core_go_sum']['sha256'],
        'original_gate_not_relaxed': True, 'no_native_build': True})
    # The original six-command sequence, including its full graph at step6,
    # has completed and its raw final/diff has been fsynced before this optional
    # extra observation. Any failure here cannot erase the original evidence.
    try:
        job.cmd([go, 'list', '-m', '-json', '-mod=readonly', 'all'],
                work / 'core-prefetch', env, '08-tail-readonly-graph', 1200)
    finally:
        graph_after = snapshot(work, '08-tail-readonly-graph-after')
    if graph_after != final:
        raise ValueError('tail readonly graph changed module locks; final raw/diff retained')
    job.verify()
    if final != baseline or final[1]['sha256'] != tools['locks']['patched_core_go_sum']['sha256']:
        raise ValueError('reviewed core go.sum/go.mod changed during acquisition; raw evidence retained; native remains blocked')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cohort', required=True, choices=[COHORT])
    parser.add_argument('--execute', action='store_true')
    parser.add_argument('--work')
    args = parser.parse_args()
    registry, inputs, tools = metadata()
    if not args.execute:
        print(json.dumps({'state': 'readonly_Go_lock_diagnostic_metadata_only', 'source_ref': parent.SOURCE_REF,
                          'cohort': COHORT, 'original_native_controller_unchanged': True}))
        return
    if sys.platform != 'linux' or os.geteuid() == 0 or not args.work:
        parser.error('explicit new ordinary-user Linux work required')
    # Reuse the stricter cold20GiB / dual1GiB / .2s /16GiB guards; the Go-only
    # diagnostic intentionally does not relax any existing resource threshold.
    work = parent.work_path(args.work, True)
    s.mkdir(work); s.mkdir(work / 'evidence')
    s.write_json(work / 'evidence/activation.json', {'cohort': COHORT, 'source_ref': parent.SOURCE_REF,
        'controller_commit': os.environ.get('GITHUB_SHA'), 'inputs_sha256': registry['inputs_sha256'],
        'native_builds': 0, 'old_failed_work_resume': False})
    budget = s.Budget(work, parent.REPO)
    failed = None
    try:
        budget.start()
        execute(work, inputs, tools, budget)
        s.write_json(work / 'evidence/probe-complete.json', {'state': 'passed_no_lock_change', **budget.record()})
        budget.finish()
        print(json.dumps({'state': 'passed_no_lock_change', 'native_builds': 0, **budget.record()}), flush=True)
    except BaseException as error:
        failed = type(error).__name__ + ': ' + str(error)
        budget.latch('probe_or_infrastructure_failed')
        raise
    finally:
        budget.stop.set()
        if budget.thread is not None:
            budget.thread.join()
        if failed or budget.reason:
            s.write_json(work / 'evidence/probe-failure.json', {'state': 'failed', 'error': failed, **budget.record()})


if __name__ == '__main__':
    main()
