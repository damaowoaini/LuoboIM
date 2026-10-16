"""Four targeted pure controls; fake Go dispatcher/real Python child, never Go or native."""
import contextlib
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import sys
import tempfile
import unittest
from unittest.mock import patch

import apple_observer as observer
import cloud
import generated_module as module

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]


def core_fixture(root):
    core, protocol = root/'core', root/'protocol'
    core.mkdir(); protocol.mkdir()
    (core/'go.mod').write_bytes(b'module github.com/openimsdk/openim-sdk-core/v3\ngo 1.27\n')
    locked = (REPO/'packages/flutter_openim_sdk/tool/native_core/acquired-go.sum').read_bytes()
    assert hashlib.sha256(locked).hexdigest() == module.CORE_SUM
    (core/'go.sum').write_bytes(locked)
    (protocol/'go.mod').write_bytes(b'module github.com/OpenIMSDK/protocol\ngo 1.27\n')
    return core, protocol


def graph(cwd, core, protocol, required, omit_hashes=False):
    approved = module.catalogue()[3]
    result = [{'Path': 'gobind', 'Main': True, 'Dir': str(cwd), 'GoMod': str(cwd/'go.mod')}]
    for p, v in required.items():
        row = {'Path': p, 'Version': v}
        if p in (module.CORE, module.PROTOCOL):
            path = core if p == module.CORE else protocol
            local = {'Path': str(path), 'Dir': str(path), 'GoMod': str(path/'go.mod')}
            row.update(Dir=str(path), GoMod=str(path/'go.mod'), Replace=local)
        elif not omit_hashes:
            row['GoModSum'] = approved[p, v+'/go.mod']
            if (p, v) in approved:
                row['Sum'] = approved[p, v]
        result.append(row)
    return '\n'.join(json.dumps(row) for row in result)


def actual_locks(core, protocol):
    version = 'v1.20.1'
    required = {module.CORE: module.CORE_GENERATED_VERSION, module.PROTOCOL: 'v0.0.40', 'cloud.google.com/go/compute': version}
    mod = ('module gobind\n\ngo 1.27\n\nreplace '+module.CORE+' => '+str(core)+'\n\nrequire (\n'+
           ''.join('\t'+p+' '+v+'\n' for p, v in required.items())+')\n\nreplace '+module.PROTOCOL+' => '+str(protocol)+'\n').encode()
    sums = ''.join('cloud.google.com/go/compute '+v+' '+module.catalogue()[3]['cloud.google.com/go/compute', v]+'\n'
                   for v in (version, version+'/go.mod')).encode()
    return mod, sums, required


def known_cache(cache):
    file = cache/'cache/download/cloud.google.com/go/compute/@v/v1.20.1.mod'
    file.parent.mkdir(parents=True); file.write_bytes((HERE/'testdata/compute-v1.20.1.mod').read_bytes())
    return file


