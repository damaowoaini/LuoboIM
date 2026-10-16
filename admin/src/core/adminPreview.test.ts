import { describe, expect, it, vi } from 'vitest';
import { AdminSessions } from './adminSession';
import {
  AdminReadonlyPreview,
  PreviewTransport,
  MessagePage,
  StalePreviewError,
} from './adminPreview';
const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
};
const page = (nextCursor = '', hasMore = false): MessagePage => ({
  messages: [],
  nextCursor,
  hasMore,
  scannedCount: 200,
  unsearchableCount: 2,
  searchComplete: !hasMore,
});
const fixture = () => {
  const records = new Map<string, string>();
  const owner = new AdminSessions(
    {
      getItem: (key) => records.get(key) || null,
      setItem: (key, value) => {
        records.set(key, value);
      },
      removeItem: (key) => {
        records.delete(key);
      },
    },
    'https://admin.example',
  );
  owner.commit(
    {
      account: 'admin-a',
      imUserID: 'im-admin',
      adminToken: 'synthetic-admin',
      imToken: 'synthetic-im',
      level: 100,
    },
    0,
  );
  const transport = {
    conversations: vi.fn<PreviewTransport['conversations']>(async () => ({
      conversations: [],
      total: 0,
      pageNumber: 1,
      pageSize: 40,
    })),
    search: vi.fn<PreviewTransport['search']>(async () => page()),
  };
  const preview = new AdminReadonlyPreview(owner, 'target-user', transport);
  return { owner, transport, preview };
};
const filters = {
  conversationID: 'si_target_peer',
  keyword: 'needle',
  startTime: 1700000000000,
  endTime: 1700001000000,
};
describe('read-only administrator preview HTTP cursor lifecycle', () => {
  it('continues past an empty scan page and preserves the original filters and cursor', async () => {
    const { preview, transport } = fixture();
    transport.search.mockResolvedValueOnce(page('signed-next', true)).mockResolvedValueOnce(page());
    preview.select(filters);
    expect((await preview.next())?.hasMore).toBe(true);
    await preview.next();
    expect(transport.search.mock.calls.map(([body]) => body)).toEqual([
      { ...filters, userID: 'target-user', pageSize: 40, cursor: '' },
      { ...filters, userID: 'target-user', pageSize: 40, cursor: 'signed-next' },
    ]);
    expect(await preview.next()).toBeUndefined();
  });
  it('cancels the obsolete query and rejects a late result without replacing the new cursor', async () => {
    const { preview, transport } = fixture();
    const slow = deferred<MessagePage>();
    transport.search
      .mockImplementationOnce(() => slow.promise)
      .mockResolvedValueOnce(page('new-cursor', true));
    preview.select(filters);
    const old = preview.next().catch((error) => error);
    preview.select({ ...filters, conversationID: 'next-conversation' });
    expect(transport.search.mock.calls[0][1].aborted).toBe(true);
    await preview.next();
    slow.resolve(page('old-cursor', true));
    expect(await old).toBeInstanceOf(StalePreviewError);
    await preview.next();
    expect(transport.search.mock.calls[2][0].cursor).toBe('new-cursor');
  });
  it('aborts requests and rejects reads immediately after administrator logout', async () => {
    const { preview, transport, owner } = fixture();
    const slow = deferred<MessagePage>();
    transport.search.mockImplementationOnce(() => slow.promise);
    preview.select(filters);
    const old = preview.next().catch((error) => error);
    owner.clear();
    expect(transport.search.mock.calls[0][1].aborted).toBe(true);
    slow.resolve(page());
    expect(await old).toBeInstanceOf(StalePreviewError);
    await expect(preview.next()).rejects.toThrow(StalePreviewError);
  });
  it('closing one modal cannot revoke another modal capability', async () => {
    const { preview, owner, transport } = fixture();
    const second = new AdminReadonlyPreview(owner, 'another-target', transport);
    preview.close();
    second.select(filters);
    await second.next();
    expect(transport.search.mock.calls[0][0].userID).toBe('another-target');
    expect(second.isCurrent()).toBe(true);
  });
  it('refuses a non-advancing server cursor instead of looping over the same private records', async () => {
    const { preview, transport } = fixture();
    transport.search.mockResolvedValue(page('same', true));
    preview.select(filters);
    await preview.next();
    await expect(preview.next()).rejects.toThrow('游标');
  });
  it('searches all conversations server-side and suppresses an obsolete list response', async () => {
    const { preview, transport } = fixture();
    const slow = deferred<any>();
    transport.conversations.mockImplementationOnce(() => slow.promise);
    const old = preview.conversations('old-id').catch((error) => error);
    await preview.conversations('another-id', 2);
    slow.resolve({ conversations: [], total: 1000, pageNumber: 1, pageSize: 40 });
    expect(await old).toBeInstanceOf(StalePreviewError);
    expect(transport.conversations.mock.calls[0][1].aborted).toBe(true);
    expect(transport.conversations.mock.calls[1][0]).toEqual({
      userID: 'target-user',
      keyword: 'another-id',
      pageNumber: 2,
      pageSize: 40,
    });
  });
});
