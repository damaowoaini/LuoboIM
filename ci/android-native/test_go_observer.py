"""Synthetic Python-child semantics only; no real Go, native tools or network."""
import contextlib
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import stat
import subprocess
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import go_observer as observer
import observed_bind as controller

STUB = '''#!/usr/bin/python3 -B
import json, os, signal, sys, time
from pathlib import Path
job=Path(os.environ['SYNTHETIC_JOB'])
(job/'started').write_text(str(os.getpid()))
mode=os.environ.get('SYNTHETIC_MODE','forward')
if mode=='signal':
 signal.signal(signal.SIGTERM,lambda *_:sys.exit(0))
 (job/'ready').write_text(str(os.getpid()))
 end=time.monotonic()+3
 while time.monotonic()<end:time.sleep(.01)
 sys.exit(9)
if mode=='child-signal':os.kill(os.getpid(),signal.SIGTERM)
if sys.argv[1:]==['mod','tidy']:
 Path('go.sum').write_text('example.test/synthetic v1.0.0 h1:synthetic\\n')
 if mode=='mutate':Path('gobind/gobind.go').write_text('changed by synthetic child')
 if mode=='after-symlink':
  p=Path.cwd().parent/'java/Owner.java';p.unlink();p.symlink_to(job/'started')
 print('synthetic tidy stdout');print('synthetic tidy stderr',file=sys.stderr)
else:
 print(json.dumps({'argv':sys.argv[1:],'PATH':os.environ['PATH'],
  'value':os.environ['SYNTHETIC_VALUE'],'stdin':sys.stdin.read(),'pgid':os.getpgrp()}))
 print('synthetic stderr',file=sys.stderr)
sys.exit(int(os.environ.get('SYNTHETIC_EXIT','0')))
'''


@contextlib.contextmanager
def fixture():
    with tempfile.TemporaryDirectory() as value:
        job = Path(value).resolve()
        for name in ['tmp', 'observer', 'evidence', 'go/bin', 'core-build']:
            (job / name).mkdir(parents=True,exist_ok=True,mode=0o700)
        real = job / 'go/bin/go'
        real.write_text(STUB)
        real.chmod(0o700)
        wrapper = job / 'observer/go'
        shutil.copyfile(Path(observer.__file__), wrapper)
        wrapper.chmod(0o700)
        base = str(real.parent) + ':/usr/bin:/bin'
        config = {'schema': 1, 'cohort': observer.COHORT, 'job_root': str(job), 'host_root': str(job),
            'real_go': str(real), 'real_go_sha256': hashlib.sha256(real.read_bytes()).hexdigest(),
            'observer_go': str(wrapper), 'observer_sha256': hashlib.sha256(wrapper.read_bytes()).hexdigest(),
            'base_path': base}
        body = (json.dumps(config) + '\n').encode()
        path = job / 'observer/config.json'
        path.write_bytes(body)
        path.chmod(0o600)
        env = {**os.environ, 'PATH': str(wrapper.parent) + ':' + base,
            'LUOBO_GO_OBSERVER_CONFIG': str(path), 'LUOBO_GO_OBSERVER_CONFIG_SHA256': hashlib.sha256(body).hexdigest(),
            'SYNTHETIC_JOB': str(job), 'SYNTHETIC_VALUE': 'owner-safe synthetic value $ untouched'}
        work = job / 'tmp/gomobile-work-fixture123'
        for name in ['src/gobind', 'java', 'src-android-arm64/gobind']:
            (work / name).mkdir(parents=True,exist_ok=True,mode=0o700)
        for name in ['src/gobind/gobind.go','src-android-arm64/gobind/gobind.go']:
            (work / name).write_text('package gobind\n')
        (work / 'java/Owner.java').write_text('class Owner {}\n')
        (work / 'src-android-arm64/go.mod').write_text('module gobind\n\ngo 1.27\n')
        yield job, config, env, work


