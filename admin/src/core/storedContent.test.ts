import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { JSDOM } from 'jsdom';
import { renderStoredMessage, safeStoredMediaURL } from '../components/ContentParse/StoredMessageContent';
describe('legacy global message storage rendering', () => {
  it('keeps cipher and malformed rows isolated without exposing opaque storage', () => {
    for (const record of [
      {contentType:101,content:'opaque-cipher'},
      {contentType:101,content:'opaque-cipher',keyVersion:1},
      {contentType:101,content:JSON.stringify({content:{nested:'invalid React child'}})},
      {contentType:1508,content:JSON.stringify({detail:'malformed-notice'})},
      {contentType:109,content:JSON.stringify({description:'malformed-location'})},
    ]) {
      const html = renderToStaticMarkup(renderStoredMessage(record));
      expect(html).toContain('只读预览');
      expect(html).not.toContain('opaque-cipher');
    }
  });
  it('preserves plain text and escapes stored HTML', () => {
    const payload='<img src=x onerror=alert(1)> & 😀';
    const html=renderToStaticMarkup(renderStoredMessage({contentType:101,content:JSON.stringify({content:payload})}));
    const document=new JSDOM(html).window.document;
    expect(document.querySelector('img,script,svg')).toBeNull();
    expect(document.body.textContent).toBe(payload);
  });
  it('blocks executable, credential-bearing and local media URLs', () => {
    for (const value of ['javascript:alert(1)','data:text/html,evil','file:///etc/passwd','https://user:password@example.com/a'])
      expect(safeStoredMediaURL(value)).toBeUndefined();
    const html=renderToStaticMarkup(renderStoredMessage({contentType:105,content:JSON.stringify({sourceUrl:'javascript:alert(1)'})}));
    expect(new JSDOM(html).window.document.querySelector('a')?.getAttribute('href')).toBeNull();
    expect(safeStoredMediaURL('https://files.example/a?signature=abc%2Bdef')).toBe('https://files.example/a?signature=abc%2Bdef');
  });
});
