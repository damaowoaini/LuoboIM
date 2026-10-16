import { expect, it, vi } from 'vitest';
import { JSDOM } from 'jsdom';
import { parseTwemoji } from '../components/Twemoji';
it('parses only the requested emoji fragment without rewriting React-owned page nodes', () => {
  const dom = new JSDOM('<!doctype html><body><section id="unrelated">existing 😀 <button>keep button</button></section></body>');
  vi.stubGlobal('document', dom.window.document);
  vi.stubGlobal('Image', dom.window.Image);
  try {
    const section = document.querySelector('#unrelated')!;
    const button = document.querySelector('button');
    const before = section.innerHTML;
    const result = parseTwemoji('<script>literal</script> 😀');
    const fragment = new JSDOM(result).window.document;
    expect(section.innerHTML).toBe(before);
    expect(document.querySelector('button')).toBe(button);
    expect(fragment.querySelector('script')).toBeNull();
    expect(fragment.querySelector('img')?.getAttribute('alt')).toBe('😀');
    expect(fragment.body.textContent).toContain('<script>literal</script>');
  } finally { vi.unstubAllGlobals(); }
});
