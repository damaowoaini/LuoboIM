"""New refusal evidence controls only; no Gradle/Go/native execution."""
import copy
import json
import os
from pathlib import Path
import tempfile
import unittest

import rich_consumer as rich
import support as s

HERE = Path(__file__).resolve().parent


def refusal(catalog):
    config = catalog['configurations'][0]
    actual = [{k: v for k, v in a.items() if k != 'origin'} for a in copy.deepcopy(config['artifacts'])]
    actual[0]['sha256'] = '0'*64
    return dict(kind='rich-artifact-refusal-v1', accepted=False,
                catalog_sha256=rich.CATALOG_SHA, project=catalog['scope']['project'],
                configuration=config['name'], scope_sources=catalog['scope']['sources'],
                no_force_or_substitution=True, edges=config['edges'],
                expected_artifacts=[{k: v for k, v in a.items() if k != 'origin'} for a in config['artifacts']],
                actual_artifacts=actual)


PRODUCTION_RESOLVER_SHA256 = '85e79a582a84898711e72f974da6cdc08f34129b7b6fb6c80f58c2f60c10add3'


def production_writer(here):
    body=rich.read(Path(here)/'resolve-observed.gradle')
    if rich.sha(body)!=PRODUCTION_RESOLVER_SHA256:
        raise ValueError('whole production resolver source differs')
    text=body.decode('utf-8'); start,end='def richPersist =','def richWrite ='
    if text.count(start)!=1 or text.count(end)!=1 or text.index(start)>=text.index(end):
        raise ValueError('unique production writer markers required')
    block=text[text.index(start):text.index(end)].encode('utf-8')
    return body,dict(path='production-resolver.gradle',bytes=len(body),
                     sha256=PRODUCTION_RESOLVER_SHA256,block_sha256=rich.sha(block))


def prepare_actual_controls(work, here):
    """Two isolated owned models consume the same whole-pinned production writer."""
    import rich_fixture
    body,pin=production_writer(here)
    selected=[]
    for label in ['positive','changed-body']:
        owner=Path(work)/('writer-control-'+label)
        s.mkdir(owner)
        cases=rich_fixture.prepare(owner, here)
        case=next(c for c in cases if c['label']==label)
        root=Path(case['cwd'])
        s.put(root/'production-resolver.gradle',body)
        s.write_json(root/'production-writer-pin.json',pin)
        s.put(root/'refusal-init.gradle',rich.read(Path(here)/'rich-refusal-fixture.gradle'))
        selected.append(dict(case,owned=str(owner),production_writer_pin=pin,
            expected_consumed_root=str(owner/'evidence/rich-consumed/metadata'),
            expected_refusal_root=str(owner/'evidence/rich-refused/metadata')))
    return selected