class ObserverTests(unittest.TestCase):
    def run_wrapper(self, job, env, args, cwd, stdin=''):
        return subprocess.run([str(job/'observer/go'), *args], env=env,cwd=cwd,
            input=stdin,text=True,capture_output=True,timeout=5)

    def test_real_configure_observer_creates0600_under_umask022_and_loads(self):
        with tempfile.TemporaryDirectory() as value:
            job = Path(value).resolve()
            for name in ['tmp', 'evidence', 'go/bin']:
                (job / name).mkdir(parents=True, exist_ok=True, mode=0o700)
            real = job / 'go/bin/go'
            real.write_text(STUB)
            real.chmod(0o700)
            previous = os.umask(0o022)
            try:
                # Exercise the actual controller method and its real support
                # primitives; never invoke the CLI or the synthetic Go stub.
                with patch.object(controller.parent, 'REPO', job):
                    controller.Job.configure_observer(SimpleNamespace(w=job))
                config = job / 'observer/config.json'
                self.assertEqual(stat.S_IMODE(config.lstat().st_mode), 0o600)
                proof = json.loads((job / 'evidence/observer-configuration.json').read_text())
                self.assertEqual(proof['config_mode'], 0o600)
                self.assertEqual(observer.digest(config.read_bytes()), proof['config_sha256'])
                path = str(job / 'observer') + os.pathsep + proof['base_path']
                with patch.dict(os.environ, {'PATH': path}), contextlib.chdir(job):
                    loaded = observer.load_config(config, proof['config_sha256'], job / 'observer/go')
                self.assertEqual(loaded['real_go'], str(real))
                self.assertEqual(loaded['observer_go'], str(job / 'observer/go'))
                self.assertFalse((job / 'started').exists())
            finally:
                os.umask(previous)

    def test_argv_env_stdio_and_process_group_are_forwarded_without_recursion(self):
        with fixture() as (job, config, env, _):
            args=['list','a space','literal$argument']
            result=self.run_wrapper(job,env,args,job/'core-build','stdin retained')
            self.assertEqual(result.returncode,0,result.stderr)
            actual=json.loads(result.stdout)
            self.assertEqual(actual['argv'],args)
            self.assertEqual(actual['PATH'],config['base_path'])
            self.assertEqual(actual['value'],env['SYNTHETIC_VALUE'])
            self.assertEqual(actual['stdin'],'stdin retained')
            self.assertEqual(actual['pgid'],os.getpgrp())
            self.assertEqual(result.stderr,'synthetic stderr\n')

    def test_nonzero_child_exit_is_preserved(self):
        with fixture() as (job, _, env, _):
            result=self.run_wrapper(job,{**env,'SYNTHETIC_EXIT':'7'},['version'],job/'core-build')
            self.assertEqual(result.returncode,7)
            row=json.loads(next((job/'evidence/bind-observer/calls').glob('*-result.json')).read_text())
            self.assertEqual(row['child_exit_code'],7)

    def test_child_signal_exit_is_preserved(self):
        with fixture() as (job, _, env, _):
            result=self.run_wrapper(job,{**env,'SYNTHETIC_MODE':'child-signal'},['version'],job/'core-build')
            self.assertEqual(result.returncode,-signal.SIGTERM)
            row=json.loads(next((job/'evidence/bind-observer/calls').glob('*-result.json')).read_text())
            self.assertEqual(row['child_exit_code'],-signal.SIGTERM)

    def test_direct_signal_is_forwarded_and_child_zero_cannot_be_success(self):
        with fixture() as (job, _, env, _):
            child=subprocess.Popen([str(job/'observer/go'),'version'],env={**env,'SYNTHETIC_MODE':'signal'},
                cwd=job/'core-build',stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
            try:
                end=time.monotonic()+3
                while not (job/'ready').exists() and time.monotonic()<end:time.sleep(.01)
                self.assertTrue((job/'ready').exists(),'synthetic child never became ready')
                child.send_signal(signal.SIGTERM)
                child.communicate(timeout=4)
                self.assertEqual(child.returncode,-signal.SIGTERM)
                row=json.loads(next((job/'evidence/bind-observer/calls').glob('*-result.json')).read_text())
                self.assertEqual(row['child_exit_code'],0)
                self.assertEqual(row['received_signal'],signal.SIGTERM)
            finally:
                if child.poll() is None:child.kill();child.communicate(timeout=2)

    def test_exact_generated_tidy_keeps_raw_sources_and_before_after_locks(self):
        with fixture() as (job, _, env, work):
            result=self.run_wrapper(job,env,['mod','tidy'],work/'src-android-arm64')
            self.assertEqual(result.returncode,0,result.stderr)
            evidence=job/'evidence/bind-observer'
            before=json.loads((evidence/'before/manifest.json').read_text())
            after=json.loads((evidence/'after/manifest.json').read_text())
            self.assertTrue(before['complete']);self.assertTrue(after['complete'])
            self.assertFalse(before['go_sum_exists']);self.assertTrue(after['go_sum_exists'])
            self.assertEqual((evidence/'before/java/Owner.java').read_bytes(),b'class Owner {}\n')
            row=json.loads((evidence/'tidy-result.json').read_text())
            self.assertTrue(row['tidy_evidence_complete'])
            self.assertEqual(row['child_exit_code'],0)

    def test_failed_tidy_still_keeps_after_raw_and_original_failure(self):
        with fixture() as (job, _, env, work):
            result=self.run_wrapper(job,{**env,'SYNTHETIC_EXIT':'7'},['mod','tidy'],work/'src-android-arm64')
            self.assertEqual(result.returncode,7)
            evidence=job/'evidence/bind-observer'
            self.assertTrue((evidence/'after/src-android-arm64/go.sum').is_file())
            row=json.loads((evidence/'tidy-result.json').read_text())
            self.assertFalse(row['tidy_evidence_complete']);self.assertEqual(row['child_exit_code'],7)

    def test_foreign_cwd_and_nonexact_tidy_are_zero_dispatch(self):
        for args in [['mod','tidy'],['mod','tidy','-e']]:
            with self.subTest(args=args),fixture() as (job, _, env, _):
                result=self.run_wrapper(job,env,args,job/'core-build')
                self.assertEqual(result.returncode,75)
                self.assertFalse((job/'started').exists())

    def test_tidy_source_mutation_or_after_snapshot_error_cannot_report_success(self):
        for mode in ['mutate','after-symlink']:
            with self.subTest(mode=mode),fixture() as (job, _, env, work):
                result=self.run_wrapper(job,{**env,'SYNTHETIC_MODE':mode},['mod','tidy'],work/'src-android-arm64')
                self.assertEqual(result.returncode,75)
                row=json.loads((job/'evidence/bind-observer/tidy-result.json').read_text())
                self.assertEqual(row['child_exit_code'],0)
                self.assertFalse(row['tidy_evidence_complete']);self.assertIsNotNone(row['error'])

    def test_snapshot_cap_retains_partial_manifest_and_rejects(self):
        with fixture() as (job, config, _, work):
            with patch.object(observer,'TREE_LIMIT',1):
                with self.assertRaisesRegex(ValueError,'cap'):
                    observer.snapshot(config,work,job/'evidence/capped')
            row=json.loads((job/'evidence/capped/manifest.json').read_text())
            self.assertFalse(row['complete']);self.assertIsNotNone(row['error'])

    def test_config_tamper_or_real_go_symlink_is_rejected_without_execution(self):
        for mode in ['config-tamper','real-symlink']:
            with self.subTest(mode=mode),fixture() as (job, _, env, _):
                if mode=='config-tamper':
                    with (job/'observer/config.json').open('ab') as out:out.write(b' ')
                else:
                    real=job/'go/bin/go';real.rename(job/'go/bin/saved');real.symlink_to(job/'go/bin/saved')
                result=self.run_wrapper(job,env,['version'],job/'core-build')
                self.assertEqual(result.returncode,75)
                self.assertFalse((job/'started').exists())


if __name__=='__main__':
    unittest.main()
