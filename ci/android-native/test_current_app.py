"""Prepared only: source/env controls, no Go/Gradle/SDK/model execution."""
import copy
import json
import os
from pathlib import Path
import tempfile
import unittest
import current_app as current
import run as historical


class CurrentInputs(unittest.TestCase):
    def test_frozen_source530_accepts_only_354_identity_and_exact_rows(self):
        context=json.loads((current.HERE/'current-app-cohort.json').read_text())
        inputs=json.loads((current.HERE/'current-app-inputs.json').read_text())
        self.assertEqual(current.validate_inputs(inputs,context)['members'],530)
        for field,value in [('cohort','current257-auditable-arm64-observed-bind-v1'),('members',428),('source_digest','0'*64)]:
            with self.subTest(field=field):
                bad=copy.deepcopy(inputs);bad[field]=value
                with self.assertRaises(ValueError):current.validate_inputs(bad,context)

    def test_source_body_or_extra_row_cannot_borrow_current_identity(self):
        context=json.loads((current.HERE/'current-app-cohort.json').read_text())
        original=json.loads((current.HERE/'current-app-inputs.json').read_text())
        bad=copy.deepcopy(original);bad['files'][0]['sha256']='0'*64
        with self.assertRaises(ValueError):current.validate_inputs(bad,context)
        bad=copy.deepcopy(original);bad['files'].append(dict(path='extra',bytes=0,sha256='0'*64))
        with self.assertRaises(ValueError):current.validate_inputs(bad,context)

    def test_published_ref_portable406_and_native7_are_bound_to_actual_source_rows(self):
        context=json.loads((current.HERE/'current-app-cohort.json').read_text())
        inputs=json.loads((current.HERE/'current-app-inputs.json').read_text())
        for field,value in [('source_ref','c6c3471df19f955b0462166d589a6eae84985847'),
                ('portable_digest','0'*64),('locked_packages',174)]:
            with self.subTest(field=field):
                bad=copy.deepcopy(context);bad[field]=value
                with self.assertRaises(ValueError):current.validate_inputs(inputs,bad)
        bad=copy.deepcopy(context)
        bad['native_source_snapshot']['files'][0]['sha256']='0'*64
        bad['native_source_snapshot']['digest']=current.digest(bad['native_source_snapshot']['files'])
        with self.assertRaises(ValueError):current.validate_inputs(inputs,bad)

    def test_enforced178_lock_and_official_webview_receipts_reject_drift_and_alias(self):
        import hashlib
        context=json.loads((current.HERE/'current-app-cohort.json').read_text())
        with tempfile.TemporaryDirectory() as value:
            root=Path(value);lock=root/'source/app/pubspec.lock';lock.parent.mkdir(parents=True)
            body=('packages:\n'+''.join('  pkg'+str(i)+':\n' for i in range(178))).encode()
            lock.write_bytes(body)
            context['pub_lock_after']=dict(path='app/pubspec.lock',bytes=len(body),sha256=hashlib.sha256(body).hexdigest())
            for row in context['webview_archive_identities']:
                name=row['name']+'-'+row['version']
                package=root/'pub-cache/hosted/pub.dev'/name;package.mkdir(parents=True)
                receipt=root/'pub-cache/hosted-hashes/pub.dev'/(name+'.sha256')
                receipt.parent.mkdir(parents=True,exist_ok=True);receipt.write_bytes(row['archive_sha256'].encode())
            proof=current.check_current_packages(root,context)
            self.assertEqual(len(proof['entries']),4)
            self.assertEqual(proof['locked_packages'],178)
            self.assertFalse(proof['independent_archive_body_rehash'])
            self.assertFalse(proof['native_acceptance'])
            good=receipt.read_bytes();receipt.write_bytes(b'0'*64)
            with self.assertRaises(ValueError):current.check_current_packages(root,context)
            receipt.unlink();alias=receipt.with_name('alias');alias.write_bytes(good);receipt.symlink_to(alias)
            with self.assertRaises(ValueError):current.check_current_packages(root,context)
            receipt.unlink();receipt.write_bytes(good);lock.write_bytes(body+b'changed\n')
            with self.assertRaises(ValueError):current.check_current_packages(root,context)

    def test_actual_effective_env_keeps_HOME_and_all_owned_paths(self):
        work=Path('/owned/current331');actual_home=os.environ['HOME']
        before=historical.environment(work,False);saved=dict(before)
        after=current.effective_environment(before,work,actual_home)
        self.assertEqual(before,saved)
        self.assertEqual(after['HOME'],actual_home)
        for key,value in {'GOENV':'off','GOAUTH':'off','GIT_CONFIG_GLOBAL':'/dev/null',
                'GIT_CONFIG_NOSYSTEM':'1','GIT_TERMINAL_PROMPT':'0','GIT_NO_REPLACE_OBJECTS':'1'}.items():
            self.assertEqual(after[key],value)
        self.assertIn('-Duser.home=/owned/current331/home',after['JAVA_TOOL_OPTIONS'])
        self.assertIn('-Djava.io.tmpdir=/owned/current331/tmp',after['JAVA_TOOL_OPTIONS'])
        self.assertIn('-XX:-UsePerfData',after['JAVA_TOOL_OPTIONS'])
        self.assertEqual(after['ANDROID_USER_HOME'],'/owned/current331/home/.android')

    def test_effective_env_refuses_external_cache_proxy_and_wrong_HOME(self):
        work=Path('/owned/current331');before=historical.environment(work,False)
        for key,value in [('PUB_CACHE','/foreign/cache'),('ANDROID_USER_HOME','/foreign/.android'),
                ('HOME','/foreign/home'),('HTTPS_PROXY','synthetic'),('FLUTTER_SUPPRESS_ANALYTICS','false')]:
            with self.subTest(key=key):
                bad=dict(before);bad[key]=value
                with self.assertRaises(ValueError):current.effective_environment(bad,work,os.environ['HOME'])

    def test_HOME_legacy_configuration_rejected_without_read_or_delete(self):
        with tempfile.TemporaryDirectory() as value:
            home=Path(value);current.home_admission(home)
            file=home/'.flutter_settings';file.write_text('synthetic')
            with self.assertRaisesRegex(ValueError,'existing HOME'):current.home_admission(home)
            self.assertEqual(file.read_text(),'synthetic')

    def test_HOME_dangling_config_alias_is_rejected(self):
        with tempfile.TemporaryDirectory() as value:
            home=Path(value);file=home/'.flutter_tool_state';file.symlink_to(home/'absent')
            with self.assertRaises(ValueError):current.home_admission(home)
            self.assertTrue(file.is_symlink())

    def test_actual_Flutter_environment_source_checker_rejects_change(self):
        import hashlib
        with tempfile.TemporaryDirectory() as value:
            root=Path(value);(root/'flutter').mkdir();file=root/'flutter/config.dart'
            file.write_bytes(b'synthetic source')
            rows=[dict(path='config.dart',bytes=file.stat().st_size,sha256=hashlib.sha256(file.read_bytes()).hexdigest())]
            current.check_flutter_environment_sources(root,rows)
            file.write_bytes(b'changed source')
            with self.assertRaises(ValueError):current.check_flutter_environment_sources(root,rows)


if __name__=='__main__':unittest.main()