class GeneratedClosure(unittest.TestCase):
    def test_original97_render_only_eight_updates_and_two_owned_paths(self):
        with tempfile.TemporaryDirectory() as value:
            root = Path(value).resolve(); core, protocol = core_fixture(root)
            before = module.render(core, protocol)
            self.assertEqual(len(re.findall(rb'^\t[^\s]+ v[^\s]+$', before, re.M)), 97)
            after = module.expected_projected_acquisition(before, core, protocol)
            module.validate_acquired(before, after, module.catalogue()[2], core, protocol)
            real = (HERE/'testdata/compute-v1.20.1.mod').read_bytes()
            self.assertEqual(module.digest(real), '488d935649ca7ae0730e7523d6fd81c2258984fa2d6284fd225a9354c9ade372')
            self.assertEqual(module.go_mod_h1(real), 'h1:4tCnrn48xsqlwSAiLf1HXMQk8CONslYbdiEZc9FEIbM=')
            self.assertEqual(module.cache_escape('github.com/OpenIMSDK/tools'), 'github.com/!open!i!m!s!d!k/tools')
            for bad in ['github.com/!open', '../module', 'module//path', '模块/路径']:
                with self.subTest(escape=bad), self.assertRaises(ValueError): module.cache_escape(bad)
            for bad in [before.replace(b'go 1.27', b'go 1.28'), before.replace(b'module gobind', b'module other'),
                        before.replace(str(protocol).encode(), b'/unapproved/protocol')]:
                with self.subTest(original=module.digest(bad)), self.assertRaises(ValueError):
                    module.expected_projected_acquisition(bad, core, protocol)
            for bad in [after.replace(b'github.com/OpenIMSDK/tools v0.0.24', b'github.com/OpenIMSDK/tools v0.0.25'),
                        after.replace(b'cloud.google.com/go/compute v1.20.1', b'cloud.google.com/go/compute v1.21.0')]:
                with self.subTest(after=module.digest(bad)), self.assertRaises(ValueError):
                    module.validate_acquired(before, bad, module.catalogue()[2], core, protocol)

    def test_real_prewarm_dispatch_boundary_uses_original_and_offline_locks(self):
        with tempfile.TemporaryDirectory() as value:
            root = Path(value).resolve(); core, protocol = core_fixture(root)
            original = module.render(core, protocol); events = []
            baseline = {n: (core/n).read_bytes() for n in ('go.mod', 'go.sum')}
            def command(w, label, args, cwd, env):
                events.append((label, args, cwd, env))
                self.assertEqual(cwd, root/'setup/generated-module')
                self.assertEqual(env['GOMODCACHE'], str(root/'owned-cache'))
                if label == 'generated-acquire':
                    self.assertEqual((cwd/'go.mod').read_bytes(), original)
                    self.assertEqual((cwd/'go.sum').read_bytes(), module.catalogue()[2])
                    self.assertEqual(args, ['/fixed/go', 'mod', 'download', 'all'])
                    (cwd/'go.mod').write_bytes(module.expected_projected_acquisition(original, core, protocol))
                else:
                    self.assertEqual(env['GOPROXY'], 'off'); self.assertEqual(env['GOSUMDB'], 'off')
                    self.assertEqual(env['GOFLAGS'], '-mod=readonly')
                required = {p.decode(): v.decode() for p, v in re.findall(rb'^\t([^\s]+) ([^\s]+)$', (cwd/'go.mod').read_bytes(), re.M)}
                return graph(cwd, core, protocol, required) if label == 'generated-graph' else ''
            # This control isolates real setup dispatch/locks; other controls use real .mod bytes/h1.
            with patch.object(module, 'cache_gomod', return_value={'scope': 'dispatch-only synthetic cache'}) as cached:
                module.prewarm(root, core, protocol, '/fixed/go', {'GOMODCACHE': str(root/'owned-cache')},
                               command, cloud.mkdir, cloud.put, cloud.record)
                self.assertGreater(cached.call_count, 0)
            self.assertEqual([e[0] for e in events], ['generated-acquire', 'generated-verify', 'generated-graph'])
            self.assertEqual(events[-1][1], ['/fixed/go', 'list', '-m', '-json', 'all'])
            self.assertEqual({n: (core/n).read_bytes() for n in baseline}, baseline)
            ready = json.loads((root/'evidence/generated-module/ready.json').read_bytes())
            self.assertFalse(ready['actual_bind_lock_installed'])
            self.assertEqual(ready['sum_sha256'], module.SUM_SHA)
            self.assertFalse((root/'tmp').exists())

    def test_actual_graph_and_checksums_reject_unapproved_identity_and_mutation(self):
        with tempfile.TemporaryDirectory() as value:
            root = Path(value).resolve(); core, protocol = core_fixture(root); cwd = root/'actual'
            cache = root/'owned-cache'; file = known_cache(cache)
            mod, sums, required = actual_locks(core, protocol)
            text = graph(cwd, core, protocol, required)
            proof = module.validate_generated(mod, sums, text, cwd, core, protocol, cache)
            self.assertEqual(proof['state'], 'passed_finite_actual_tidy_provenance')
            no_optional_hashes = graph(cwd, core, protocol, required, omit_hashes=True)
            self.assertEqual(module.validate_generated(mod, sums, no_optional_hashes, cwd, core, protocol, cache)['cache_mods'][0]['go_mod_h1'],
                             'h1:4tCnrn48xsqlwSAiLf1HXMQk8CONslYbdiEZc9FEIbM=')
            original = file.read_bytes()
            for variant in ['missing', 'changed', 'symlink', 'hardlink', 'foreign_escaped_module_path']:
                file.unlink()
                if variant == 'changed': file.write_bytes(original+b'// mutation\n')
                elif variant == 'symlink': file.symlink_to(HERE/'testdata/compute-v1.20.1.mod')
                elif variant == 'hardlink':
                    source = root/'hardlink-source'; source.write_bytes(original); os.link(source, file)
                elif variant == 'foreign_escaped_module_path':
                    wrong = cache/'cache/download/cloud.google.com/go/!compute/@v/v1.20.1.mod'; wrong.parent.mkdir(parents=True); wrong.write_bytes(original)
                with self.subTest(cache=variant), self.assertRaises((ValueError, FileNotFoundError)):
                    module.validate_generated(mod, sums, no_optional_hashes, cwd, core, protocol, cache)
                if file.is_symlink() or file.exists(): file.unlink()
                file.write_bytes(original)
            for badmod, badsum, badgraph in [
                (mod.replace(b'go 1.27', b'go 1.28'), sums, text),
                (mod.replace(str(protocol).encode(), b'/foreign/protocol'), sums, text),
                (mod, sums+b'unknown.invalid/module v1.0.0 h1:'+b'A'*43+b'=\n', text),
                (mod, sums.replace(b'h1:', b'h2:', 1), text),
                (mod, sums, text.replace('v0.0.40', 'v0.0.41')),
                (mod, sums, text.replace('"GoModSum": "h1:', '"GoModSum": "unapproved:')),
            ]:
                with self.subTest(module=module.digest(badmod), sum=module.digest(badsum), graph=module.digest(badgraph.encode())):
                    with self.assertRaises(ValueError):
                        module.validate_generated(badmod, badsum, badgraph, cwd, core, protocol, cache)

    def test_actual_observer_main_verifies_tidy_and_offline_selected_graph(self):
        for invalid in (False, True):
            with self.subTest(invalid_graph=invalid), tempfile.TemporaryDirectory() as value:
                job = Path(value).resolve(); root = job/'.qa/apple-cloud-1'; root.mkdir(parents=True, mode=0o700)
                core, protocol = core_fixture(root); (root/'bin').mkdir()
                cache = job/'setup/modcache'; known_cache(cache)
                parent = root/'tmp/gomobile-work-finite/ios'; cwd = parent/'src-arm64'
                for name in ('src', 'src-arm64'):
                    path = parent/name/'gobind'; path.mkdir(parents=True)
                    for f in ('source.go', 'source.m', 'source.h'): (path/f).write_text('// synthetic fixture\n')
                (cwd/'go.mod').write_bytes(module.render(core, protocol))
                mod, sums, required = actual_locks(core, protocol); text = graph(cwd, core, protocol, required, omit_hashes=True)
                if invalid: text = text.replace('v1.20.1', 'v9.99.99')
                (root/'fixture.json').write_text(json.dumps({'module': mod.decode(), 'sum': sums.decode(), 'graph': text}))
                child = root/'python-go-stub'
                child.write_text('#!'+sys.executable+'\nimport json,os,sys\nfrom pathlib import Path\n'
                    'r=Path(os.environ["FIXTURE_ROOT"]);p=json.loads((r/"fixture.json").read_text())\n'
                    'with (r/"actual-argv.jsonl").open("a") as f:f.write(json.dumps(sys.argv[1:])+"\\n")\n'
                    'if sys.argv[1:]==["mod","tidy","-v"]:\n Path("go.mod").write_text(p["module"]);Path("go.sum").write_text(p["sum"])\n'
                    'elif sys.argv[1:]==["list","-m","-json","all"]:print(p["graph"])\n'
                    'else:raise SystemExit(91)\n'); child.chmod(0o700)
                helper_path = REPO/'ci/android-native/go_observer.py'
                spec = importlib.util.spec_from_file_location('actual_fixture_io', helper_path)
                helper = importlib.util.module_from_spec(spec); spec.loader.exec_module(helper)
                config = {'cohort': 'auditable-sdk-apple-arm64-v1', 'root': str(root), 'go': str(child),
                    'go_sha256': module.digest(child.read_bytes()), 'base_path': '/usr/bin:/bin',
                    'observer_helper': str(helper_path), 'observer_helper_sha256': module.digest(helper_path.read_bytes()),
                    'module_provenance': {'schema': 'luobo.apple.generated-provenance.v1', 'helper': str(HERE/'generated_module.py'),
                        'helper_sha256': module.digest((HERE/'generated_module.py').read_bytes()), 'catalogue_sha256': module.SUM_SHA,
                        'core': str(core), 'protocol': str(protocol), 'module_cache': str(cache)}}
                helper.record(root/'observer.json', config)
                env = {'PATH': str(root/'bin')+':'+config['base_path'], 'APPLE_OBSERVER_CONFIG': str(root/'observer.json'),
                    'APPLE_OBSERVER_SHA': module.digest((root/'observer.json').read_bytes()), 'GOOS': 'ios', 'GOARCH': 'arm64',
                    'DARWIN_SDK': 'iphoneos', 'FIXTURE_ROOT': str(root), 'PYTHONDONTWRITEBYTECODE': '1'}
                env['GOMODCACHE'] = str(cache)
                with patch.dict(os.environ, env, clear=True), contextlib.chdir(cwd), patch.object(sys, 'argv', ['observer', 'mod', 'tidy', '-v']):
                    if invalid:
                        with self.assertRaises(ValueError): observer.main()
                    else:
                        self.assertEqual(observer.main(), 0)
                self.assertEqual([json.loads(line) for line in (root/'actual-argv.jsonl').read_text().splitlines()],
                                 [['mod', 'tidy', '-v'], ['list', '-m', '-json', 'all']])
                evidence = root/'evidence/generated/ios'; result = json.loads((evidence/'result.json').read_bytes())
                self.assertTrue(result['source_unchanged']); self.assertEqual(result['exit_code'], 0)
                if invalid:
                    self.assertEqual(result['failure_type'], 'ValueError'); self.assertIsNone(result['module_provenance'])
                    self.assertFalse((evidence/'module-provenance.json').exists())
                else:
                    self.assertEqual(result['module_provenance']['state'], 'passed_finite_actual_tidy_provenance')
                self.assertTrue((evidence/'before/src-arm64/go.mod').is_file())
                self.assertTrue((evidence/'after/src-arm64/go.sum').is_file())


