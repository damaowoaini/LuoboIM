"""One focused metadata admission test; no tool, network or native payload use."""
import copy
from pathlib import Path
import sys
import unittest

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent / 'android-native'))
sys.path.append(str(HERE.parent / 'android-current359'))
import rich_origins as current


class CurrentRichOriginTests(unittest.TestCase):
    def test_finite_current_sources_keep_original_bodies_metadata_and_history_strict(self):
        catalog, _, _ = current.historical.approved(HERE)
        policy = current.load_policy(HERE)
        rows = {}
        for config in catalog['configurations']:
            for row in config['artifacts']:
                coordinate = ':'.join(row['id'][k] for k in ('group', 'module', 'version'))
                rows[coordinate, row['filename']] = {'coordinate': coordinate, 'filename': row['filename'],
                    'bytes': row['bytes'], 'sha256': row['sha256'], 'observed_sources': [row['origin']]}
        for row in current.historical.metadata_sources(catalog):
            rows[current._key(row)] = {k: row[k] for k in ('coordinate', 'filename', 'bytes', 'sha256')}
            rows[current._key(row)]['observed_sources'] = [row['origin']]
        for rule in policy['source_alternatives']:
            rows[current._key(rule)]['observed_sources'] = [rule['allowed_exact_sources'][-1]]
        for rule in policy['metadata_additions']:
            rows[current._key(rule)] = {k: rule[k] for k in ('coordinate', 'filename', 'bytes', 'sha256')}
            rows[current._key(rule)]['observed_sources'] = [rule['allowed_exact_sources'][-1]]
        actual = {'all_actual_artifact_sources_accounted': True, 'artifacts': list(rows.values())}
        before_catalog, before_actual = copy.deepcopy(catalog), copy.deepcopy(actual)
        selected, admissions = current.verify_origin_rows(catalog, actual, policy)
        self.assertEqual((len(selected), len(admissions)), (18, 6))
        self.assertEqual(catalog, before_catalog)
        self.assertEqual(actual, before_actual)
        central = copy.deepcopy(actual)
        central_rows = {current._key(r): r for r in central['artifacts']}
        for rule in policy['source_alternatives']:
            central_rows[current._key(rule)]['observed_sources'] = [rule['historical_origin']]
        for rule in policy['metadata_additions']:
            central_rows[current._key(rule)]['observed_sources'] = [rule['allowed_exact_sources'][0]]
        self.assertEqual(len(current.verify_origin_rows(catalog, central, policy)[0]), 18)
        with self.assertRaises(ValueError):
            current.historical.verify_origin_rows(catalog, actual)
        alias_key = current._key(policy['source_alternatives'][0])
        metadata_key = current._key(policy['metadata_additions'][0])
        for case in ('bad-body', 'new-host', 'new-path', 'two-sources', 'bad-metadata', 'extra-metadata', 'missing-mandatory', 'unaccounted', 'wrong-policy'):
            value, bound = copy.deepcopy(actual), copy.deepcopy(policy)
            index = {current._key(r): r for r in value['artifacts']}
            if case == 'bad-body': index[alias_key]['sha256'] = '0' * 64
            elif case == 'new-host': index[alias_key]['observed_sources'] = ['https://unreviewed.invalid/artifact.jar']
            elif case == 'new-path': index[alias_key]['observed_sources'][0] += '.other'
            elif case == 'two-sources': index[alias_key]['observed_sources'] = list(policy['source_alternatives'][0]['allowed_exact_sources'])
            elif case == 'bad-metadata': index[metadata_key]['bytes'] += 1
            elif case == 'extra-metadata':
                value['artifacts'].append({'coordinate': alias_key[0], 'filename': 'unlisted.module', 'bytes': 1,
                    'sha256': '0' * 64, 'observed_sources': ['https://repo.maven.apache.org/maven2/unlisted.module']})
            elif case == 'missing-mandatory':
                value['artifacts'] = [r for r in value['artifacts'] if current._key(r) != ('org.jetbrains.kotlin:kotlin-gradle-plugin:2.4.0','kotlin-gradle-plugin-2.4.0.module')]
            elif case == 'unaccounted': value['all_actual_artifact_sources_accounted'] = False
            else: bound['source_alternatives'].pop()
            with self.subTest(case=case), self.assertRaises(ValueError):
                current.verify_origin_rows(catalog, value, bound)


if __name__ == '__main__':
    unittest.main()
