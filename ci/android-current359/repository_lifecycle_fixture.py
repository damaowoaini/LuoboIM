"""Prepare one offline Gradle lifecycle control using the real resolver policy.

No Gradle/tool/network dispatch occurs in this module or prepare().
"""
import hashlib
import io
import json
import os
from pathlib import Path
import stat
import zipfile

RESOLVER_SHA = '00e49cc73c2831c825a43f392145ec7b974518414e2f7adb759324e17b3519cd'
POLICY_SHA = 'b61142ead7c11a64a5a1d943d8ac16b239b781b88e2d73f482986d8df313258b'
START = '// BEGIN strict repository lifecycle.\n'
END = '// END strict repository lifecycle.\n'
MARKER = 'LUOBO_REPOSITORY_LIFECYCLE_COMPLETED '


def policy_bytes(path):
    path = Path(path)
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as source:
        before = os.fstat(source.fileno())
        if (path.resolve() != path or not stat.S_ISREG(before.st_mode)
                or before.st_uid != os.getuid() or before.st_nlink != 1
                or before.st_size > 128 * 1024):
            raise ValueError('exact regular resolver source required')
        body = source.read(128 * 1024 + 1)
        after = os.fstat(source.fileno())
    identity = lambda value: (value.st_dev, value.st_ino, value.st_uid, value.st_mode,
                              value.st_nlink, value.st_size, value.st_mtime_ns, value.st_ctime_ns)
    if (identity(before) != identity(after) or identity(after) != identity(path.lstat())
            or hashlib.sha256(body).hexdigest() != RESOLVER_SHA):
        raise ValueError('whole resolver source SHA or identity differs')
    text = body.decode('utf-8')
    if text.count(START) != 1 or text.count(END) != 1:
        raise ValueError('unique production repository policy markers required')
    begin = text.index(START) + len(START)
    end = text.index(END)
    if end <= begin:
        raise ValueError('ordered production repository policy markers required')
    # Exact production closure body, not a maintained test implementation.
    return text[begin:end].encode()


def put(path, body):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, 'wb') as output:
        output.write(body); output.flush(); os.fsync(output.fileno())
    descriptor = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


BUILD = r'''
import groovy.json.JsonOutput
import groovy.json.JsonSlurper
import java.security.MessageDigest
import org.gradle.api.internal.artifacts.DefaultModuleIdentifier
import org.gradle.internal.component.external.model.DefaultModuleComponentIdentifier
import org.gradle.api.internal.artifacts.repositories.ArtifactResolutionDetails

def owned = rootDir.parentFile.canonicalFile
def sourceProof = new JsonSlurper().parse(new File(rootDir,'control-source.json'))
def policyFile = new File(rootDir,'policy.groovy')
def policyBytes = policyFile.bytes
def policySHA = MessageDigest.getInstance('SHA-256').digest(policyBytes).encodeHex().toString()
assert policySHA == sourceProof.policy_sha256
def policy = new GroovyShell(getClass().classLoader,new Binding([owned:owned])).evaluate(
    'import org.gradle.api.GradleException\n' + new String(policyBytes,'UTF-8') + '\nreturn [observe:observeRepositories,check:checkRepo]\n')
policy.observe(repositories)
def jitpack = repositories.maven {
    name = 'controlledJitPack'; url = uri('https://jitpack.io')
    // Demonstrate that a pre-existing broader include cannot expand authority.
    content { includeGroupByRegex('.*') }
}
repositories.maven { name = 'ownedFileMaven'; url = new File(owned,'maven').toURI() }
configurations { first; second }
dependencies { first 'com.luobo.fixture:fixed:1.0'; second 'com.luobo.fixture:fixed:1.0' }

tasks.register('repositoryLifecycle') {
    doLast {
        def filter = jitpack.getContentFilter()
        def expected = new File(owned,'maven/com/luobo/fixture/fixed/1.0/fixed-1.0.jar').canonicalFile
        def first = configurations.first.resolve()
        assert first.size() == 1
        def resolved = first.first().canonicalFile
        assert resolved.toPath().startsWith(owned.toPath()) && resolved.name == expected.name
        assert MessageDigest.getInstance('SHA-256').digest(resolved.bytes).encodeHex().toString() ==
            MessageDigest.getInstance('SHA-256').digest(expected.bytes).encodeHex().toString()
        repositories.each(policy.check) // Real post-use production read-only path.
        policy.observe(repositories)   // Repeated early-observer installation is idempotent.
        assert configurations.second.resolve() == first
        repositories.each(policy.check)
        assert filter.is(jitpack.getContentFilter())
        boolean locked = false
        try { jitpack.content { includeModule('forbidden','module') } }
        catch (IllegalStateException failure) {
            assert failure.message.contains('Cannot mutate content repository descriptor')
            locked = true
        }
        assert locked

        def accepts = { group,module,version ->
            def component = DefaultModuleComponentIdentifier.newId(DefaultModuleIdentifier.newId(group,module),version)
            boolean accepted = true
            def details = [getModuleId:{ component.moduleIdentifier },getComponentId:{ component },
                isVersionListing:{ false },notFound:{ accepted=false }] as ArtifactResolutionDetails
            filter.execute(details) // Real Gradle filter, not a duplicate predicate.
            accepted
        }
        def commit = '039a35aefab7747c557242fa216c9ea11743b604'
        assert accepts('com.github.davidliu','audioswitch',commit)
        assert !accepts('comXgithubXdavidliu','audioswitch',commit)
        assert !accepts('com.github.davidliu','other',commit)
        assert !accepts('com.github.davidliu','audioswitch','wrong')
        boolean unknownHost = false
        try { repositories.maven { name='unapproved';url=uri('https://unapproved.invalid') } }
        catch (Exception failure) {
            def cause = failure
            int depth = 0
            while (depth++ < 8 && cause != null) {
                if (cause.message?.contains('unexpected dependency repository')) unknownHost=true
                cause = cause.cause
            }
            assert unknownHost
        }
        assert unknownHost
        def rejected = repositories.findByName('unapproved')
        if (rejected != null) repositories.remove(rejected) // Control cleanup only.
        repositories.each(policy.check)
        def facts = [state:'passed',Gradle:gradle.gradleVersion,production_resolver_sha256:sourceProof.resolver_sha256,
            production_policy_sha256:policySHA,actual_offline_file_resolutions:2,
            same_frozen_filter_after_use:true,content_mutation_after_use_refused:locked,
            exact_tuple_allowed:true,regex_lookalike_group_refused:true,other_module_refused:true,
            other_commit_refused:true,unknown_host_refused:unknownHost,network_acquisition:false]
        assert facts.Gradle == '9.3.1'
        logger.lifecycle('LUOBO_REPOSITORY_LIFECYCLE_COMPLETED ' + JsonOutput.toJson(facts))
    }
}
'''


