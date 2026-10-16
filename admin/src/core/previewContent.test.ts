import { describe, expect, it, vi } from 'vitest';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { JSDOM } from 'jsdom';
vi.mock('@/components/Twemoji', () => ({ default: ({ children }: { children: unknown }) => children }));
vi.mock('@/components/OIMAvatar', () => ({ default: () => null }));
vi.mock('@/utils/common', () => ({ parseMessageByType: (message: any) => message.textElem.content }));
import TextMessageRender from '../pages/im_system/PreviewMessageModal/MessageItem/TextMessageRender';
import QuoteMessageRenderer from '../pages/im_system/PreviewMessageModal/MessageItem/QuoteMessageRenderer';
const payload = '<img src=x onerror="globalThis.previewInjected=true"><svg onload="globalThis.previewInjected=true"></svg><script>globalThis.previewInjected=true</script> & 😀\nsecond line';
const assertTextOnly = (html: string) => {
  const document = new JSDOM(html).window.document;
  expect(document.querySelectorAll('img,svg,script,iframe').length).toBe(0);
  expect(document.body.textContent).toContain(payload);
  expect(html).toContain('&lt;img');
};
describe('untrusted message preview rendering', () => {
  it('renders incoming message HTML as literal text while preserving emoji and newlines', () => {
    assertTextOnly(renderToStaticMarkup(createElement(TextMessageRender, { message: { contentType:101,textElem:{content:payload} } as any,isSender:false })));
  });
  it('escapes both the quoted nickname and quoted body', () => {
    const html=renderToStaticMarkup(createElement(QuoteMessageRenderer, { message: {quoteElem:{text:'quote',quoteMessage:{contentType:101,senderNickname:payload,textElem:{content:payload}}}} as any,isSender:false }));
    assertTextOnly(html);
    expect(new JSDOM(html).window.document.body.textContent?.split(payload).length).toBe(3);
  });
});
