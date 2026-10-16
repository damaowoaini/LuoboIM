"""Owned multi-artifact project controls for the real pinned Gradle; no runner."""
import json
from pathlib import Path

import gradle_selector_fixture as original
import gradle_local_file_fixture as local_files
import support as s

AMBIGUITY = 'cannot choose between the following variants'
CASES = ('project-multi-legacy', 'project-multi-module-view', 'project-multi-transitive-snapshot')


def prepare(work):
    work = Path(work)
    root = work / 'evidence/selector-controls'
    if root.is_symlink() or root.resolve(strict=True) != root or not root.is_dir():
        raise ValueError('original owned controls root must already exist')
    result = []
    for label in CASES:
        case = root / label
        if case.exists() or case.is_symlink():
            raise ValueError('fresh multi-variant case only; no partial resume')
        s.mkdir(case / 'evidence')
        s.mkdir(case / 'empty')
        s.put(case / 'settings.gradle',
              b"rootProject.name = 'multi-artifact-control'\ninclude ':plugin', ':empty'\n")
        fixed_sha = original.module(case / 'm2', 'fixture.synthetic', 'fixed', '1.0')
        original.module(case / 'm2', 'fixture.synthetic', 'child', '1.0-SNAPSHOT')
        original.module(case / 'm2', 'fixture.synthetic', 'parent', '1.0',
                        ('fixture.synthetic', 'child', '1.0-SNAPSHOT'))
        dependency = 'fixture.synthetic:parent:1.0' if label == CASES[2] else 'fixture.synthetic:fixed:1.0'
        # No artifacts on the parent configuration: official publications API
        # only creates an implicit variant when parent artifacts exist. These
        # two secondary variants differ solely by artifactType, like the real
        # AGP failure. The graph still has one selected project configuration.
        plugin = """import org.gradle.api.attributes.Usage
import org.gradle.api.attributes.Category
import org.gradle.api.artifacts.type.ArtifactTypeDefinition
group = 'fixture.synthetic.plugin'
version = '1.0-SNAPSHOT'
repositories { maven { url = uri('../m2') } }
def dependencyScope = configurations.create('fixtureDependencies') {
    canBeResolved = false; canBeConsumed = false
}
['fixtureApiElements': Usage.JAVA_API, 'fixtureRuntimeElements': Usage.JAVA_RUNTIME].each { name, usage ->
    configurations.create(name) {
        canBeResolved = false; canBeConsumed = true
        extendsFrom dependencyScope
        attributes {
            attribute(Usage.USAGE_ATTRIBUTE, objects.named(Usage, usage))
            attribute(Category.CATEGORY_ATTRIBUTE, objects.named(Category, Category.LIBRARY))
        }
        outgoing.variants.create('first') {
            attributes.attribute(ArtifactTypeDefinition.ARTIFACT_TYPE_ATTRIBUTE, 'fixture-first')
            artifact(file('first.jar'))
        }
        outgoing.variants.create('second') {
            attributes.attribute(ArtifactTypeDefinition.ARTIFACT_TYPE_ATTRIBUTE, 'fixture-second')
            artifact(file('second.jar'))
        }
    }
}
dependencies { fixtureDependencies %s }
""" % json.dumps(dependency)
        s.put(case / 'plugin/build.gradle', plugin.encode())
        s.put(case / 'plugin/first.jar', original.jar_bytes())
        s.put(case / 'plugin/second.jar', original.jar_bytes())
        # Separate process/case from the passing view: an intentionally failing
        # legacy artifact query cannot poison the positive configuration state.
        script = """plugins { id 'java-library' }
repositories { maven { url = uri('m2') } }
dependencies { api project(':plugin') }
tasks.register('legacyArtifactProbe') {
    doLast {
        configurations.compileClasspath.resolvedConfiguration.resolvedArtifacts
        throw new GradleException('legacy artifact probe unexpectedly succeeded')
    }
}
"""
        s.put(case / 'build.gradle', script.encode())
        s.put(case / 'gradle.properties', b'org.gradle.daemon=false\norg.gradle.parallel=false\n')
        row = {'label': label, 'cwd': str(case), 'variant_contract': True,
               'kind': 'project', 'group': 'fixture.synthetic.plugin', 'module': 'plugin',
               'version': '1.0-SNAPSHOT', 'expected_fixed_artifact_sha256': fixed_sha,
               'task': 'legacyArtifactProbe' if label == CASES[0] else 'dependencies',
               'expected_rejection': AMBIGUITY if label == CASES[0] else None}
        if label == CASES[2]:
            row.update(kind='transitive', group='fixture.synthetic', module='child',
                       expected_rejection='dynamic dependency version refused')
        s.write_json(case / 'input.json', row)
        result.append(row)
    s.write_json(root / 'prepared-variants.json', {'state': 'prepared_not_executed',
        'cases': result, 'original9_unchanged': True, 'Gradle_processes': 3,
        'actual_Gradle_or_AGP_not_yet_executed': True})
    return result + local_files.prepare(work)


def check(case, log, captured):
    if case.get('local_file_contract'):
        return local_files.check(case, log, captured)
    if case['label'] == CASES[0]:
        text = Path(log).read_text(errors='replace')
        if AMBIGUITY not in text or 'compileClasspath' not in text or captured:
            raise ValueError('legacy counterfactual must fail with real artifact ambiguity')
        requests = [json.loads(line.split('LUOBO_SELECTOR_REQUEST ', 1)[1])['requested']
                    for line in text.splitlines() if 'LUOBO_SELECTOR_REQUEST ' in line]
        if not any(x.get('kind') == 'project' and x.get('projectPath') == ':plugin' for x in requests):
            raise ValueError('legacy counterfactual did not reach real typed project selector')
        return {'legacy_real_project_artifact_ambiguity_observed': True}
    result = original.check(case, log, captured)
    if case['label'] == CASES[1]:
        rows = json.loads((Path(case['cwd']) / 'evidence' / original.OUTPUTS[0]).read_text())
        if not any(x.get('selected', {}).get('kind') == 'module'
                   and x['selected'].get('group') == 'fixture.synthetic'
                   and x['selected'].get('module') == 'fixed'
                   and x['selected'].get('version') == '1.0'
                   and x.get('sha256') == case['expected_fixed_artifact_sha256'] for x in rows):
            raise ValueError('module artifact below excluded project must still be hashed')
        result['fixed_module_below_multi_artifact_project_verified'] = True
    return result
