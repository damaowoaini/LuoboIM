import type { MessageItem } from 'open-im-sdk-wasm/lib/types/entity';
export class StalePreviewError extends Error {
  constructor() {
    super('消息预览会话已变更');
  }
}
interface SessionOwner {
  generation: number;
  readonly current: unknown;
  onChange(listener: () => void): () => void;
}
export interface PreviewConversation {
  conversationID: string;
  conversationType: number;
  userID: string;
  groupID: string;
  showName: string;
  faceURL: string;
  isPinned: boolean;
  latestMsgSendTime: number;
}
export type PreviewMessage = MessageItem & {
  encrypted: boolean;
  decryptionState: 'plain' | 'decrypted' | 'unavailable' | 'invalid';
};
export interface ConversationPage {
  conversations: PreviewConversation[];
  total: number;
  pageNumber: number;
  pageSize: number;
}
export interface PreviewFilters {
  conversationID: string;
  keyword: string;
  startTime: number;
  endTime: number;
}
export interface MessagePage {
  messages: PreviewMessage[];
  nextCursor: string;
  hasMore: boolean;
  scannedCount: number;
  unsearchableCount: number;
  searchComplete: boolean;
}
export interface PreviewTransport {
  conversations(
    body: { userID: string; keyword: string; pageNumber: number; pageSize: number },
    signal: AbortSignal,
  ): Promise<ConversationPage>;
  search(
    body: PreviewFilters & { userID: string; pageSize: number; cursor: string },
    signal: AbortSignal,
  ): Promise<MessagePage>;
}
// A preview holds only an administrator capability. It never obtains a target
// user's token or initializes a second SDK/database for that user.
export class AdminReadonlyPreview {
  private closed = false;
  private ownerGeneration: number;
  private query = 0;
  private listQuery = 0;
  private requests = new Set<AbortController>();
  private searchController?: AbortController;
  private listController?: AbortController;
  private detach: () => void;
  private filters?: PreviewFilters;
  private cursor = '';
  private loading = false;
  private more = true;
  constructor(
    private owner: SessionOwner,
    private userID: string,
    private transport: PreviewTransport,
  ) {
    this.ownerGeneration = owner.generation;
    this.detach = owner.onChange(() => this.close());
  }
  isCurrent() {
    return !this.closed && this.ownerGeneration === this.owner.generation && !!this.owner.current;
  }
  get isLoading() {
    return this.loading;
  }
  private assertCurrent() {
    if (!this.isCurrent()) throw new StalePreviewError();
  }
  close() {
    this.closed = true;
    this.query++;
    this.listQuery++;
    for (const request of this.requests) request.abort();
    this.requests.clear();
    this.detach?.();
  }
  async conversations(keyword = '', pageNumber = 1): Promise<ConversationPage> {
    this.assertCurrent();
    this.listController?.abort();
    const request = (this.listController = new AbortController());
    this.requests.add(request);
    const query = ++this.listQuery;
    try {
      const page = await this.transport.conversations(
        { userID: this.userID, keyword, pageNumber, pageSize: 40 },
        request.signal,
      );
      this.assertCurrent();
      if (query !== this.listQuery) throw new StalePreviewError();
      return page;
    } catch (error) {
      if (!this.isCurrent() || query !== this.listQuery) throw new StalePreviewError();
      throw error;
    } finally {
      this.requests.delete(request);
    }
  }
  select(filters: PreviewFilters) {
    this.assertCurrent();
    this.query++;
    this.searchController?.abort();
    this.filters = { ...filters };
    this.cursor = '';
    this.loading = false;
    this.more = true;
  }
  async next(): Promise<MessagePage | undefined> {
    this.assertCurrent();
    if (!this.filters || this.loading || !this.more) return;
    const request = (this.searchController = new AbortController());
    this.requests.add(request);
    const query = this.query;
    this.loading = true;
    try {
      const page = await this.transport.search(
        { ...this.filters, userID: this.userID, pageSize: 40, cursor: this.cursor },
        request.signal,
      );
      this.assertCurrent();
      if (query !== this.query) throw new StalePreviewError();
      if (page.hasMore && (!page.nextCursor || page.nextCursor === this.cursor))
        throw new Error('无效的消息扫描游标');
      this.cursor = page.nextCursor;
      this.more = page.hasMore;
      return page;
    } catch (error) {
      if (!this.isCurrent() || query !== this.query) throw new StalePreviewError();
      throw error;
    } finally {
      this.requests.delete(request);
      if (query === this.query) this.loading = false;
    }
  }
}
