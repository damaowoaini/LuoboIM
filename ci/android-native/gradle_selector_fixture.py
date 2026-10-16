"""Prepare and check tiny local Gradle cases; never start Gradle on import."""
import hashlib
import io
import json
import os
from pathlib import Path
import stat
import zipfile

import support as s

JITPACK = '039a35aefab7747c557242fa216c9ea11743b604'
OUTPUTS = ('gradle-resolved-artifacts.json', 'gradle-resolved-selectors.json', 'gradle-checked-projects.json')
CASES = (
    ('project-snapshot', 'project', 'fixture.synthetic.plugin', 'plugin', '1.0-SNAPSHOT', None),
    ('same-name-external-snapshot', 'module', 'fixture.synthetic.plugin', 'plugin', '1.0-SNAPSHOT', 'dynamic dependency version refused'),
    ('fixed-module', 'module', 'fixture.synthetic', 'fixed', '1.0', None),
    ('transitive-snapshot', 'transitive', 'fixture.synthetic', 'child', '1.0-SNAPSHOT', 'dynamic dependency version refused'),
    ('external-plus', 'module', 'fixture.synthetic', 'fixed', '+', 'dynamic dependency version refused'),
    ('external-latest', 'module', 'fixture.synthetic', 'fixed', 'latest.release', 'dynamic dependency version refused'),
    ('external-range', 'module', 'fixture.synthetic', 'fixed', '[1.0,2.0)', 'dynamic dependency version refused'),
    ('jitpack-exact', 'module', 'com.github.davidliu', 'audioswitch', JITPACK, None),
    ('jitpack-wrong', 'module', 'com.github.davidliu', 'audioswitch', 'wrong-reviewed-commit', 'JitPack source commit differs'),
)


def jar_bytes():
    out = io.BytesIO()
    with zipfile.ZipFile(out, 'w', compression=zipfile.ZIP_STORED) as archive:
        entry = zipfile.ZipInfo('META-INF/MANIFEST.MF', date_time=(2026, 1, 1, 0, 0, 0))
        archive.writestr(entry, b'Manifest-Version: 1.0\nSynthetic-Fixture: no Java compilation\n\n')
    return out.getvalue()


def module(repository, group, name, version, dependency=None):
    root = repository / group.replace('.', '/') / name / version
    body = jar_bytes()
    s.put(root / (name + '-' + version + '.jar'), body)
    extra = ''
    if dependency is not None:
        g, n, v = dependency
        extra = '<dependencies><dependency><groupId>' + g + '</groupId><artifactId>' + n + \
            '</artifactId><version>' + v + '</version></dependency></dependencies>'
    pom = '<project><modelVersion>4.0.0</modelVersion><groupId>' + group + '</groupId><artifactId>' + name + \
        '</artifactId><version>' + version + '</version>' + extra + '</project>\n'
    s.put(root / (name + '-' + version + '.pom'), pom.encode())
    return hashlib.sha256(body).hexdigest()


def prepare(work):
    """O_EXCL inputs under a new task root; no command/tool/network calls."""
    work = Path(work)
    root = work / 'evidence/selector-controls'
    if root.exists() or root.is_symlink():
        raise ValueError('new selector-controls root required; no partial resume')
    s.mkdir(root)
    result = []
    for label, kind, group, name, version, rejection in CASES:
        case = root / label
        s.mkdir(case / 'evidence')
        settings = "rootProject.name = 'selector-control'\ninclude ':plugin'\n"
        if kind == 'project':
            # A real empty Gradle project may legitimately lack its default
            # build script; this differs from a missing custom buildFile.
            s.mkdir(case / 'empty')
            settings += "include ':empty'\n"
        s.put(case / 'settings.gradle', settings.encode())
        plugin = "plugins { id 'java-library' }\ngroup = 'fixture.synthetic.plugin'\nversion = '1.0-SNAPSHOT'\n"
        s.put(case / 'plugin/build.gradle', plugin.encode())
        repo = case / 'm2'
        expected_sha = module(repo, 'fixture.synthetic', 'fixed', '1.0')
        module(repo, 'fixture.synthetic.plugin', 'plugin', '1.0-SNAPSHOT')
        module(repo, 'fixture.synthetic', 'child', '1.0-SNAPSHOT')
        module(repo, 'fixture.synthetic', 'parent', '1.0', ('fixture.synthetic', 'child', '1.0-SNAPSHOT'))
        module(repo, 'com.github.davidliu', 'audioswitch', JITPACK)
        module(repo, 'com.github.davidliu', 'audioswitch', 'wrong-reviewed-commit')
        # These make the counterfactual unguarded dynamic requests resolvable
        # from the private local repository too; no remote Maven is configured.
        s.put(repo / 'fixture/synthetic/fixed/maven-metadata.xml',
            b'<metadata><groupId>fixture.synthetic</groupId><artifactId>fixed</artifactId><versioning>'
            b'<latest>1.0</latest><release>1.0</release><versions><version>1.0</version></versions>'
            b'</versioning></metadata>\n')
        dependency = "api project(':plugin')" if kind == 'project' else \
            'api ' + json.dumps('fixture.synthetic:parent:1.0' if kind == 'transitive' else group + ':' + name + ':' + version)
        script = "plugins { id 'java-library' }\ngroup = 'fixture.synthetic.app'\nversion = '1.0'\n" + \
            "repositories { maven { url = uri('m2') } }\ndependencies { " + dependency + " }\n"
        s.put(case / 'build.gradle', script.encode())
        s.put(case / 'gradle.properties', b'org.gradle.daemon=false\norg.gradle.parallel=false\n')
        row = {'label': label, 'kind': kind, 'group': group, 'module': name, 'version': version,
               'expected_rejection': rejection, 'expected_fixed_artifact_sha256': expected_sha,
               'cwd': str(case)}
        s.write_json(case / 'input.json', row)
        result.append(row)
    s.write_json(root / 'prepared.json', {'state': 'prepared_not_executed', 'cases': result,
        'local_only': True, 'JitPack_coordinate_controls_not_network_or_IAM': True})
    return result


