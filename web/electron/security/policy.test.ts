import { describe,it,expect } from 'vitest';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { isWithinRoot,isTrustedDocument,downloadURL,safeFilename } from './policy';
describe('desktop privilege boundaries',()=>{
 it('checks an existing account cache after uncreated sibling roots without allowing an outside symlink',()=>{
  const folder=fs.mkdtempSync(path.join(os.tmpdir(),'luobo-lazy-cache-'));
  try{
   const sent=path.join(folder,'own','sentFileCache'),missing=path.join(folder,'own','fileCache'),outside=path.join(folder,'other','private');
   fs.mkdirSync(sent,{recursive:true});fs.mkdirSync(path.dirname(outside),{recursive:true});fs.writeFileSync(path.join(sent,'attachment'),'own');fs.writeFileSync(outside,'other');fs.symlinkSync(outside,path.join(sent,'escape'));
   const roots=[missing,sent,path.join(folder,'own','imageCache')];
   expect(isWithinRoot(path.join(sent,'attachment'),roots)).toBe(true);
   expect(isWithinRoot(outside,roots)).toBe(false);expect(isWithinRoot(path.join(sent,'escape'),roots)).toBe(false);
   expect(isWithinRoot(path.join(sent,'attachment'),[missing])).toBe(false);
  }finally{fs.rmSync(folder,{recursive:true,force:true});}
 });
 it('rejects traversal, prefix confusion and symlinks outside current account',()=>{const folder=fs.mkdtempSync(path.join(os.tmpdir(),'luobo-policy-'));try{const own=path.join(folder,'own');const other=path.join(folder,'owner-other');fs.mkdirSync(own);fs.mkdirSync(other);fs.writeFileSync(path.join(own,'a'),'own');fs.writeFileSync(path.join(other,'secret'),'other');fs.symlinkSync(path.join(other,'secret'),path.join(own,'shortcut'));expect(isWithinRoot(path.join(own,'a'),[own])).toBe(true);expect(isWithinRoot(path.join(other,'secret'),[own])).toBe(false);expect(isWithinRoot(path.join(own,'shortcut'),[own])).toBe(false);expect(isWithinRoot('../secret',[own])).toBe(false);}finally{fs.rmSync(folder,{recursive:true,force:true});}});
 it('allows only the packaged local document or configured loopback dev origin',()=>{expect(isTrustedDocument('file:///tmp/app/index.html#/chat','/tmp/app/index.html')).toBe(true);expect(isTrustedDocument('file:///etc/passwd','/tmp/app/index.html')).toBe(false);expect(isTrustedDocument('https://untrusted.example','/tmp/app/index.html')).toBe(false);expect(isTrustedDocument('http://127.0.0.1:5173/#/login','/tmp/app/index.html','http://127.0.0.1:5173')).toBe(true);expect(isTrustedDocument('https://remote.example','/tmp/app/index.html','https://remote.example')).toBe(false);});
 it('rejects executable/local download schemes and strips hostile filenames',()=>{for(const url of ['file:///etc/passwd','javascript:alert(1)','http://example.com/a','https://user:password@example.com/a'])expect(()=>downloadURL(url)).toThrow();expect(downloadURL('https://files.example/a.png').hostname).toBe('files.example');expect(safeFilename('../../evil.exe')).toBe('evil.exe');expect(safeFilename('..\\evil.exe')).toBe('evil.exe');});
});
