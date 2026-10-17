#!/usr/bin/env python3
"""Fetch one public fixed commit. No checkout of unreviewed working tree."""
from pathlib import Path
import subprocess
import sys
root=Path(sys.argv[1]).resolve();commit=sys.argv[2]
if commit!='7b156fc5557878ad6e6a07e572694af5699c6624':raise SystemExit('fixed commit required')
git=root/'git';git.mkdir()
common=['git','-c','credential.helper=','-c','core.hooksPath=/dev/null','-c','protocol.file.allow=never']
subprocess.run(common+['init','--quiet',str(git)],check=True)
subprocess.run(common+['-C',str(git),'fetch','--quiet','--no-tags','--depth=1','https://github.com/openimsdk/openim-sdk-core.git',commit],check=True)
actual=subprocess.check_output(common+['-C',str(git),'rev-parse','FETCH_HEAD'],text=True).strip()
if actual!=commit:raise SystemExit('fixed Git source differs')
with (root/'core.tar').open('xb') as f:subprocess.run(common+['-C',str(git),'archive','--format=tar',commit],stdout=f,check=True)
print('fixed_public_git_archive_complete')
