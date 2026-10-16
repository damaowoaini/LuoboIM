"""Small I/O, archive and resource primitives for the disposable cloud job."""
import hashlib
import json
import os
from pathlib import Path
import signal
import stat
import subprocess
import tarfile
import threading
import time
import urllib.parse
import urllib.request
import zipfile

GIB = 1024 ** 3
FLOOR, INITIAL, CAP = GIB, 4 * GIB, 16 * GIB


def sha(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as f:
        for block in iter(lambda: f.read(65536), b''):
            h.update(block)
    return h.hexdigest()


def sync(directory):
    fd = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def mkdir(path):
    path = Path(path)
    if not path.exists():
        mkdir(path.parent)
        path.mkdir(mode=0o700)
        sync(path.parent)
    if path.is_symlink() or not path.is_dir():
        raise ValueError('real owned directory required')


def put(path, data):
    path = Path(path)
    mkdir(path.parent)
    with path.open('xb') as f:
        f.write(data)
        f.flush()
        os.fsync(f.fileno())
    sync(path.parent)


def write_json(path, value):
    put(path, (json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + '\n').encode())


def name(value):
    if (not value or value.startswith('/') or '\\' in value
            or any(p in ('', '.', '..') for p in value.split('/'))
            or any(ord(c) < 32 for c in value)):
        raise ValueError('unsafe relative archive path')
    return value


def allocated(root):
    total = 0
    def visit(path):
        nonlocal total
        try:
            with os.scandir(path) as entries:
                for e in entries:
                    try:
                        s = e.stat(follow_symlinks=False)
                        total += s.st_blocks * 512
                        if stat.S_ISDIR(s.st_mode):
                            visit(e.path)
                    except FileNotFoundError:
                        pass
        except FileNotFoundError:
            pass
    visit(root)
    return total


class Budget:
    def __init__(self, work, host=None):
        self.work = Path(work)
        self.host = Path(host) if host is not None else self.work
        self.reason = None
        self.minimum = None
        self.minimum_host = None
        self.minimum_output = None
        self.peak = 0
        self.samples = 0
        self.lock = threading.Lock()
        self.stop = threading.Event()
        self.thread = None

    def latch(self, reason):
        with self.lock:
            self.reason = self.reason or reason

    def sample(self):
        v = os.statvfs(self.work)
        output_free = v.f_bavail * v.f_frsize
        h = os.statvfs(self.host)
        host_free = h.f_bavail * h.f_frsize
        free = min(host_free, output_free)
        used = allocated(self.work)
        with self.lock:
            self.minimum = free if self.minimum is None else min(free, self.minimum)
            self.minimum_host = host_free if self.minimum_host is None else min(host_free, self.minimum_host)
            self.minimum_output = output_free if self.minimum_output is None else min(output_free, self.minimum_output)
            self.peak = max(used, self.peak)
            self.samples += 1
            self.reason = self.reason or ('disk_floor' if free < FLOOR else None)
            self.reason = self.reason or ('owned_workspace_cap' if used > CAP else None)
        self.check()
        return free

    def check(self):
        if self.reason:
            raise ValueError('latched resource failure: ' + self.reason)

    def poll(self):
        while not self.stop.wait(.2):
            try:
                self.sample()
            except BaseException:
                self.latch('monitor_infrastructure_failed')

    def start(self):
        self.sample()
        self.thread = threading.Thread(target=self.poll, daemon=True)
        self.thread.start()

    def finish(self):
        try:
            self.sample()
        finally:
            self.stop.set()
            if self.thread is not None:
                self.thread.join()
        self.check()

    def record(self):
        return {'minimum_free_bytes': self.minimum, 'peak_owned_allocated_bytes': self.peak,
                'minimum_host_free_bytes': self.minimum_host, 'minimum_output_free_bytes': self.minimum_output,
                'samples': self.samples, 'sampling_interval_seconds': .2,
                'floor_bytes': FLOOR, 'cap_bytes': CAP, 'latched_failure': self.reason}


def kill(child):
    if child is not None and child.poll() is None:
        os.killpg(child.pid, signal.SIGTERM)
        try:
            child.wait(timeout=3)
        except subprocess.TimeoutExpired:
            os.killpg(child.pid, signal.SIGKILL)
            child.wait()


def run(argv, cwd, env, work, budget, label, timeout=900, expected_rejection=None, input_bytes=None):
    budget.check()
    log = Path(work) / 'evidence' / (label + '.log')
    child = None
    start = time.monotonic()
    error = None
    ok = False
    try:
        with log.open('x') as out:
            child = subprocess.Popen(list(map(str, argv)), cwd=cwd, env=env,
                stdout=out, stderr=subprocess.STDOUT, start_new_session=True,
                stdin=subprocess.PIPE if input_bytes is not None else subprocess.DEVNULL)
            if input_bytes is not None:
                child.stdin.write(input_bytes)
                child.stdin.close()
            while child.poll() is None:
                if time.monotonic() - start > timeout:
                    budget.latch('phase_timeout')
                if budget.reason:
                    kill(child)
                    break
                time.sleep(.05)
            out.flush()
            os.fsync(out.fileno())
        budget.sample()
        ok = child.returncode == 0
        if expected_rejection is not None:
            ok = child.returncode != 0 and expected_rejection in log.read_text(errors='replace')
        if not ok:
            print('FAILED phase=' + label, flush=True)
            print(log.read_text(errors='replace')[-12000:], flush=True)
            raise ValueError('command expectation failed: ' + label)
        return log
    except BaseException as cause:
        error = type(cause).__name__ + ': ' + str(cause)
        raise
    finally:
        kill(child)
        record = {'argv': list(map(str, argv)), 'exit_code': child.returncode if child is not None else None,
                  'expected_rejection': expected_rejection, 'expectation_met': ok and error is None,
                  'error': error, 'elapsed_seconds': round(time.monotonic() - start, 3),
                  'log_sha256': sha(log) if log.is_file() else None, **budget.record()}
        write_json(Path(work) / 'evidence' / (label + '.resource.json'), record)


def download(pin, output, budget):
    url = pin['url']
    if urllib.parse.urlsplit(url).scheme != 'https':
        raise ValueError('HTTPS artifact source required')
    h, old = hashlib.sha256(), hashlib.sha1()
    size = 0
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(url, timeout=60) as response, Path(output).open('xb') as f:
        final = urllib.parse.urlsplit(response.url)
        if final.scheme != 'https' or final.hostname not in {
                'go.dev', 'dl.google.com', 'services.gradle.org', 'downloads.gradle.org',
                'github.com', 'release-assets.githubusercontent.com', 'objects.githubusercontent.com'}:
            raise ValueError('unexpected official artifact redirect')
        while True:
            budget.check()
            b = response.read(65536)
            if not b:
                break
            size += len(b)
            if size > pin.get('max_bytes', 1024 ** 3):
                raise ValueError('tool archive exceeds bound')
            f.write(b)
            h.update(b)
            old.update(b)
        f.flush()
        os.fsync(f.fileno())
    if (pin.get('sha256') and h.hexdigest() != pin['sha256']
            or pin.get('official_sha1') and old.hexdigest() != pin['official_sha1']
            or pin.get('bytes') and size != pin['bytes']):
        raise ValueError('official artifact digest/length mismatch')
    return {'source_url': url, 'bytes': size, 'sha256': h.hexdigest(),
            'official_sha1_checked': bool(pin.get('official_sha1'))}


def unpack(archive, output, budget):
    """Official tool archives only; internal links allowed, traversal refused."""
    output = Path(output)
    if output.exists():
        raise ValueError('fresh tool extraction root required')
    mkdir(output)
    if str(archive).endswith('.zip'):
        with zipfile.ZipFile(archive) as z:
            if z.testzip() is not None:
                raise ValueError('tool ZIP CRC mismatch')
            entries = z.infolist()
            names = [name(e.filename.rstrip('/')) for e in entries]
            if len(set(names)) != len(names) or sum(e.file_size for e in entries) > 5 * GIB:
                raise ValueError('duplicate/oversize tool ZIP')
            links = []
            for e, n in zip(entries, names):
                budget.check()
                target = output / n
                mode = e.external_attr >> 16
                if e.is_dir():
                    mkdir(target)
                elif stat.S_ISLNK(mode):
                    links.append((target, z.read(e).decode()))
                else:
                    mkdir(target.parent)
                    with z.open(e) as src, target.open('xb') as dst:
                        while True:
                            budget.check()
                            block = src.read(65536)
                            if not block:
                                break
                            dst.write(block)
                        dst.flush()
                        os.fsync(dst.fileno())
                    target.chmod((mode & 0o777) or 0o644)
            for target, link in links:
                resolved = (target.parent / link).resolve()
                if Path(link).is_absolute() or not resolved.is_relative_to(output):
                    raise ValueError('external tool ZIP symlink')
                mkdir(target.parent)
                target.symlink_to(link)
            for target, _ in links:
                if not target.resolve().is_relative_to(output) or not target.exists():
                    raise ValueError('invalid tool ZIP link')
    else:
        # Ubuntu24's Python3.12 data filter understands bounded internal links.
        if not hasattr(tarfile, 'data_filter'):
            raise ValueError('Python3.12 archive data filter required')
        with tarfile.open(archive, 'r:gz') as t:
            entries = t.getmembers()
            if sum(e.size for e in entries) > 5 * GIB:
                raise ValueError('oversize official tool TAR')
            for e in entries:
                budget.check()
                name(e.name.rstrip('/'))
                filtered = tarfile.data_filter(e, str(output))
                if filtered is None:
                    raise ValueError('filtered official TAR member')
                target = output / filtered.name
                if filtered.isfile():
                    mkdir(target.parent)
                    with t.extractfile(e) as src, target.open('xb') as dst:
                        while True:
                            budget.check()
                            block = src.read(65536)
                            if not block:
                                break
                            dst.write(block)
                        dst.flush()
                        os.fsync(dst.fileno())
                    target.chmod(filtered.mode & 0o777)
                else:
                    t.extract(e, output, filter='data')
