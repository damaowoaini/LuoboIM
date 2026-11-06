"""One explicit current363 producer; no old354 identity or fresh-bind fallback."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path

import audit
import support as s
import verify_inputs

SPEC_SHA256 = '4a851bdcb4074f63eac4d65baccaa53a592d078317c8b99b4ebcd6475a7f0a1b'


def encoded(value):
    return (json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':')) + '\n').encode()


def validate(spec, context, inputs, tools):
    if hashlib.sha256(encoded(spec)).hexdigest() != SPEC_SHA256:
        raise ValueError('exact Root frozen current363 producer binding required')
    if (spec['source'] != {'members': inputs['members'], 'digest': inputs['source_digest'], 'ref': context['source_ref']}
            or context['core_build_mode'] != 'restore-verified-current363' or context['restore_fallback'] is not False
            or context['cohort'] != spec['cohort'] or context['core_compatibility']['nonbusiness176_digest'] != spec['nonbusiness176_digest']
            or spec['source']['members'] != 539 or spec['onlyDebugQA_ARM64'] is not True
            or spec['coreBindAgain'] is not False or spec['productionReplacement'] is not False):
        raise ValueError('explicit same-source539/native176 debug-only restore required')
    if (spec['producerMobileTool']['version'] != tools['mobile']['version']
            or spec['producerMobileTool']['commit'] != tools['mobile']['commit']):
        raise ValueError('original fixed mobile tool identity differs')


def pinned(root, row):
    p = verify_inputs.checked_path(root, row['path'])
    if p.stat().st_size != row['bytes'] or s.sha(p) != row['sha256']:
        raise ValueError('frozen producer body differs: ' + row['path'])
    return p


def product_files(root, spec):
    expected = {r['path'] for r in spec['product_pins']}
    actual = {str(p.relative_to(root)) for p in root.rglob('*') if p.is_file() or p.is_symlink()}
    if actual != expected:
        raise ValueError('complete exact five producer products required')
    for row in spec['product_pins']:
        pinned(root, row)


def audit_metadata(raw, products, spec, inputs, tools):
    # Whole raw ZIP hash binds every member; these fourteen critical records are
    # also individually frozen. The missing top-level activation is not invented.
    for row in spec['raw_markers']:
        pinned(raw, row)
    load = lambda name: json.loads(verify_inputs.checked_path(raw, 'evidence/' + name).read_bytes())
    source, built = load('current363-source.json'), load('current363-core-build.json')
    if (source['members'] != 539 or source['App_members'] != 363
            or source['source_ref'] != spec['source']['ref'] or source['source_digest'] != inputs['source_digest']
            or source['cohort'] != spec['producer_cohort'] or source['old354_source_not_relabelled'] is not True
            or built['cohort'] != spec['producer_cohort'] or built['source_members'] != 539 or built['App_members'] != 363
            or built['source_ref'] != spec['source']['ref'] or built['source_digest'] != inputs['source_digest']
            or built['fresh_Go_bind_executed'] is not True or built['restored_binary'] is not False
            or built['only_debug_QA'] is not True or built['production_replacement'] is not False
            or built['AAR_sha256'] != spec['AARSHA256']
            or built['build_receipt_sha256'] != next(r['sha256'] for r in spec['product_pins'] if r['path'].endswith('.build.json'))):
        raise ValueError('actual producer current363 source and fresh core evidence required')
    for phase in ('setup', 'core', 'preseal'):
        for suffix in ('complete', 'final-resource'):
            value = load(phase + '-' + suffix + '.json')
            if value['cohort'] != spec['producer_cohort'] or value['state'] != 'passed' or value['latched_failure'] is not None:
                raise ValueError('actual complete producer phase/cohort required')
    observation = load('actual-bind-observation-final.json')
    if (observation['actual_core_build_success'] is not True or observation['original_error'] is not None
            or observation['retained_capture_error'] is not None):
        raise ValueError('actual successful observed native bind required')
    for name, row in observation['core_locks'].items():
        pinned(raw, {'path': 'evidence/core-build-locks/' + name, **row})
    lock = load('core-build-locks/acquired-dependency-lock.json')
    if (lock['source_go_sum_sha256'] != tools['locks']['patched_core_go_sum']['sha256']
            or lock['acquired_go_sum_sha256'] != tools['acquired_go_sum']['sha256']
            or lock['old_checksums_preserved'] is not True):
        raise ValueError('original and acquired dependency locks differ')
    tidy = load('bind-observer/tidy-result.json')
    if (tidy['tidy_evidence_complete'] is not True or tidy['child_exit_code'] != 0 or tidy['received_signal'] is not None
            or tidy['real_go_sha256'] != spec['producerGoBinarySHA256']
            or tidy['selected_env']['GOFLAGS'] != '-p=1 -mod=readonly'
            or any(tidy['selected_env'][k] != v for k, v in {'GOOS': 'android', 'GOARCH': 'arm64', 'CGO_ENABLED': '1', 'GOWORK': 'off', 'GOPROXY': 'off', 'GOSUMDB': 'off'}.items())):
        raise ValueError('actual generated tidy/tool/readonly target proof differs')
    if load('mobile-tools.json') != spec['producerMobileTool']:
        raise ValueError('original gobind/gomobile executable provenance differs')
    for label in ('core-build', 'actual-generated-verify', 'actual-generated-graph'):
        value = load(label + '.resource.json')
        if (value['exit_code'] != 0 or value['expectation_met'] is not True or value['latched_failure'] is not None
                or value['error'] is not None or s.sha(verify_inputs.checked_path(raw, 'evidence/' + label + '.log')) != value['log_sha256']):
            raise ValueError('original bind and generated readonly verification/graph proof differs')
    if load('job-CA.json')['public_SHA256'] != spec['publicCoreCASHA256']:
        raise ValueError('original public core CA provenance differs')
    product_files(products, spec)
    aar = products / 'core-current363-arm64.aar'
    meta = json.loads(aar.with_suffix('.build.json').read_bytes())
    if (meta.get('readonly_dependency_mode') is not True or meta.get('acquired_dependency_lock') != lock
            or s.sha(products / 'job-ca-public.pem') != spec['publicCoreCASHA256']):
        raise ValueError('original full AAR manifest/lock/public CA required')
    proof = audit.core(aar, meta, spec['publicCoreCASHA256'])
    proof['binding_API'] = audit.bindings(products / 'core-current363-arm64-sources.jar', json.loads((Path(__file__).resolve().parents[1] / 'android-native/bindings.json').read_bytes()))
    if proof != load('core-audit.json'):
        raise ValueError('actual recovered AAR/ARM64 ELF/API/callback audit differs')
    return proof


def restore(owner, spec):
    validate(spec, owner.context, owner.inputs, owner.tools)
    owner.verify()
    # Borrow the original bounded exact-API/ZIP/CRC transport, never old354
    # metadata/activation assumptions or a historical binary selection branch.
    path = Path(__file__).resolve().parents[1] / 'apple-sdk-link/custody.py'
    module_spec = importlib.util.spec_from_file_location('current363_exact_artifact_custody', path)
    custody = importlib.util.module_from_spec(module_spec)
    module_spec.loader.exec_module(custody)
    root = owner.w / 'recovered-core'
    custody.mkdir(root)
    token = os.environ.pop('RESTORE_GITHUB_TOKEN', '')
    if not token:
        raise ValueError('ephemeral artifact-read token required only for explicit restore')
    try:
        identity = custody.check_remote(spec, token)
        for artifact in spec['parent']['artifacts']:
            archive = root / (artifact['role'] + '.zip')
            custody.download(artifact, token, archive)
            allowed = {r['path'] for r in spec['product_pins']} if artifact['role'] == 'products' else None
            rows = custody.extract(archive, artifact, root / artifact['role'], allowed)
            if len(rows) != artifact['exact_members'] or sum(r['bytes'] for r in rows) != artifact['exact_decoded_bytes']:
                raise ValueError('exact complete frozen archive member vector required')
            s.write_json(owner.w / 'evidence' / ('import-' + artifact['role'] + '-members.json'), rows)
    finally:
        token = None
    raw, products = root / 'raw', root / 'products'
    proof = audit_metadata(raw, products, spec, owner.inputs, owner.tools)
    for row in spec['product_pins']:
        target = 'recovered-core-ca-public.pem' if row['path'] == 'job-ca-public.pem' else row['path']
        s.put(owner.w / 'artifacts' / target, (products / row['path']).read_bytes())
    for suffix in ('.aar', '.build.json', '-sources.jar'):
        s.put(owner.w / 'artifacts' / ('core-current257-arm64' + suffix), (products / ('core-current363-arm64' + suffix)).read_bytes())
    s.write_json(owner.w / 'evidence/core-audit.json', proof)
    s.write_json(owner.w / 'evidence/current363-core-import.json', {
        'cohort': owner.context['cohort'], 'producer': identity, 'source': spec['source'],
        'native176_digest': spec['nonbusiness176_digest'], 'Root_review_sha256': spec['rootReviewSHA256'],
        'AAR_sha256': spec['AARSHA256'], 'full_API_ZIP_CRC_and_five_bodies_checked': True,
        'actual_AAR_ARM64_ELF_API_callbacks_reaudited': True, 'producer_top_activation_included': False,
        'producer_source_and_phase_cohort_evidence_used': True,
        'Go_bind_executed_in_this_job': False, 'fresh_bind_fallback': False,
        'original_core_CA_sha256': spec['publicCoreCASHA256'],
        'recovered_core_CA_path': 'artifacts/recovered-core-ca-public.pem',
        'new_setup_CA_sha256': s.sha(owner.w / 'job-private/job-ca.pem'),
        'new_setup_CA_installed_in_recovered_core': False, 'old_private_key_obtained': False,
        'only_debug_QA_ARM64': True, 'production_replacement': False,
        'wrapper_compilation_or_APK_accepted': False, 'device_service_or_fixture_TLS_accepted': False})
    owner.verify()