def capture_outputs(work, case):
    """Move only this invocation's new JSON, keeping originals and free slots.

    The resolver retains its one production output directory. Each fixture's
    files are renamed intact before the next fixture and the real app phase.
    No preexisting/evidenced output is overwritten or silently discarded.
    """
    work, case = Path(work), Path(case)
    captured = {}
    for name in OUTPUTS:
        source, target = work / 'evidence' / name, case / 'evidence' / name
        if not source.exists() and not source.is_symlink():
            continue
        info = source.lstat()
        if (source.resolve(strict=True) != source or not stat.S_ISREG(info.st_mode)
                or info.st_uid != os.getuid() or info.st_nlink != 1
                or target.exists() or target.is_symlink()):
            raise ValueError('fresh owned regular selector output required')
        row = {'path': str(target.relative_to(work)), 'bytes': info.st_size, 'sha256': s.sha(source)}
        os.rename(source, target)
        s.sync(source.parent)
        s.sync(target.parent)
        if s.sha(target) != row['sha256'] or target.stat().st_size != row['bytes']:
            raise ValueError('moved selector output differs')
        captured[name] = row
    return captured


def check(case, log, captured):
    """Inspect actual Gradle output; source generation alone is never PASS."""
    requested = []
    text = Path(log).read_text(errors='replace')
    for line in text.splitlines():
        marker = 'LUOBO_SELECTOR_REQUEST '
        if marker in line:
            requested.append(json.loads(line.split(marker, 1)[1])['requested'])
    if case['expected_rejection']:
        # Command exit/rejection is separately checked by the real support.run.
        if case['expected_rejection'] not in text:
            raise ValueError('negative lacks the exact real resolver rejection')
        if not any(r.get('kind') == 'module' and r.get('group') == case['group']
                and r.get('module') == case['module'] and r.get('version') == case['version'] for r in requested):
            raise ValueError('negative did not reach its real typed module selector')
        if captured:
            raise ValueError('negative unexpectedly completed resolver artifact evidence')
        return {'requested_module_guard_observed': True, 'real_Gradle_rejection_required': True}
    root = Path(case['cwd']) / 'evidence'
    if set(captured) != set(OUTPUTS):
        raise ValueError('positive lacks complete artifacts, selections and project ownership')
    artifacts = json.loads((root / OUTPUTS[0]).read_text())
    selections = json.loads((root / OUTPUTS[1]).read_text())
    projects = json.loads((root / OUTPUTS[2]).read_text())
    if not projects or not all(p.get('canonicalOwned') is True for p in projects):
        raise ValueError('positive lacks checked canonical owned project sources')
    if case['kind'] == 'project':
        if (not any(r.get('kind') == 'project' and r.get('projectPath') == ':plugin' for r in requested)
                or not any(r['requested'].get('kind') == 'project' and r['selected'].get('kind') == 'project'
                           and r['selected'].get('projectPath') == ':plugin' for r in selections)
                or not any(r.get('selected', {}).get('kind') == 'project'
                           and r.get('deferred_to_actual_APK_compile') is True for r in artifacts)
                or not any(p['project'] == ':empty' and p['buildFileExists'] is False
                           and p['defaultBuildScriptMissing'] is True for p in projects)):
            raise ValueError('positive lacks actual project selector and selected component')
    else:
        def matches(row):
            item = row.get('selected', {})
            return (item.get('kind') == 'module' and item.get('group') == case['group']
                    and item.get('module') == case['module'] and item.get('version') == case['version'])
        if (not any(matches(r) for r in selections)
                or not any(matches(r) and r.get('sha256') == case['expected_fixed_artifact_sha256'] for r in artifacts)):
            raise ValueError('positive lacks fixed actual module and original artifact SHA')
    return {'actual_selected_identity_and_artifact_checked': True, 'real_Gradle_success_required': True}
