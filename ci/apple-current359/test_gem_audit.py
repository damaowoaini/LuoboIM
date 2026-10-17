"""Prepared new2 filesystem controls; no Ruby/Pod/native subprocesses."""
import copy
import hashlib
import json
import os
from pathlib import Path
import shutil
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

import gem_audit


class GemSourceAudit(unittest.TestCase):
    def observed_path(self, value):
        p = Path(value)
        kind = 'file' if p.is_file() else 'directory' if p.is_dir() else 'other' if p.exists() else 'missing'
        return {'lexical': str(value), 'canonical': str(p.resolve(strict=True)) if p.exists() else None, 'kind': kind}

    def refresh_maps(self, source):
        for gem in source['gems']:
            gem['path_mappings'] = {'spec': self.observed_path(gem['loaded_from']),
                                    'gem': self.observed_path(gem['full_gem_path']),
                                    'requires': [self.observed_path(p) for p in gem['full_require_paths']]}

    def system_sources(self, aliases):
        aliases['gem'].unlink()
        shutil.copytree(aliases['target_gem'], aliases['gem'])

    def bundled_alias(self, source, selected, name='minitest', version='5.20.0'):
        full_name = name+'-'+version
        base = Path(selected['ruby']['path']).parent.parent/'lib/ruby/gems/3.3.0'
        target_spec = base/'specifications'/(full_name+'.gemspec')
        target_spec.parent.mkdir(parents=True, exist_ok=True)
        target_spec.write_bytes(b'# selected Ruby bundled spec\n'); target_spec.chmod(0o644)
        target_gem = base/'gems'/full_name
        (target_gem/'lib').mkdir(parents=True)
        (target_gem/'lib'/('minitest.rb')).write_bytes(b'# selected bundled source\n')
        system = Path(selected['gem_root'])
        logical_spec = system/'specifications'/target_spec.name
        logical_spec.symlink_to(target_spec)
        logical_gem = system/'gems'/full_name
        logical_gem.symlink_to(target_gem, target_is_directory=True)
        source['gems'].append({'name': name, 'version': version, 'full_name': full_name,
                               'default': False, 'loaded_from': str(logical_spec),
                               'full_gem_path': str(logical_gem), 'require_paths': ['lib'],
                               'full_require_paths': [str(logical_gem/'lib')], 'files': ['lib/minitest.rb']})
        source['load_path'].append(str(logical_gem/'lib'))
        self.refresh_maps(source)
        return {'spec': logical_spec, 'gem': logical_gem, 'target_spec': target_spec,
                'target_gem': target_gem, 'base': base}

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
                            'sha256': hashlib.sha256(ruby.read_bytes()).hexdigest(), 'mode': 0o644,
                            'uid': ruby.stat().st_uid, 'gid': ruby.stat().st_gid},
                    'gem_root': str(gem_root)}
        source = {'schema': 2, 'ruby': '3.3.12', 'rubygems': '4.0.20', 'ruby_binary': str(ruby),
                  'config': {'prefix': str(ruby_root), 'bindir': str(ruby.parent), 'libdir': str(lib),
                             'rubylibdir': str(stdlib), 'rubyarchdir': str(arch), 'ruby_version': '3.3.0',
                             'arch': 'arm64-darwin', 'DLEXT': 'bundle', 'ENABLE_SHARED': 'yes',
                             'LIBRUBY_SO': runtime.name},
                  'gem_dir': str(gem_root), 'gem_path': [str(gem_root)], 'default_dir': str(gem_root),
                  'default_specifications_dir': str(default_specs),
                  'load_path': [str(stdlib), str(arch)]+[g['full_require_paths'][0] for g in gems if not g['default']],
                  'gems': gems, 'features': features}
        pin = {'bytes': config.stat().st_size, 'sha256': hashlib.sha256(config.read_bytes()).hexdigest()}
        self.refresh_maps(source)
        return source, selected, home, pin

    def leaf_fixture(self, root):
        source, selected, home, pin = self.fixture(root)
        aliases = self.bundled_alias(source, selected)
        self.system_sources(aliases)
        target = aliases['target_gem']/'History.rdoc'
        target.write_bytes(b'bundled documentation body\n'); target.chmod(0o644)
        entry = aliases['gem']/target.name
        entry.symlink_to(os.path.relpath(target, entry.parent))
        self.refresh_maps(source)
        return source, selected, home, pin, aliases, entry, target

    def test_registered_bundled_leaf_aliases_record_real_bodies_and_exact_loaded_owner(self):
        with tempfile.TemporaryDirectory() as directory:
            source, selected, home, pin, aliases, entry, target = self.leaf_fixture(Path(directory))
            ruby_entry = aliases['gem']/'lib/minitest.rb'
            ruby_entry.unlink()
            ruby_target = aliases['target_gem']/'lib/minitest.rb'
            ruby_entry.symlink_to(os.path.relpath(ruby_target, ruby_entry.parent))
            source['features'].append({'feature': str(ruby_entry), 'bootstrap': False, 'paths': [str(ruby_target)]})
            proof = gem_audit.audit(source, selected, home, pin)
            bundled = proof['gems'][-1]
            self.assertEqual(bundled['name'], 'minitest')
            self.assertEqual(bundled['layout'], 'selected_bundled_spec_with_system_sources')
            records = {r['entry_relative']: r for r in bundled['file_leaf_aliases']}
            self.assertEqual(set(records), {'History.rdoc', 'lib/minitest.rb'})
            for relative, actual_entry, actual_target in [('History.rdoc', entry, target),
                                                          ('lib/minitest.rb', ruby_entry, ruby_target)]:
                record = records[relative]
                self.assertEqual(record['logical'], str(actual_entry))
                self.assertEqual(record['symbolic_target'], os.readlink(actual_entry))
                self.assertEqual(record['canonical_target'], str(actual_target))
                self.assertEqual(record['full_name'], 'minitest-5.20.0')
                self.assertEqual(record['body']['sha256'], hashlib.sha256(actual_target.read_bytes()).hexdigest())
                self.assertEqual(record['body']['bytes'], actual_target.stat().st_size)
                self.assertEqual(record['target_identity'][5:7], (selected['ruby']['uid'], selected['ruby']['gid']))
                self.assertEqual(len([r for r in proof['source_bodies'] if r['path'] == str(actual_target)]), 1)
            self.assertEqual(bundled['ruby_sources'], [records['lib/minitest.rb']['body']])
            self.assertEqual(bundled['loaded_files'], [str(ruby_target)])
            self.assertEqual(proof['loaded_features'][-1]['owner'], 'minitest')
            self.assertFalse(proof['source_provenance_Root_acceptance'])
            json.dumps(proof)

    def test_registered_bundled_leaf_aliases_refuse_unsafe_or_changed_targets(self):
        kinds = ['external', 'wrong-fullname', 'wrong-relative', 'directory', 'target-chain',
                 'parent-link', 'cycle', 'hardlink', 'writable-file', 'writable-parent',
                 'wrong-owner', 'wrong-gid', 'wrong-alias-owner', 'missing-selected-owner', 'late-remap', 'late-body']
        for kind in kinds:
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                source, selected, home, pin, aliases, entry, target = self.leaf_fixture(root)
                baseline = gem_audit.audit(source, selected, home, pin)
                self.assertEqual(baseline['gems'][-1]['file_leaf_aliases'][0]['canonical_target'], str(target))
                self.assertEqual(baseline['gems'][-1]['file_leaf_aliases'][0]['body']['sha256'],
                                 hashlib.sha256(target.read_bytes()).hexdigest())
                if kind in ['external', 'wrong-fullname', 'wrong-relative']:
                    replacement = (root/'foreign/History.rdoc' if kind == 'external' else
                                   aliases['base']/'gems/minitest-5.19.0/History.rdoc' if kind == 'wrong-fullname' else
                                   aliases['target_gem']/'Other.rdoc')
                    replacement.parent.mkdir(parents=True, exist_ok=True)
                    replacement.write_bytes(b'foreign counterpart'); replacement.chmod(0o644)
                    entry.unlink(); entry.symlink_to(replacement)
                elif kind == 'directory':
                    target.unlink(); target.mkdir()
                elif kind == 'target-chain':
                    alternate = target.with_name('body.rdoc'); alternate.write_bytes(b'chain body')
                    target.unlink(); target.symlink_to(alternate)
                elif kind == 'parent-link':
                    alternate = aliases['target_gem'].with_name('minitest-5.20.0-real')
                    aliases['target_gem'].rename(alternate)
                    aliases['target_gem'].symlink_to(alternate, target_is_directory=True)
                elif kind == 'cycle':
                    target.unlink(); target.symlink_to(target.name)
                elif kind == 'hardlink':
                    os.link(target, target.with_name('duplicate.rdoc'))
                elif kind == 'writable-file':
                    target.chmod(0o666)
                elif kind == 'writable-parent':
                    target.parent.chmod(0o777)
                elif kind == 'missing-selected-owner':
                    selected['ruby'].pop('uid')
                original_stat, original_walk = gem_audit.os.stat, gem_audit.os.walk
                checked_parent = original_stat(entry.parent if kind == 'wrong-alias-owner' else target.parent)
                def observed_stat(path, *args, **kwargs):
                    observed = original_stat(path, *args, **kwargs)
                    parent_fd = kwargs.get('dir_fd')
                    if (kind in ['wrong-owner', 'wrong-gid', 'wrong-alias-owner'] and path == target.name and
                            parent_fd is not None and os.fstat(parent_fd).st_ino == checked_parent.st_ino):
                        fields = {name: getattr(observed, name) for name in dir(observed) if name.startswith('st_')}
                        fields['st_gid' if kind == 'wrong-gid' else 'st_uid'] += 1
                        return SimpleNamespace(**fields)
                    return observed
                def walking(path, *args, **kwargs):
                    yield from original_walk(path, *args, **kwargs)
                    if Path(path) == aliases['gem']:
                        if kind == 'late-remap':
                            entry.unlink(); entry.symlink_to(aliases['target_gem']/'missing.rdoc')
                        elif kind == 'late-body':
                            target.write_bytes(b'body changed after initial observation\n')
                with mock.patch.object(gem_audit.os, 'stat', side_effect=observed_stat), (
                        mock.patch.object(gem_audit.os, 'walk', side_effect=walking)):
                    with self.assertRaises(gem_audit.AuditFailure) as rejected:
                        gem_audit.audit(source, selected, home, pin)
                self.assertEqual(rejected.exception.proof['subject'], 'minitest')
                self.assertEqual(rejected.exception.proof['field'],
                                 'source_alias.mapping' if kind in ['late-remap', 'late-body'] else 'source_walk')
                if kind not in ['late-remap', 'late-body']:
                    self.assertEqual(rejected.exception.proof['reason'], 'gem tree symlink refused')
                    self.assertFalse(rejected.exception.proof['link']['linked_body_read'])

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
            self.refresh_maps(source)
            source['features'].append({'feature': str(native), 'bootstrap': False, 'paths': [str(native)]})
            changed = gem_audit.audit(source, selected, home, pin)
            self.assertIn(str(native), changed['gems'][-1]['loaded_files'])
            self.assertEqual(changed['configured_shared_libruby'], proof['configured_shared_libruby'])
            json.dumps(changed)  # evidence must remain serializable
            aliases = self.bundled_alias(source, selected)
            mapped = gem_audit.audit(source, selected, home, pin)
            bundled = mapped['gems'][-1]
            self.assertEqual(bundled['name'], 'minitest')
            self.assertEqual(bundled['canonical_gem_root'], str(aliases['target_gem']))
            self.assertEqual(bundled['path_mappings']['spec']['canonical'], str(aliases['target_spec']))
            self.assertTrue(bundled['activation_only_no_individual_loaded_file'])
            feature = aliases['gem']/'lib/minitest.rb'
            source['features'].append({'feature': str(feature), 'bootstrap': False,
                                       'paths': [str(feature.resolve(strict=True))]})
            loaded = gem_audit.audit(source, selected, home, pin)
            self.assertEqual(loaded['gems'][-1]['loaded_files'], [str(feature.resolve(strict=True))])
            self.assertEqual(loaded['loaded_features'][-1]['owner'], 'minitest')
            self.system_sources(aliases)
            self.refresh_maps(source)
            source['features'][-1]['paths'] = [str(feature.resolve(strict=True))]
            hybrid = gem_audit.audit(source, selected, home, pin)
            self.assertEqual(hybrid['gems'][-1]['layout'], 'selected_bundled_spec_with_system_sources')
            self.assertEqual(hybrid['gems'][-1]['canonical_spec_base'], str(aliases['base']))
            self.assertEqual(hybrid['gems'][-1]['canonical_source_base'], selected['gem_root'])
            self.assertEqual(hybrid['gems'][-1]['canonical_gem_root'], str(aliases['gem']))
            self.assertEqual(hybrid['gems'][-1]['loaded_files'], [str(feature)])
            self.assertEqual(hybrid['loaded_features'][-1]['owner'], 'minitest')
            self.assertTrue(hybrid['gems'][-1]['ruby_sources'])
            self.assertFalse(hybrid['source_provenance_Root_acceptance'])

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
            s['gems'][2]['path_mappings']['requires'] = copy.deepcopy(s['gems'][3]['path_mappings']['requires'])
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
        # Each alias denial starts with the same valid hybrid actually observed.
        for change, field in [('other-ruby', 'loaded_from'), ('other-fullname', 'loaded_from'),
                              ('wrong-layout', 'loaded_from'), ('unlisted-bundle', 'loaded_from'),
                              ('hybrid-require-other-base', 'full_require_paths'), ('require-remap', 'full_require_paths'),
                              ('stale-probe', 'loaded_from.mapping'), ('late-remap', 'loaded_from.mapping')]:
            with self.subTest(alias=change), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                source, selected, home, pin = self.fixture(root)
                aliases = self.bundled_alias(source, selected)
                self.system_sources(aliases)
                self.refresh_maps(source)
                baseline = gem_audit.audit(copy.deepcopy(source), selected, home, pin)
                self.assertEqual(baseline['gems'][-1]['canonical_gem_root'], str(aliases['gem']))
                self.assertEqual(baseline['gems'][-1]['layout'], 'selected_bundled_spec_with_system_sources')
                def retarget_spec(target):
                    target.parent.mkdir(parents=True, exist_ok=True)
                    target.write_bytes(b'# replacement spec\n')
                    aliases['spec'].unlink(); aliases['spec'].symlink_to(target)
                if change == 'other-ruby':
                    retarget_spec(root/'other-ruby/lib/ruby/gems/3.3.0/specifications/minitest-5.20.0.gemspec')
                elif change == 'other-fullname':
                    retarget_spec(aliases['base']/'specifications/minitest-5.19.0.gemspec')
                elif change == 'wrong-layout':
                    retarget_spec(aliases['base']/'misc/minitest-5.20.0.gemspec')
                elif change == 'unlisted-bundle':
                    self.bundled_alias(source, selected, name='unreviewed')
                elif change == 'hybrid-require-other-base':
                    shutil.rmtree(aliases['gem']/'lib')
                    (aliases['gem']/'lib').symlink_to(aliases['target_gem']/'lib', target_is_directory=True)
                elif change == 'require-remap':
                    shutil.rmtree(aliases['gem']/'lib')
                    other = aliases['base']/'other-lib'; other.mkdir()
                    (aliases['gem']/'lib').symlink_to(other, target_is_directory=True)
                elif change == 'stale-probe':
                    retarget_spec(aliases['base']/'misc/minitest-5.20.0.gemspec')
                if change != 'stale-probe':
                    self.refresh_maps(source)
                real_walk = gem_audit.os.walk
                def walking(path, *args, **kwargs):
                    yield from real_walk(path, *args, **kwargs)
                    if change == 'late-remap' and Path(path) == aliases['gem']:
                        retarget_spec(aliases['base']/'misc/minitest-5.20.0.gemspec')
                with mock.patch.object(gem_audit.os, 'walk', side_effect=walking):
                    with self.assertRaises(gem_audit.AuditFailure) as rejected:
                        gem_audit.audit(source, selected, home, pin)
                self.assertEqual(rejected.exception.proof['field'], field)
                self.assertEqual(rejected.exception.proof['subject'], 'unreviewed' if change == 'unlisted-bundle' else 'minitest')

    def test_tree_symlink_refusal_records_bounded_relative_diagnostics_without_private_targets(self):
        for kind in ['selected-file', 'selected-directory', 'system-file', 'user-home',
                     'outside-root', 'missing', 'long-relative-entry']:
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                source, selected, home, pin = self.fixture(root)
                baseline = gem_audit.audit(source, selected, home, pin)
                self.assertEqual(baseline['ruby_binary']['sha256'], selected['ruby']['sha256'])
                ruby = Path(selected['ruby']['path']).parent.parent
                system = Path(selected['gem_root'])
                gem = system/'gems/cocoapods-1.17.0'
                link = gem/'diagnostic-link'
                target = ruby/'lib/ruby/3.3.0/logger.rb'
                expected_root, expected_kind = 'selected_ruby', 'file'
                if kind == 'selected-directory':
                    target = target.parent
                    expected_kind = 'directory'
                elif kind == 'system-file':
                    target = system/'gems/cocoapods-core-1.17.0/lib/cocoapods-core.rb'
                    expected_root = 'selected_system_gems'
                elif kind in ['user-home', 'outside-root']:
                    target = (home if kind == 'user-home' else root/'unapproved')/'private-never-print.txt'
                    target.parent.mkdir(parents=True, exist_ok=True)
                    target.write_bytes(b'private body must not be read')
                    expected_root = 'user_home' if kind == 'user-home' else 'outside_selected_roots'
                    expected_kind = 'not_inspected'
                elif kind == 'missing':
                    target = ruby/'absent-target'
                    expected_root, expected_kind = 'unresolved', 'unresolved'
                elif kind == 'long-relative-entry':
                    link = gem.joinpath(*['d'*100+str(i) for i in range(6)], 'diagnostic-link')
                    link.parent.mkdir(parents=True)
                link.symlink_to(target, target_is_directory=kind == 'selected-directory')
                original_open = Path.open
                def checked_open(path, *args, **kwargs):
                    self.assertNotEqual(path, target, 'symlink target body was read')
                    return original_open(path, *args, **kwargs)
                with mock.patch.object(Path, 'open', checked_open):
                    with self.assertRaises(gem_audit.AuditFailure) as rejected:
                        gem_audit.audit(source, selected, home, pin)
                self.assertEqual(rejected.exception.proof['field'], 'source_walk')
                self.assertEqual(rejected.exception.proof['subject'], 'cocoapods')
                detail = rejected.exception.proof['link']
                self.assertEqual(detail['link_kind'], 'symbolic_link')
                self.assertEqual(detail['target_root'], expected_root)
                self.assertEqual(detail['target_kind'], expected_kind)
                self.assertFalse(detail['linked_body_read'])
                self.assertEqual(detail['entry_relative'], None if kind == 'long-relative-entry' else 'diagnostic-link')
                if kind in ['selected-file', 'selected-directory', 'system-file', 'long-relative-entry']:
                    base = system if expected_root == 'selected_system_gems' else ruby
                    self.assertEqual(detail['target_relative'], target.relative_to(base).as_posix())
                else:
                    self.assertIsNone(detail['target_relative'])
                payload = json.dumps(rejected.exception.proof)
                self.assertLess(len(payload), 4096)
                self.assertNotIn(str(root), payload)
                self.assertNotIn('private-never-print', payload)


if __name__ == '__main__':
    unittest.main()
