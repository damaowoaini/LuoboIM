"""New3 prepared FS controls; no Ruby/CocoaPods/compiler/network execution."""
import hashlib
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import gem_audit
import test_gem_audit


class ConfiguredRubyGemsSources(unittest.TestCase):
    def fixture(self, root):
        helper = test_gem_audit.GemSourceAudit()
        source, selected, home, pin = helper.fixture(root)
        library = Path(source['config']['sitelibdir'])
        paths = [library/'rubygems.rb', library/'rubygems/defaults.rb']
        for p in paths:
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_bytes(b'# selected configured bootstrap RubyGems source\n')
            p.chmod(0o644)
            source['features'].append({'feature': str(p), 'bootstrap': True, 'paths': [str(p)]})
        source['load_path'].append(str(library))
        source['config_path_mappings'] = {key: helper.observed_path(source['config'][key])
                                         for key in gem_audit.BOOTSTRAP_CONFIG_KEYS}
        return source, selected, home, pin, paths

    def test_actual_audit_accepts_only_configured_loaded_bootstrap_RubyGems_and_records_source(self):
        with tempfile.TemporaryDirectory() as temporary:
            source, selected, home, pin, paths = self.fixture(Path(temporary))
            proof = gem_audit.audit(source, selected, home, pin)
            configured = proof['configured_bootstrap_RubyGems']
            self.assertEqual(configured['loaded_body_count'], 2)
            self.assertEqual(set(configured['configuration_paths']), set(gem_audit.BOOTSTRAP_CONFIG_KEYS))
            self.assertTrue(configured['no_directory_wide_grant'])
            self.assertTrue(configured['first_observation_not_official_content_proof'])
            self.assertTrue(configured['final_owner_parents_FD_path_body_rechecked'])
            self.assertFalse(proof['source_provenance_Root_acceptance'])
            rows = {row['body']['path']: row for row in configured['sources']}
            self.assertEqual(set(rows), set(map(str, paths)))
            for p in paths:
                row = rows[str(p)]
                self.assertEqual(row['body']['sha256'], hashlib.sha256(p.read_bytes()).hexdigest())
                self.assertEqual(row['identity'][3], 1)
                self.assertEqual(row['identity'][5:7], (selected['ruby']['uid'], selected['ruby']['gid']))
                self.assertEqual(len([b for b in proof['source_bodies'] if b['path'] == str(p)]), 1)
            self.assertFalse(any('/vendor_ruby/' in row['body']['path'] for row in configured['sources']))
            self.assertTrue(all(row['owner'] == 'selected_Ruby_configured_bootstrap_RubyGems'
                                for row in proof['loaded_features'][-2:]))

    def test_actual_audit_refuses_unconfigured_nonbootstrap_class_alias_owner_and_unsafe_FS(self):
        for variant in ['not-bootstrap', 'other-source-class', 'unknown-config', 'missing-mapping',
                        'mapping-changed', 'symlink', 'hardlink', 'writable-file', 'writable-parent',
                        'wrong-selected-owner', 'foreign-system-root']:
            with self.subTest(variant=variant), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                source, selected, home, pin, paths = self.fixture(root)
                target = paths[-1]
                feature = source['features'][-1]
                if variant == 'not-bootstrap':
                    feature['bootstrap'] = False
                elif variant == 'other-source-class':
                    alternate = target.parent.parent/'arbitrary.rb'
                    target.rename(alternate)
                    feature.update(feature=str(alternate), paths=[str(alternate)])
                elif variant == 'unknown-config':
                    source['config']['sitelibdir'] = str(root/'unrelated')
                elif variant == 'missing-mapping':
                    source['config_path_mappings'].pop('vendorarchdir')
                elif variant == 'mapping-changed':
                    source['config_path_mappings']['sitelibdir']['canonical'] = str(root/'foreign')
                elif variant == 'symlink':
                    alternate = target.with_name('real.rb')
                    target.rename(alternate)
                    target.symlink_to(alternate.name)
                elif variant == 'hardlink':
                    os.link(target, target.with_name('another.rb'))
                elif variant == 'writable-file':
                    target.chmod(0o666)
                elif variant == 'writable-parent':
                    target.parent.chmod(0o777)
                elif variant == 'wrong-selected-owner':
                    selected['ruby']['uid'] += 1
                else:
                    alternate = root/'foreign/rubygems/defaults.rb'
                    alternate.parent.mkdir(parents=True)
                    alternate.write_bytes(b'# no selected configuration provenance\n')
                    feature.update(feature=str(alternate), paths=[str(alternate)])
                with self.assertRaises(gem_audit.AuditFailure) as rejected:
                    gem_audit.audit(source, selected, home, pin)
                self.assertEqual(rejected.exception.proof['field'],
                                 'feature.paths' if variant == 'foreign-system-root' else 'configured_rubygems')

    def test_actual_audit_final_recheck_rejects_late_source_or_config_parent_changes(self):
        original = gem_audit.ConfiguredRubyGems.finish
        for variant in ['late-body', 'late-parent-alias']:
            with self.subTest(variant=variant), tempfile.TemporaryDirectory() as temporary:
                source, selected, home, pin, paths = self.fixture(Path(temporary))
                def finish(subject):
                    if variant == 'late-body':
                        paths[-1].write_bytes(b'# changed after the initial source observation\n')
                    else:
                        directory = paths[-1].parent
                        alternate = directory.with_name('rubygems-relocated')
                        directory.rename(alternate)
                        directory.symlink_to(alternate.name, target_is_directory=True)
                    return original(subject)
                with mock.patch.object(gem_audit.ConfiguredRubyGems, 'finish', finish):
                    with self.assertRaises(gem_audit.AuditFailure) as rejected:
                        gem_audit.audit(source, selected, home, pin)
                self.assertEqual(rejected.exception.proof['field'], 'configured_rubygems')
                self.assertEqual(rejected.exception.proof['subject'], 'final_recheck')


if __name__ == '__main__':
    unittest.main(verbosity=2)
