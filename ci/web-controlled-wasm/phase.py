#!/usr/bin/env python3
"""Bounded stdout/stderr capture. Caller guard owns this entire process group."""
import json
import os
from pathlib import Path
import subprocess
import sys
import time
out=Path(sys.argv[1]);argv=sys.argv[2:]
if argv[:1]!=['--'] or not argv[1:]:raise SystemExit('explicit command required')
argv=argv[1:];limit=4*1024*1024;oversized=False
if os.environ.get('LUOBO_WASM_ISOLATED')=='1':
    if os.geteuid()==0 or str(os.stat('/proc/self/ns/net').st_ino)==os.environ.get('LUOBO_WASM_HOST_NET_INODE'):
        raise SystemExit('real non-root kernel network namespace required')
    with (out/'isolation.json').open('x') as f:
        json.dump({'nonRoot':True,'differentKernelNetNamespace':True,'uid':os.geteuid(),'DBFixture':False},f);f.write('\n')
with (out/'stdout.log').open('xb') as stdout,(out/'stderr.log').open('xb') as stderr:
    child=subprocess.Popen(argv,stdout=stdout,stderr=stderr)
    while child.poll() is None:
        if any((out/n).stat().st_size>limit for n in ['stdout.log','stderr.log']):
            oversized=True;child.terminate()
            try:child.wait(timeout=3)
            except subprocess.TimeoutExpired:child.kill();child.wait()
            break
        time.sleep(.05)
    code=child.wait()
    stdout.flush();stderr.flush();os.fsync(stdout.fileno());os.fsync(stderr.fileno())
if any((out/n).stat().st_size>limit for n in ['stdout.log','stderr.log']):oversized=True
with (out/'process.json').open('x') as f:
    json.dump({'argv':argv,'exit':code,'outputLimitBytesPerStream':limit,'outputLimitExceeded':oversized},f,indent=2);f.write('\n');f.flush();os.fsync(f.fileno())
raise SystemExit(74 if oversized else code)