class ActualCatalogue(unittest.TestCase):
    def test_actual_union_requires_exact_core_lock_and_keeps_warm610(self):
        with tempfile.TemporaryDirectory() as value:
            root = Path(value).resolve(); core, _ = core_fixture(root)
            warm = module.catalogue()[3]; locked = (core/'go.sum').read_bytes()
            approved = module.actual_catalogue(core)
            self.assertEqual(len(warm), 610); self.assertEqual(len(approved), 632)
            self.assertEqual({k: approved[k] for k in warm}, warm)
            for key, expected in [
                (('cloud.google.com/go/compute', 'v1.19.1/go.mod'), 'h1:6ylj3a05WF8leseCdIf77NK0g1ey+nj5IKd5/kvShxE='),
                (('golang.org/x/oauth2', 'v0.7.0/go.mod'), 'h1:hPLQkd9LyjfXTiRohC/41GhcFqxisoUQ99sCUOHO9x4='),
            ]:
                self.assertNotIn(key, warm); self.assertEqual(approved[key], expected)
            cache = root/'owned-cache'; cache.mkdir()
            with self.assertRaisesRegex(ValueError, 'outside independent cache catalogue'):
                module.cache_gomod(cache, 'cloud.google.com/go/compute', 'v1.19.1')
            # Union admission still requires the real bounded, independently hashed cache file.
            with self.assertRaises(FileNotFoundError):
                module.cache_gomod(cache, 'cloud.google.com/go/compute', 'v1.19.1', approved)
            for bad in [locked+b'unknown.invalid/module v1.0.0 h1:'+b'A'*43+b'=\n', locked.replace(b'h1:', b'h2:', 1)]:
                (core/'go.sum').write_bytes(bad)
                with self.assertRaisesRegex(ValueError, 'original core8eb9'):
                    module.actual_catalogue(core)
            (core/'go.sum').unlink(); source = root/'other.sum'; source.write_bytes(locked)
            (core/'go.sum').symlink_to(source)
            with self.assertRaises(ValueError): module.actual_catalogue(core)
            (core/'go.sum').unlink(); (core/'go.sum').write_bytes(locked)
            self.assertEqual(module.catalogue()[3], warm)

    def test_actual_graph_routes_verified_union_and_rejects_unknown_or_changed_core(self):
        with tempfile.TemporaryDirectory() as value:
            root = Path(value).resolve(); core, protocol = core_fixture(root); cache = root/'owned-cache'
            cwd = root/'actual'; mod, sums, _ = actual_locks(core, protocol)
            mod = mod.replace(b'cloud.google.com/go/compute v1.20.1', b'cloud.google.com/go/compute v1.19.1')
            required = {module.CORE: module.CORE_GENERATED_VERSION, module.PROTOCOL: 'v0.0.40',
                        'cloud.google.com/go/compute': 'v1.19.1', 'golang.org/x/oauth2': 'v0.7.0'}
            text = graph(cwd, core, protocol, required, omit_hashes=True); calls = []
            def cache_boundary(actual_cache, path, version, approved):
                self.assertEqual(actual_cache, cache); self.assertEqual(len(approved), 632)
                self.assertEqual(approved, module.actual_catalogue(core))
                calls.append((path, version))
                return {'scope': 'union-routing-only; existing real-cache control retained', 'path': path, 'version': version}
            with patch.object(module, 'cache_gomod', side_effect=cache_boundary):
                proof = module.validate_generated(mod, sums, text, cwd, core, protocol, cache)
                self.assertEqual(calls, [('cloud.google.com/go/compute', 'v1.19.1'), ('golang.org/x/oauth2', 'v0.7.0')])
                self.assertEqual(proof['actual_catalogue_rows'], 632)
                self.assertEqual(proof['independent_core_catalogue_sha256'], module.CORE_SUM)
                with self.assertRaises(ValueError):
                    module.validate_generated(mod, sums, text.replace('v0.7.0', 'v0.7.1'), cwd, core, protocol, cache)
            original = (core/'go.sum').read_bytes()
            def changed_core(*_args):
                (core/'go.sum').write_bytes(original+b'\n')
                return {'scope': 'mutation injection only'}
            with patch.object(module, 'cache_gomod', side_effect=changed_core), self.assertRaisesRegex(ValueError, 'core lock changed'):
                module.validate_generated(mod, sums, text, cwd, core, protocol, cache)
            (core/'go.sum').write_bytes(original)


if __name__ == '__main__':
    unittest.main()
