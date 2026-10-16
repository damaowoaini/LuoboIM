import path from 'node:path';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
export function isWithinRoot(candidate: string, roots: string[]): boolean {
  if (!path.isAbsolute(candidate)) return false;
  try {
    const actual = fs.realpathSync(candidate);
    return roots.some(root => {
      if (!root) return false;
      // Cache directories are created on demand. A missing sibling must not
      // hide another valid root; each unavailable root still fails closed.
      try {
        const boundary = fs.realpathSync(root);
        const relative = path.relative(boundary, actual);
        return relative === '' || (!relative.startsWith(`..${path.sep}`) && relative !== '..' && !path.isAbsolute(relative));
      } catch { return false; }
    });
  } catch { return false; }
}
export function isTrustedDocument(raw: string, indexHtml: string, developmentUrl?: string): boolean {
  try {
    const url = new URL(raw);
    if (developmentUrl) { const expected = new URL(developmentUrl); return url.origin === expected.origin && ['localhost','127.0.0.1','[::1]'].includes(url.hostname); }
    return url.protocol === 'file:' && path.resolve(fileURLToPath(url)) === path.resolve(indexHtml);
  } catch { return false; }
}
export function downloadURL(raw: string): URL {
  const url = new URL(raw);
  if (url.protocol !== 'https:' || url.username || url.password) throw new Error('下载仅允许HTTPS地址');
  return url;
}
export const safeFilename = (raw: string): string => path.basename(raw.replace(/\\/g,'/')).replace(/[\x00-\x1f]/g,'').slice(0,180) || 'file';
