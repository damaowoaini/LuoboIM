"""Current363 exact acquisition sources; historical R12 admission stays frozen."""
import copy
import json
from pathlib import Path

import rich_current as historical

POLICY_SHA256 = 'df0e32ffb1580ae7514d215754a7b46e4e6a1b31b8dd35f331b5cea652701d39'


def encoded(value):
    return (json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':')) + '\n').encode()


def load_policy(here):
    raw = historical.read(Path(here) / 'rich-origin-policy.json')
    if historical.sha(raw) != POLICY_SHA256:
        raise ValueError('exact current363 origin policy required')
    return json.loads(raw)


def validate_scope(policy, context, inputs):
    expected = {'cohort': context['cohort'], 'members': inputs['members'],
        'source_digest': inputs['source_digest'], 'source_ref': context['source_ref']}
    if historical.sha(encoded(policy)) != POLICY_SHA256 or policy['scope'] != expected:
        raise ValueError('exact current363 source/cohort origin policy required')


def _key(row):
    return row['coordinate'], row['filename']


def _artifact_key(row):
    return ':'.join(row['id'][k] for k in ('group', 'module', 'version')), row['filename']


def verify_origin_rows(catalog, actual, policy):
    """Pure metadata comparison; raw origin rows are never normalized or edited."""
    if (historical.sha(encoded(policy)) != POLICY_SHA256
            or policy['template_catalog_sha256'] != historical.CATALOG_SHA
            or actual.get('all_actual_artifact_sources_accounted') is not True):
        raise ValueError('frozen policy and complete original strong origin audit required')
    observed = {}
    for row in actual['artifacts']:
        key = _key(row)
        if key in observed and observed[key] != row:
            raise ValueError('ambiguous actual Maven origin')
        observed[key] = row
    # This is a current-cohort expected-source projection, never a rewrite of
    # the accepted template or a claim that its historical URL check passed.
    projected = copy.deepcopy(catalog)
    base_artifacts = {_artifact_key(row): row for c in projected['configurations'] for row in c['artifacts']}
    base_metadata = {_key(row): row for row in projected['metadata_sources']}
    admissions = []

    def observe(rule, expected, *, addition):
        row = observed.get(_key(rule))
        if row is None:
            return None  # Historical selected/mandatory metadata checks follow.
        if any(row[k] != rule[k] for k in ('bytes', 'sha256')):
            raise ValueError('exact current policy artifact/metadata body differs')
        sources = row['observed_sources']
        if (not isinstance(sources, list) or len(sources) != 1
                or sources[0] not in rule['allowed_exact_sources']):
            raise ValueError('single exact reviewed current source required')
        expected['origin'] = sources[0]
        admissions.append({'coordinate': rule['coordinate'], 'filename': rule['filename'],
            'bytes': row['bytes'], 'sha256': row['sha256'], 'observed_sources': list(sources),
            'kind': 'fixed-published-metadata-addition' if addition else 'exact-source-alternative',
            'historical_expected_origin': None if addition else rule['historical_origin']})
        return expected

    for rule in policy['source_alternatives']:
        rows = base_artifacts if rule['kind'] == 'artifact' else base_metadata
        expected = rows.get(_key(rule))
        if (expected is None or any(expected[k] != rule[k] for k in ('bytes', 'sha256'))
                or expected['origin'] != rule['historical_origin']):
            raise ValueError('current source rule does not match original pinned template body')
        observe(rule, expected, addition=False)
    for rule in policy['metadata_additions']:
        if _key(rule) in base_metadata or _key(rule) in base_artifacts:
            raise ValueError('new metadata cannot replace an original expected row')
        expected = {k: rule[k] for k in ('coordinate', 'filename', 'bytes', 'sha256')}
        if observe(rule, expected, addition=True) is not None:
            projected['metadata_sources'].append(expected)
    # Original byte/SHA/unique-origin checks for all18 and every present selected
    # metadata row, all selected components, and the original three mandatory
    # generator/BC parent rows remain mandatory and run without monkeypatching.
    selected = historical.verify_origin_rows(projected, actual)
    return selected, sorted(admissions, key=_key)


def _binding(work, here):
    work = Path(work)
    policy = load_policy(here)
    catalog, _, pins = historical.reports(work, here, 'metadata', True)
    raw = historical.read(work / 'evidence/dependency-origin-audit.json')
    selected, admissions = verify_origin_rows(catalog, json.loads(raw), policy)
    tooling = historical.kgp_owner_model.require_report(work, here, catalog, 'metadata', historical.read)
    return {'schema': 1, 'kind': 'current363-exact-rich-origin-binding-v1',
        'template_catalog_sha256': historical.CATALOG_SHA,
        'current_origin_policy_sha256': POLICY_SHA256,
        'actual_origin_receipt_sha256': historical.sha(raw), 'actual_metadata_reports': pins,
        'checked_artifact_rows': len(selected), 'explicit_source_admissions': admissions,
        'actual_publishing_tooling_owner': tooling, 'runtime_or_App_claim': False}


def bind_origins(work, here, write_json):
    value = _binding(work, here)
    write_json(Path(work) / 'evidence/rich-origin-binding.json', value)


def require_origin_binding(work, here):
    expected = _binding(work, here)
    actual = json.loads(historical.read(Path(work) / 'evidence/rich-origin-binding.json'))
    if actual != expected:
        raise ValueError('current source policy/origin/reports/tooling binding changed')


def check_apk_reports(work, here):
    require_origin_binding(work, here)
    catalog, values, _ = historical.reports(work, here, 'apk', False)
    historical.kgp_owner_model.require_report(work, here, catalog, 'apk', historical.read)
    return [v['configuration'] for v in values]
