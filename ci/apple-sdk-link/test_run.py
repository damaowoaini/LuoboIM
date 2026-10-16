"""Six new pure controls only. No actual download, Go, native tool or bind."""
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import struct
import sys
import tempfile
import unittest
from unittest.mock import patch
import zipfile

import custody
import run


def digest(body):
    return hashlib.sha256(body).hexdigest()


def image(platform=2):
    command = struct.pack('<6I', 0x32, 24, platform, 15 << 16, 18 << 16, 0)
    return struct.pack('<8I', 0xFEEDFACF, 0x100000C, 0, 1, 1, len(command), 0, 0) + command


def archive(body):
    header = f'{"bridge.o/":<16}{0:<12}{0:<6}{0:<6}{100644:<8}{len(body):<10}`\n'.encode()
    return b'!<arch>\n' + header + body + (b'\n' if len(body) % 2 else b'')


def module(path, name):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


class LinkOnlyControls(unittest.TestCase):
    def test_exact_original_run_artifact_and_anonymous_blob_boundary(self):
        spec = {'parent': {'run': 1, 'job': 2, 'head': 'fixed', 'artifacts': [
            {'id': 3, 'bytes': 4, 'name': 'original', 'sha256': 'a'*64}]}}
        values = {
            '/actions/runs/1': {'id': 1, 'head_sha': 'fixed', 'run_attempt': 1,
                                'status': 'completed', 'conclusion': 'failure'},
            '/actions/runs/1/jobs': {'jobs': [{'id': 2, 'conclusion': 'failure'}]},
            '/actions/artifacts/3': {'expired': False, 'size_in_bytes': 4, 'name': 'original',
                'digest': 'sha256:'+'a'*64, 'workflow_run': {'id': 1, 'head_sha': 'fixed'}}}
        with patch.object(custody, 'api_json', side_effect=lambda p, token: values[p]):
            self.assertEqual(custody.check_remote(spec, 'synthetic-token')['job'], 2)
            values['/actions/artifacts/3']['digest'] = 'sha256:'+'b'*64
            with self.assertRaisesRegex(ValueError, 'artifact metadata'):
                custody.check_remote(spec, 'synthetic-token')
        self.assertEqual(custody.blob_url('https://owned.blob.core.windows.net/a?synthetic=1'),
                         'https://owned.blob.core.windows.net/a?synthetic=1')
        for url in ['http://owned.blob.core.windows.net/a', 'https://blob.core.windows.net.evil/a',
                    'https://user@owned.blob.core.windows.net/a', 'https://owned.blob.core.windows.net:444/a']:
            with self.subTest(url=url), self.assertRaises(ValueError):
                custody.blob_url(url)
        self.assertIsNone(custody.NoRedirect().redirect_request(None, None, None, None, None, None))
        # Real download() request construction, mocked transport only: bearer may
        # reach API but must never be copied to the anonymous blob request.
        import urllib.error
        from types import SimpleNamespace
        data = b'original'
        class Reply(io.BytesIO):
            status = 200
            headers = {'Content-Length': str(len(data))}
        requests = []
        def api_open(request, timeout):
            requests.append(request)
            raise urllib.error.HTTPError(request.full_url, 302, 'redirect',
                {'Location': 'https://owned.blob.core.windows.net/bytes?synthetic=1'}, None)
        def anonymous_open(request, timeout):
            requests.append(request)
            return Reply(data)
        with tempfile.TemporaryDirectory() as text, patch.object(custody, 'opener', side_effect=[
                SimpleNamespace(open=api_open), SimpleNamespace(open=anonymous_open)]):
            output = Path(text).resolve()/'download.zip'
            custody.download({'id': 3, 'bytes': len(data), 'sha256': digest(data)}, 'synthetic-token', output)
            self.assertEqual(output.read_bytes(), data)
        self.assertEqual(requests[0].get_header('Authorization'), 'Bearer synthetic-token')
        self.assertIsNone(requests[1].get_header('Authorization'))

    def test_zip_requires_whole_sha_crc_safe_and_exact_member_set(self):
        with tempfile.TemporaryDirectory() as text:
            root = Path(text).resolve()
            def zipped(entries):
                out = io.BytesIO()
                with zipfile.ZipFile(out, 'w') as z:
                    for name, body in entries:
                        z.writestr(name, body)
                return out.getvalue()
            def attempt(label, body, allowed):
                p = root/(label+'.zip'); p.write_bytes(body)
                spec = {'bytes': len(body), 'sha256': digest(body), 'members_cap': 10,
                        'decoded_cap': 4096, 'member_cap': 1024}
                return custody.extract(p, spec, root/label, allowed)
            rows = attempt('good', zipped([('framework/Info.plist', b'original')]), {'framework/Info.plist'})
            self.assertEqual(rows[0]['sha256'], digest(b'original'))
            damaged = bytearray(zipped([('framework/Info.plist', b'original')]))
            position = damaged.find(b'original'); self.assertGreaterEqual(position, 0)
            damaged[position] ^= 1
            with self.assertRaisesRegex(ValueError, 'CRC'):
                attempt('badcrc', bytes(damaged), {'framework/Info.plist'})
            for label, entries in [('traversal', [('../escape', b'x')]),
                                    ('extra', [('framework/Info.plist', b'original'), ('extra', b'x')]),
                                    ('missing', [('other', b'x')])]:
                with self.subTest(label=label), self.assertRaises(ValueError):
                    attempt(label, zipped(entries), {'framework/Info.plist'})
            body = zipped([('one', b'x')]); p = root/'wrong.zip'; p.write_bytes(body)
            with self.assertRaisesRegex(ValueError, 'whole SHA'):
                custody.extract(p, {'bytes': len(body), 'sha256': '0'*64}, root/'wrong')

    def test_architecture_first_thin_audit_preserves_original_framework(self):
        helper = run.REPO/'ci/apple-sdk'
        sys.path.insert(0, str(helper))
        try:
            link = module(helper/'link_probe.py', 'test_actual_link_probe')
            with tempfile.TemporaryDirectory() as text:
                root = Path(text).resolve(); (root/'evidence').mkdir(); framework = root/'OpenIMCore.framework'; framework.mkdir()
                out = root/'link'; out.mkdir()
                inner = archive(image())
                fat = b'\xca\xfe\xba\xbe' + struct.pack('>I5I', 1, 0x100000C, 0, 28, len(inner), 0) + inner
                original = framework/'OpenIMCore'; original.write_bytes(fat)
                calls = []
                def fake_command(r, label, argv, cwd, env):
                    calls.append((label, list(map(str, argv))))
                    if label == 'core-arch-device':
                        body = b'arm64\n'
                    else:
                        self.assertEqual(Path(argv[-1]), out/'core-arm64.a')
                        self.assertEqual((out/'core-arm64.a').read_bytes(), inner)
                        body = {'ar': b'bridge.o\n', 'file': b'current ar archive\n', 'load': b'raw load evidence\n',
                                'symbols': b'0 T _Open_im_sdkInitSDK\n1 T _Open_im_sdkGetAtAllTag\n'}[label.split('-')[1]]
                    (root/'evidence'/(label+'.log')).write_bytes(body)
                with patch.object(link, 'command', side_effect=fake_command):
                    link.audit_framework(dict(lipo='lipo', ar='ar', file='file', otool='otool', nm='nm'),
                                         root, {}, 'device', framework, out)
                self.assertEqual(calls[0][0], 'core-arch-device')
                self.assertEqual(original.read_bytes(), fat)
                proof = json.loads((out/'core-audit.json').read_bytes())
                self.assertEqual(proof['versioned_objects'], 1)
                self.assertEqual(proof['core_sha256'], digest(fat))
                self.assertTrue(proof['original_framework_retained_for_link'])
                # A false lipo string cannot make a second fat slice acceptable.
                original.write_bytes(fat[:4]+struct.pack('>I', 2)+fat[8:])
                with patch.object(link, 'command', side_effect=fake_command), self.assertRaises(ValueError):
                    link.audit_framework(dict(lipo='lipo'), root, {}, 'device', framework, root/'refused')
        finally:
            sys.path.pop(0)

    def test_consumed_source_change_refused_and_native_environment_has_no_credentials(self):
        with tempfile.TemporaryDirectory() as text:
            root = Path(text).resolve(); here = root/'ci/apple-sdk-link'; here.mkdir(parents=True)
            p = root/'bridge.swift'; p.write_bytes(b'original')
            spec = {'identity': run.IDENTITY, 'source_files': [], 'bridge_files': [
                {'path': 'bridge.swift', 'bytes': 8, 'sha256': digest(b'original')}]}
            (here/'inputs.json').write_text(json.dumps(spec))
            with patch.object(run, 'HERE', here), patch.object(run, 'REPO', root):
                self.assertEqual(run.metadata()['identity'], run.IDENTITY)
                p.write_bytes(b'modified')
                with self.assertRaisesRegex(ValueError, 'source pin'):
                    run.metadata()
        native = run.native_environment(Path('/owned/output'), '/fixed/Xcode')
        self.assertEqual(native['HOME'], os.environ['HOME'])
        self.assertNotIn('GITHUB_TOKEN', native)
        self.assertNotIn('GOAUTH', native)
        self.assertNotIn('GOPATH', native)
        policy = run.policy(Path('/owned'), 'link').decode()
        self.assertIn('(deny network*)', policy)
        self.assertIn('(deny file-write* (subpath "/owned/products"))', policy)
        self.assertIn('(deny file-write* (subpath "/owned/.qa/apple-link-only-1/bridge"))', policy)

    def test_real_bind_and_both_complete_generated_snapshots_required(self):
        with tempfile.TemporaryDirectory() as text:
            root = Path(text).resolve(); evidence = root/'.qa/apple-cloud-1/evidence'; evidence.mkdir(parents=True)
            (evidence/'apple-bind.result.json').write_text('{"exit_code":0}')
            locks = {'go.mod': 'mod', 'go.sum': 'sum'}
            (evidence/'core-locks-after.json').write_text(json.dumps({'before': locks, 'after': locks}))
            for platform in ['ios', 'iossimulator']:
                folder = evidence/'generated'/platform; folder.mkdir(parents=True)
                proof = {'actual_catalogue_rows': 632, 'state': 'passed_finite_actual_tidy_provenance'}
                (folder/'module-provenance.json').write_text(json.dumps(proof))
                (folder/'result.json').write_text(json.dumps({'exit_code': 0, 'source_unchanged': True, 'module_provenance': proof}))
                (folder/'selected-graph.result.json').write_text('{"exit_code":0}')
                for phase in ['before', 'after']:
                    snap = folder/phase; snap.mkdir(); (snap/'generated.go').write_bytes(b'fixed')
                    (snap/'manifest.json').write_text(json.dumps({'complete_source_snapshot': True, 'error': None,
                        'files': [{'path': 'generated.go', 'bytes': 5, 'sha256': digest(b'fixed')}]}))
            spec = {'raw_markers': [], 'core_sum': 'sum'}
            self.assertEqual(custody.audit_raw(root, spec)['generated_snapshot_rows'], 4)
            (evidence/'generated/iossimulator/after/generated.go').write_bytes(b'stale')
            with self.assertRaisesRegex(ValueError, 'generated source'):
                custody.audit_raw(root, spec)
            (evidence/'apple-bind.result.json').write_text('{"exit_code":1}')
            with self.assertRaisesRegex(ValueError, 'bind0'):
                custody.audit_raw(root, spec)

    def test_failed_final_guard_is_returned_without_false_link_acceptance(self):
        from types import SimpleNamespace
        from unittest.mock import Mock
        with tempfile.TemporaryDirectory() as text:
            w = Path(text).resolve(); here = w/'controller'; here.mkdir()
            (here/'inputs.json').write_bytes(b'fixed descriptor')
            (w/'activation.json').write_text(json.dumps({'descriptor_sha256': custody.sha(here/'inputs.json')}))
            guarded = Mock(return_value=75)
            with patch.object(run, 'HERE', here), patch.object(run, 'metadata', return_value={}), \
                 patch.object(run, 'clean_process_environment'), patch.object(run, 'work_path', return_value=w), \
                 patch.object(run, 'space', return_value=8*run.GIB), \
                 patch.object(run.platform, 'system', return_value='Darwin'), \
                 patch.object(run.platform, 'machine', return_value='arm64'), \
                 patch.object(run.sys, 'version_info', (3, 12, 10)), \
                 patch.object(run.signal, 'signal'), \
                 patch.object(run, 'load', return_value=SimpleNamespace(run=guarded)):
                result = run.main(['--identity', run.IDENTITY, '--stage', 'link', '--work', str(w)])
            self.assertEqual(result, 75)
            options = guarded.call_args.args[0]
            self.assertEqual(options.min_free_mib, 1024)
            self.assertEqual(options.poll_interval, .2)
            self.assertEqual(options.max_allocated_mib, 6144)
            self.assertEqual(options.secondary_cap_mib, 3072)
            self.assertEqual(options.timeout, 1800)
            self.assertEqual(options.command[:2], ['/usr/bin/sandbox-exec', '-f'])
            self.assertIn('--worker', options.command)
            self.assertFalse((w/'.qa/apple-link-only-1/link-only-passed.json').exists())


if __name__ == '__main__':
    unittest.main()
