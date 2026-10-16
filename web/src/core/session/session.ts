export interface Tenant {
  serverID: string;
  serverName: string;
  domains: string[];
  activeLine: number;
}
export interface Session {
  version: 1;
  issuer: string;
  userID: string;
  chatToken: string;
  imToken: string;
  tenant: Tenant;
}
export interface SessionStorage {
  read(): Promise<unknown>;
  write(value: Session): Promise<void>;
  clear(): Promise<void>;
}
export class StaleSessionError extends Error {
  constructor() { super("会话已切换，请重新操作"); this.name = "StaleSessionError"; }
}
const requiredString = (value: unknown, name: string): string => {
  if (typeof value !== "string" || !value.trim()) throw new Error(`认证响应缺少 ${name}`);
  return value.trim();
};
export function validateDomain(value: unknown): string {
  const raw = requiredString(value, "租户线路");
  const input = raw.replace(/^https:\/\//, "").replace(/\/$/, "");
  const url = new URL(`https://${input}`);
  if (url.username || url.password || url.pathname !== "/" || url.search || url.hash || !url.hostname || /[\s\\]/.test(input)) {
    throw new Error("租户线路必须是有效 HTTPS 域名");
  }
  if (url.port && url.port !== "443") throw new Error("租户线路端口无效");
  return url.host;
}
export function sessionFromResponse(value: unknown): Session {
  if (!value || typeof value !== "object") throw new Error("认证响应格式错误");
  const data = value as Record<string, unknown>;
  const info = data.serverInfo as Record<string, unknown> | undefined;
  if (!info || typeof info !== "object") throw new Error("认证响应缺少租户信息");
  const rawDomains = Array.isArray(info.domains) ? info.domains : typeof info.domains === "string" ? info.domains.split(",").map(x => x.trim()).filter(Boolean) : [];
  const domains = [...new Set(rawDomains.map(validateDomain))];
  if (!domains.length) throw new Error("租户没有可用线路");
  return {
    version: 1,
    issuer: requiredString(data.issuer, "登录服务标识"),
    userID: requiredString(data.userID, "userID"),
    chatToken: requiredString(data.chatToken, "chatToken"),
    imToken: requiredString(data.imToken, "imToken"),
    tenant: { serverID: requiredString(info.serverID, "serverID"), serverName: requiredString(info.serverName, "serverName"), domains, activeLine: 0 },
  };
}
export function validateStoredSession(value: unknown): Session {
  const saved = value as Session;
  if (!saved || saved.version !== 1 || !saved.tenant) throw new Error("会话存储格式错误");
  const validated = sessionFromResponse({ ...saved, serverInfo: saved.tenant });
  const index = saved.tenant.activeLine;
  if (!Number.isInteger(index) || index < 0 || index >= validated.tenant.domains.length) throw new Error("会话线路索引无效");
  return { ...validated, tenant: { ...validated.tenant, activeLine: index } };
}
export interface RequestLease { generation: number; signal: AbortSignal; release(): void; assertCurrent(): void; }
export class SessionCoordinator {
  private generationValue = 0;
  private active: Session | undefined;
  private requests = new Set<AbortController>();
  private ready: Promise<Session | undefined> | undefined;
  private queue: Promise<void> = Promise.resolve();
  private listeners = new Set<() => void>();
  constructor(private storage: SessionStorage) {}
  get generation(): number { return this.generationValue; }
  get current(): Session | undefined { return this.active; }
  get namespace(): string {
    if (!this.active) throw new Error("请先登录");
    return `${encodeURIComponent(this.active.issuer)}:${encodeURIComponent(this.active.tenant.serverID)}:${encodeURIComponent(this.active.userID)}`;
  }
  onChange(listener: () => void): () => void { this.listeners.add(listener); return () => this.listeners.delete(listener); }
  settled(): Promise<void> { return this.queue; }
  private rotate(): void {
    this.generationValue += 1;
    for (const request of this.requests) request.abort();
    this.requests.clear();
  }
  private emit(): void { for (const listener of this.listeners) listener(); }
  private serialize<T>(work: () => Promise<T>): Promise<T> {
    const operation = this.queue.then(work, work);
    this.queue = operation.then(() => undefined, () => undefined);
    return operation;
  }
  restore(): Promise<Session | undefined> {
    if (!this.ready) {
      const expected = this.generationValue;
      this.ready = this.storage.read().then(value => {
        if (expected !== this.generationValue) return this.active;
        if (value === null || value === undefined) return undefined;
        this.active = validateStoredSession(value);
        this.emit();
        return this.active;
      }).catch(async () => {
        if (expected !== this.generationValue) return this.active;
        this.rotate(); this.active = undefined; this.emit();
        await this.storage.clear().catch(() => undefined);
        return undefined;
      });
    }
    return this.ready;
  }
  async commit(response: unknown, expectedGeneration = this.generationValue): Promise<Session> {
    const next = sessionFromResponse(response);
    return this.serialize(async () => {
      if (expectedGeneration !== this.generationValue) throw new StaleSessionError();
      this.rotate();
      const committingGeneration = this.generationValue;
      this.active = undefined;
      this.ready = Promise.resolve(undefined);
      this.emit();
      // One IndexedDB record holds tenant and both credentials. Navigation
      // waits for the whole write; partial token/address combinations cannot exist.
      await this.storage.write(next);
      if (committingGeneration !== this.generationValue) throw new StaleSessionError();
      this.active = next;
      this.ready = Promise.resolve(next);
      this.emit();
      return next;
    });
  }
  invalidate(): void {
    this.rotate(); this.active = undefined; this.ready = undefined; this.emit();
  }
  clear(): Promise<void> {
    // Invalidate callbacks synchronously, before the async storage write.
    this.rotate(); this.active = undefined; this.ready = Promise.resolve(undefined); this.emit();
    return this.serialize(() => this.storage.clear());
  }
  async switchLine(index: number, reconnect: (session: Session) => Promise<void>): Promise<void> {
    return this.serialize(async () => {
      const previous = this.active;
      if (!previous || !Number.isInteger(index) || index < 0 || index >= previous.tenant.domains.length) throw new Error("线路不可用");
      if (index === previous.tenant.activeLine) return;
      this.rotate();
      const switchingGeneration = this.generationValue;
      const next = { ...previous, tenant: { ...previous.tenant, activeLine: index } };
      try {
        await reconnect(next);
        if (switchingGeneration !== this.generationValue) throw new StaleSessionError();
        await this.storage.write(next);
        if (switchingGeneration !== this.generationValue) throw new StaleSessionError();
        this.active = next; this.ready = Promise.resolve(next); this.emit();
      } catch (error) {
        // Keep the previous persisted record. Reconnect failure cannot change
        // which tenant owns data or send the old token to another tenant.
        if (switchingGeneration === this.generationValue) {
          this.active = previous; this.ready = Promise.resolve(previous); this.emit();
          try { await reconnect(previous); } catch { /* UI still reports the original connection failure. */ }
        }
        throw error;
      }
    });
  }
  lease(): RequestLease {
    const generation = this.generationValue;
    const controller = new AbortController();
    this.requests.add(controller);
    return { generation, signal: controller.signal, release: () => this.requests.delete(controller), assertCurrent: () => { if (generation !== this.generationValue) throw new StaleSessionError(); } };
  }
  guard<T extends unknown[], R>(callback: (...args: T) => R): (...args: T) => R | undefined {
    const generation = this.generationValue;
    return (...args) => generation === this.generationValue ? callback(...args) : undefined;
  }
}
export function endpoints(session: Session): { chat: string; im: string; ws: string } {
  const domain = session.tenant.domains[session.tenant.activeLine];
  return { chat: `https://${domain}/chat`, im: `https://${domain}/api`, ws: `wss://${domain}/msg_gateway` };
}