def check_actual_control(case, log, exit_code):
    """Read the real closure's output; command launch is a separate Root action."""
    owner=Path(case['owned']); root=Path(case['cwd'])
    config=json.loads(rich.read(root/'control-catalog.json'))
    body,pin=production_writer(HERE)
    if rich.read(root/'production-resolver.gradle')!=body or case['production_writer_pin']!=pin:
        raise ValueError('production writer source after actual control differs')
    consumed=owner/'evidence/rich-consumed/metadata'; refused=owner/'evidence/rich-refused/metadata'
    def checked_output(file):
        folder=file.parent
        if (folder.resolve(strict=True)!=folder or folder.stat().st_uid!=os.getuid()
                or folder.stat().st_mode & 0o077):
            raise ValueError('private canonical production report directory required')
        value=json.loads(rich.read(file,128*1024))
        if file.stat().st_mode & 0o077:
            raise ValueError('private production report file required')
        return value
    if case['label']=='positive':
        if exit_code!=0 or refused.exists():
            raise ValueError('positive production writer control failed')
        files=sorted(consumed.iterdir())
        if [p.name for p in files]!=sorted(c['name']+'.json' for c in config['configurations']):
            raise ValueError('two production consumed reports required')
        for file in files:
            value=checked_output(file)
            expected=next(c for c in config['configurations'] if c['name']==value.get('configuration'))
            if (value.get('catalog_sha256')!='synthetic-control-only'
                    or value.get('project')!=config['scope']['project']
                    or value.get('scope_sources')!=config['scope']['sources']
                    or value.get('no_force_or_substitution') is not True
                    or sorted(map(rich.canonical,value['edges']))!=sorted(map(rich.canonical,expected['edges']))
                    or sorted(map(rich.canonical,value['artifacts']))!=sorted(map(rich.canonical,expected['artifacts']))):
                raise ValueError('actual positive full synthetic projection differs')
    else:
        if exit_code==0 or case['expected_rejection'] not in log or consumed.exists():
            raise ValueError('original changed-body refusal/nonpromotion required')
        completion=('LUOBO_RICH_WRITER_COMPLETED writer_sha256=63f1330c16da061fbd41a390887bd7fb51db75a06d24724524c1c03e8d6b0e8b '
                    'source_sha256='+PRODUCTION_RESOLVER_SHA256+' configuration='+config['configurations'][0]['name'])
        markers=[line for line in log.splitlines() if line.startswith('LUOBO_RICH_WRITER_COMPLETED ')]
        if markers!=[completion]:
            raise ValueError('one exact post-return production writer completion marker required')
        file=refused/(config['configurations'][0]['name']+'.json')
        if list(refused.iterdir())!=[file]:
            raise ValueError('one bounded production refusal report required')
        value=checked_output(file)
        if value.get('kind')!='rich-artifact-refusal-v1' or value.get('accepted') is not False:
            raise ValueError('actual production refusal shape required')
        expected=config['configurations'][0]
        actual=copy.deepcopy(expected['artifacts'])
        changed=next(a for a in actual if a['id']['module']=='kotlin-build-tools-impl')
        file=root/'m2/org/jetbrains/kotlin/kotlin-build-tools-impl/2.4.21/kotlin-build-tools-impl-2.4.21.jar'
        jar_body=rich.read(file)
        changed.update(bytes=len(jar_body),sha256=rich.sha(jar_body))
        if (value.get('catalog_sha256')!='synthetic-control-only'
                or value.get('project')!=config['scope']['project']
                or value.get('scope_sources')!=config['scope']['sources']
                or value.get('no_force_or_substitution') is not True
                or sorted(map(rich.canonical,value['edges']))!=sorted(map(rich.canonical,expected['edges']))
                or sorted(map(rich.canonical,value['expected_artifacts']))!=sorted(map(rich.canonical,expected['artifacts']))
                or sorted(map(rich.canonical,value['actual_artifacts']))!=sorted(map(rich.canonical,actual))):
            raise ValueError('actual changed-body evidence differs')
    return dict(case=case['label'],actual_production_closure=True,whole_source_sha256=pin['sha256'],
                writer_block_sha256=pin['block_sha256'],file_and_directory_force_calls_completed=True,
                power_loss_recovery_simulated=False)


class RichRefusalEvidence(unittest.TestCase):
    def test_actual_refusal_identifies_body_and_variant_fields_without_enrolling_them(self):
        catalog, _, _ = rich.approved(HERE)
        value = refusal(catalog)
        before = copy.deepcopy(catalog)
        result = rich.inspect_refusal(catalog, value)
        self.assertEqual(result['differences'][0]['fields'], ['sha256'])
        value['actual_artifacts'][0]['variant']['attributes']['unexpected'] = {'type': 'java.lang.String', 'value': 'different'}
        result = rich.inspect_refusal(catalog, value)
        self.assertEqual(result['differences'][0]['fields'], ['sha256', 'variant'])
        self.assertEqual(catalog, before)
        self.assertFalse(result['accepted'])
        with self.assertRaises(ValueError):
            rich.validate_report(catalog, dict(value, artifacts=value['actual_artifacts']))

    def test_refusal_never_satisfies_success_reports_and_wrong_scope_is_rejected(self):
        catalog, _, _ = rich.approved(HERE)
        value = refusal(catalog)
        with tempfile.TemporaryDirectory() as name:
            root = Path(name).resolve()
            s.write_json(root/'evidence/rich-refused/metadata'/(value['configuration']+'.json'), value)
            with self.assertRaisesRegex(ValueError, 'complete actual'):
                rich.reports(root, HERE, 'metadata', True)
        for field, replacement in [('accepted', True), ('catalog_sha256', '0'*64), ('configuration', 'other')]:
            bad = copy.deepcopy(value); bad[field] = replacement
            with self.assertRaises(ValueError): rich.inspect_refusal(catalog, bad)


if __name__ == '__main__':
    unittest.main()
