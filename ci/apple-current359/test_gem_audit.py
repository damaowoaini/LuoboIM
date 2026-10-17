"""Prepared new2 filesystem controls; no Ruby/Pod/native subprocesses."""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import gem_audit


class GemSourceAudit(unittest.TestCase):
    def fixture(self, root):
        ruby_root, gem_root, home = root/'ruby', root/'system-gems', root/'real-home'
        home.mkdir()
        def put(path, data=b'# actual source\n'):
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
            path.chmod(0o644)
            return path
        ruby = put(ruby_root/'bin/ruby', b'selected executable')
        lib = ruby_root/'lib'
        stdlib = lib/'ruby/3.3.0'
        arch = stdlib/'arm64-darwin'
        arch.mkdir(parents=True)
        runtime = put(lib/'libruby.3.3.dylib', b'configured shared runtime')
        rbconfig = put(arch/'rbconfig.rb')
        rubygems = put(stdlib/'rubygems.rb')
        logger = put(stdlib/'logger.rb')
        default = lib/'ruby/gems/3.3.0'
        default_specs = default/'specifications/default'
        gems, features = [], []
        for name, is_default in [('logger', True), ('ruby2_keywords', True),
                                 ('cocoapods', False), ('cocoapods-core', False)]:
            version = '1.0' if is_default else '1.17.0'
            base = default if is_default else gem_root
            spec = put(base/('specifications/default' if is_default else 'specifications')/(name+'-'+version+'.gemspec'))
            computed = base/'gems'/(name+'-'+version)
            gems.append({'name': name, 'version': version, 'full_name': name+'-'+version,
                         'default': is_default, 'loaded_from': str(spec), 'full_gem_path': str(computed),
                         'require_paths': ['lib'], 'full_require_paths': [str(computed/'lib')],
                         'files': ['lib/'+name+'.rb']})
            if not is_default:
                feature = put(computed/('lib/'+name+'.rb'))
                features.append({'feature': str(feature), 'bootstrap': False, 'paths': [str(feature)]})
        config = put(gem_root/'gems/cocoapods-1.17.0/lib/cocoapods/config.rb', b'# official config\n')
        for p in [rbconfig, rubygems, logger]:
            features.append({'feature': str(p), 'bootstrap': False, 'paths': [str(p)]})
        for marker in gem_audit.BUILTINS:
            features.append({'feature': marker, 'bootstrap': True, 'paths': []})
        selected = {'ruby': {'path': str(ruby), 'bytes': ruby.stat().st_size,
                            'sha256': hashlib.sha256(ruby.read_bytes()).hexdigest(), 'mode': 0o644},
                    'gem_root': str(gem_root)}
        source = {'schema': 1, 'ruby': '3.3.12', 'rubygems': '3.5.22', 'ruby_binary': str(ruby),
                  'config': {'prefix': str(ruby_root), 'bindir': str(ruby.parent), 'libdir': str(lib),
                             'rubylibdir': str(stdlib), 'rubyarchdir': str(arch), 'ruby_version': '3.3.0',
                             'arch': 'arm64-darwin', 'DLEXT': 'bundle', 'ENABLE_SHARED': 'yes',
                             'LIBRUBY_SO': runtime.name},
                  'gem_dir': str(gem_root), 'gem_path': [str(gem_root)], 'default_dir': str(gem_root),
                  'default_specifications_dir': str(default_specs),
                  'load_path': [str(stdlib), str(arch)]+[g['full_require_paths'][0] for g in gems if not g['default']],
                  'gems': gems, 'features': features}
        pin = {'bytes': config.stat().st_size, 'sha256': hashlib.sha256(config.read_bytes()).hexdigest()}
        return source, selected, home, pin

    def test_real_specs_stdlib_and_compiled_markers_replace_synthetic_default_roots(self):
        with tempfile.TemporaryDirectory() as directory:
            source, selected, home, pin = self.fixture(Path(directory))
            proof = gem_audit.audit(source, selected, home, pin)
            defaults = {g['name']: g for g in proof['gems'] if g['default']}
            self.assertFalse(Path(defaults['logger']['full_gem_path']).exists())
            self.assertFalse(defaults['logger']['computed_directory_is_source_evidence'])
            self.assertEqual(len(defaults['logger']['loaded_files']), 1)
            self.assertTrue(defaults['ruby2_keywords']['activation_only_no_individual_loaded_file'])
            self.assertEqual(len([f for f in proof['loaded_features'] if f['kind'] == 'MRI_compiled_startup_marker']), 6)
            self.assertEqual(proof['ruby_binary']['sha256'], selected['ruby']['sha256'])
            self.assertTrue(proof['configured_shared_libruby']['sha256'])
            self.assertFalse(proof['source_provenance_Root_acceptance'])
            self.assertTrue(all(g['spec_body']['sha256'] for g in proof['gems']))
            self.assertTrue(all(g['ruby_sources'] for g in proof['gems'] if not g['default']))
            source['load_path'].append('/opt/homebrew/lib/ruby/site_ruby/3.3.0')
            # Real relative file and native extension paths remain sources, not marker exceptions.
            source['features'][2]['feature'] = 'rbconfig.rb'
            extension = Path(selected['gem_root'])/'extensions/arm64-darwin/3.3.0/cocoapods-core-1.17.0'
            extension.mkdir(parents=True)
            native = extension/'core.bundle'; native.write_bytes(b'extension bytes')
            source['gems'][-1]['full_require_paths'].append(str(extension))
            source['features'].append({'feature': str(native), 'bootstrap': False, 'paths': [str(native)]})
            changed = gem_audit.audit(source, selected, home, pin)
            self.assertIn(str(native), changed['gems'][-1]['loaded_files'])
            self.assertEqual(changed['configured_shared_libruby'], proof['configured_shared_libruby'])
            json.dumps(changed)  # evidence must remain serializable

    def test_unknown_or_missing_sources_cannot_hide_behind_default_or_builtin_flags(self):
        def missing_spec(s, _):
            Path(s['gems'][0]['loaded_from']).unlink()
        def missing_feature(s, _):
            Path(s['features'][0]['paths'][0]).unlink()
        def unknown_marker(s, _):
            s['features'].append({'feature': 'unknown.so', 'bootstrap': True, 'paths': []})
        def late_marker(s, _):
            s['features'][-1]['bootstrap'] = False
        def user_source(s, home):
            p = home/'injected.rb'; p.write_bytes(b'no')
            s['features'].append({'feature': str(p), 'bootstrap': False, 'paths': [str(p)]})
        def wrong_flag(s, _):
            s['gems'][0]['default'] = False
        def missing_regular_root(s, _):
            s['gems'][2]['full_gem_path'] += '-absent'
        def missing_runtime(s, _):
            (Path(s['config']['libdir'])/s['config']['LIBRUBY_SO']).unlink()
        def wrong_interpreter(s, _):
            Path(s['ruby_binary']).write_bytes(b'changed loader')
        def ambient_gem_path(s, home):
            s['gem_path'].append(str(home))
        def require_other_gem(s, _):
            s['gems'][2]['full_require_paths'] = s['gems'][3]['full_require_paths']
        for change, expected_field in [(missing_spec, 'loaded_from'), (missing_feature, 'feature.paths'),
                                       (unknown_marker, 'feature.paths'), (late_marker, 'feature.paths'),
                                       (user_source, 'feature.paths'), (wrong_flag, 'default'),
                                       (missing_regular_root, 'full_gem_path'), (missing_runtime, 'libruby'),
                                       (wrong_interpreter, 'ruby_binary'), (ambient_gem_path, 'gem_path'),
                                       (require_other_gem, 'full_require_paths')]:
            with self.subTest(change=change.__name__), tempfile.TemporaryDirectory() as directory:
                source, selected, home, pin = self.fixture(Path(directory))
                baseline = gem_audit.audit(copy.deepcopy(source), selected, home, pin)
                self.assertEqual(baseline['ruby_binary']['sha256'], selected['ruby']['sha256'])
                change(source, home)
                with self.assertRaises(gem_audit.AuditFailure) as rejected:
                    gem_audit.audit(copy.deepcopy(source), selected, home, pin)
                self.assertEqual(rejected.exception.proof['field'], expected_field)
                self.assertTrue(rejected.exception.proof['subject'])


if __name__ == '__main__':
    unittest.main()
