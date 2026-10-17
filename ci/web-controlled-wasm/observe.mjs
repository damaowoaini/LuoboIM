import fs from 'node:fs';
import vm from 'node:vm';
const [shimPath, wasmPath, expectedPath] = process.argv.slice(2);
const expected = JSON.parse(fs.readFileSync(expectedPath, 'utf8')).functions;
let networkAttempts = 0, databaseAttempts = 0;
globalThis.window = globalThis;
globalThis.self = globalThis;
globalThis.fetch = () => { ++networkAttempts; throw Error('startup networking forbidden'); };
globalThis.WebSocket = class { constructor() { ++networkAttempts; throw Error('startup WebSocket forbidden'); } };
globalThis.indexedDB = new Proxy({}, {get() { ++databaseAttempts; throw Error('startup database forbidden'); }});
vm.runInThisContext(fs.readFileSync(shimPath, 'utf8'), {filename: 'same-Go-wasm_exec.js'});
const before = new Set(Object.getOwnPropertyNames(globalThis));
const go = new Go();
const module = await WebAssembly.compile(fs.readFileSync(wasmPath));
const instance = await WebAssembly.instantiate(module, go.importObject);
go.run(instance).then(() => {throw Error('WASM main ended before observation');}).catch(() => process.exit(1));
setTimeout(() => {
  const actual = Object.getOwnPropertyNames(globalThis).filter(k => !before.has(k) && typeof globalThis[k] === 'function').sort();
  if (expected.length !== 145 || actual.length !== 145 || JSON.stringify(actual) !== JSON.stringify([...expected].sort()) || networkAttempts || databaseAttempts) {
    console.error('exact145 startup ABI/network/database check failed');process.exit(1);
  }
  console.log(JSON.stringify({state:'startup_registered_only_no_SDK_init',functions:actual,imports:WebAssembly.Module.imports(module),exports:WebAssembly.Module.exports(module),calls:{init:0,login:0,database:0,network:0},registrationNotFullAPISemanticProof:true}));
  process.exit(0);
},100);