def prepare(work, resolver):
    work = Path(work)
    info = work.lstat()
    if (not work.is_absolute() or work.resolve() != work or not stat.S_ISDIR(info.st_mode)
            or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) & 0o077):
        raise ValueError('existing canonical private owned control root required')
    policy = policy_bytes(Path(resolver))
    project = work / 'project'
    put(project / 'policy.groovy', policy)
    put(project / 'control-source.json', (json.dumps({'resolver_sha256': RESOLVER_SHA,
        'policy_sha256': hashlib.sha256(policy).hexdigest()}, sort_keys=True) + '\n').encode())
    put(project / 'settings.gradle', b"rootProject.name='repository-lifecycle-control'\n")
    put(project / 'gradle.properties', b'org.gradle.daemon=false\norg.gradle.parallel=false\norg.gradle.workers.max=1\n')
    put(project / 'build.gradle', BUILD.encode())
    buffer = io.BytesIO()
    with zipfile.ZipFile(buffer, 'w', compression=zipfile.ZIP_STORED) as archive:
        archive.writestr(zipfile.ZipInfo('fixture.txt', (2026, 1, 1, 0, 0, 0)), b'owned lifecycle control\n')
    base = work / 'maven/com/luobo/fixture/fixed/1.0'
    put(base / 'fixed-1.0.jar', buffer.getvalue())
    put(base / 'fixed-1.0.pom', b'<project><modelVersion>4.0.0</modelVersion><groupId>com.luobo.fixture</groupId><artifactId>fixed</artifactId><version>1.0</version></project>\n')
    return project


def check(log):
    lines = [line[len(MARKER):] for line in Path(log).read_text().splitlines() if line.startswith(MARKER)]
    if len(lines) != 1:
        raise ValueError('one actual lifecycle completion required')
    proof = json.loads(lines[0])
    if (proof['state'] != 'passed' or proof['Gradle'] != '9.3.1'
            or proof['production_resolver_sha256'] != RESOLVER_SHA
            or proof['production_policy_sha256'] != POLICY_SHA
            or proof['actual_offline_file_resolutions'] != 2 or proof['network_acquisition'] is not False
            or any(proof[field] is not True for field in ('same_frozen_filter_after_use',
                'content_mutation_after_use_refused', 'exact_tuple_allowed', 'regex_lookalike_group_refused',
                'other_module_refused', 'other_commit_refused', 'unknown_host_refused'))):
        raise ValueError('actual production lifecycle proof differs')
    return proof
