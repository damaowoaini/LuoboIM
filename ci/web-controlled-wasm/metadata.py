#!/usr/bin/env python3
import hashlib
import json
from pathlib import Path
import subprocess
import sys
root=Path(sys.argv[1]);node=subprocess.check_output(['node','--version'],text=True).strip()
go=subprocess.check_output(['go','version'],text=True).strip().split()[2]
files={}
for name in ['bin/go','lib/wasm/wasm_exec.js','lib/wasm/go_js_wasm_exec']:
    raw=(root/name).read_bytes();files[name]={'bytes':len(raw),'sha256':hashlib.sha256(raw).hexdigest()}
print(json.dumps({'go':go,'node':node,'GOROOT':str(root),'files':files}))
