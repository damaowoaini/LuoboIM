const assert = require('node:assert/strict');
const { test } = require('node:test');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const { spawnSync } = require('node:child_process');
const hash = b => crypto.createHash('sha256').update(b).digest('hex');
function fixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'luobo-sdk-upgrade-'));
  const write = (name, body) => { const p=path.join(root,name);fs.mkdirSync(path.dirname(p),{recursive:true});fs.writeFileSync(p,body); };
  write('tool/prepare-sdk.cjs',fs.readFileSync(path.join(__dirname,'prepare-sdk.cjs')));
  const manifest={package:'synthetic-sdk',version:'fixed',files:[]};
  write('node_modules/synthetic-sdk/package.json',JSON.stringify({version:'fixed'}));
  for(const name of ['first.js','second.js']) {
    const original=Buffer.from('original '+name),previous=Buffer.from('reviewed previous '+name),patched=Buffer.from('reviewed current '+name);
    manifest.files.push({path:name,original_sha256:hash(original),previous_patched_sha256:hash(previous),patched_sha256:hash(patched)});
    write('sdk-patches/'+name+'.patched',patched);write('node_modules/synthetic-sdk/'+name,previous);
  }
  const seal=()=>write('sdk-patches/manifest.json',JSON.stringify(manifest));
  const run=()=>spawnSync(process.execPath,[path.join(root,'tool/prepare-sdk.cjs')],{encoding:'utf8',timeout:10000});
  const read=name=>fs.readFileSync(path.join(root,'node_modules/synthetic-sdk/'+name));
  seal();return {root,write,read,manifest,seal,run,cleanup:()=>fs.rmSync(root,{recursive:true,force:true})};
}
test('upgrades only pinned previous bytes after whole-batch preflight and remains idempotent',()=>{
  const f=fixture();try {
    f.write('node_modules/synthetic-sdk/second.js','unknown source');
    assert.notEqual(f.run().status,0);assert.equal(f.read('first.js').toString(),'reviewed previous first.js');
    f.write('node_modules/synthetic-sdk/second.js','reviewed previous second.js');
    f.write('sdk-patches/second.js.patched','corrupt patch');
    assert.notEqual(f.run().status,0);assert.equal(f.read('first.js').toString(),'reviewed previous first.js');
    f.write('sdk-patches/second.js.patched','reviewed current second.js');
    assert.equal(f.run().status,0);assert.equal(f.run().status,0);
    for(const name of ['first.js','second.js'])assert.equal(f.read(name).toString(),'reviewed current '+name);
    f.write('sdk-patches/second.js.patched','tampered after install');
    assert.notEqual(f.run().status,0);
  }finally{f.cleanup();}
});
test('requires both exact core files before any JS upgrade and rejects mixed or incomplete pairs',()=>{
  const f=fixture();try {
    const wasm=Buffer.from('synthetic wasm bytes'),shim=Buffer.from('synthetic paired Go shim');
    f.write('public/openIM.wasm',wasm);f.write('public/wasm_exec.js',shim);
    const pair=[{path:'public/openIM.wasm',bytes:wasm.length,sha256:hash(wasm)},{path:'public/wasm_exec.js',bytes:shim.length,sha256:hash(shim)}];
    for(const invalid of [[pair[0]],[pair[0],pair[0]],[pair[0],{...pair[1],sha256:'0'.repeat(64)}]]){
      f.manifest.coreFiles=invalid;f.seal();assert.notEqual(f.run().status,0);
      assert.equal(f.read('first.js').toString(),'reviewed previous first.js');
    }
    f.manifest.coreFiles=pair;f.seal();f.write('public/wasm_exec.js','different Go runtime');
    assert.notEqual(f.run().status,0);assert.equal(f.read('first.js').toString(),'reviewed previous first.js');
    f.write('public/wasm_exec.js',shim);assert.equal(f.run().status,0);
    assert.equal(f.read('first.js').toString(),'reviewed current first.js');
  }finally{f.cleanup();}
});
