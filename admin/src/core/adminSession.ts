export interface AdminSession {
  version: 2;
  issuer: string;
  account: string;
  adminToken: string;
  imToken: string;
  imUserID: string;
  level: number;
}
export class StaleAdminSessionError extends Error {
  constructor() {
    super('管理员会话已变更');
  }
}
const key = 'luobo.admin.session';
export class AdminSessions {
  generation = 0;
  private requests = new Set<AbortController>();
  private listeners = new Set<() => void>();
  onChange(listener: () => void) {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }
  private revoked = false;
  private lastRecord: string | null = null;
  constructor(
    private storage: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>,
    private issuer: string,
  ) {
    try {
      this.lastRecord = storage.getItem(key);
    } catch {
      this.revoked = true;
    }
  }
  get current(): AdminSession | undefined {
    if (this.revoked) return undefined;
    try {
      const raw = this.storage.getItem(key);
      if (raw !== this.lastRecord) {
        this.revoked = true;
        this.invalidate();
        return undefined;
      }
      const value = JSON.parse(raw || 'null');
      if (
        value?.version === 2 &&
        value.issuer === this.issuer &&
        ['account', 'adminToken', 'imToken', 'imUserID'].every(
          (field) => typeof value[field] === 'string' && value[field],
        ) &&
        Number.isInteger(value.level)
      )
        return value;
    } catch {
      /* malformed or inaccessible storage cannot authenticate */
    }
    return undefined;
  }
  commit(value: Omit<AdminSession, 'version' | 'issuer'>, expectedGeneration: number) {
    if (expectedGeneration !== this.generation) throw new StaleAdminSessionError();
    if (
      !value.account ||
      !value.adminToken ||
      !value.imToken ||
      !value.imUserID ||
      !Number.isInteger(value.level)
    )
      throw new Error('管理员认证响应不完整');
    // A single atomic record is persisted before navigation or UI state changes.
    const record = JSON.stringify({ ...value, version: 2, issuer: this.issuer });
    this.storage.setItem(key, record);
    this.lastRecord = record;
    this.revoked = false;
    this.invalidate();
    for (const legacy of [
      'IMAccountToken',
      'IMAdminToken',
      'IMAdminUserID',
      'level',
      'cosprofile',
    ]) {
      try {
        this.storage.removeItem(legacy);
      } catch {
        /* new atomic record is authoritative */
      }
    }
  }
  invalidate() {
    this.generation++;
    for (const request of this.requests) request.abort();
    this.requests.clear();
    for (const listener of this.listeners) listener();
  }
  clear() {
    this.revoked = true;
    this.invalidate();
    this.storage.removeItem(key);
    this.lastRecord = null;
    for (const legacy of [
      'IMAccountToken',
      'IMAdminToken',
      'IMAdminUserID',
      'IMAdminAccount',
      'level',
      'cosprofile',
    ])
      this.storage.removeItem(legacy);
  }
  rejectAuthentication(errCode: number): boolean {
    // OpenIM tools/errs 1501..1507: expired, invalid, malformed, not-yet-valid,
    // unknown, kicked, or missing token. Business authorization/args are separate.
    if (!Number.isInteger(errCode) || errCode < 1501 || errCode > 1507 || !this.current)
      return false;
    try {
      this.clear();
    } catch {
      /* clear revokes memory before touching storage */
    }
    return true;
  }
  lease() {
    const generation = this.generation;
    const controller = new AbortController();
    this.requests.add(controller);
    return {
      signal: controller.signal,
      release: () => this.requests.delete(controller),
      assertCurrent: () => {
        if (generation !== this.generation) throw new StaleAdminSessionError();
      },
    };
  }
}
export const adminSessions =
  typeof window === 'undefined'
    ? undefined
    : new AdminSessions(window.localStorage, window.location.origin);
if (typeof window !== 'undefined')
  window.addEventListener('storage', (event) => {
    if (event.key === key) {
      adminSessions!.invalidate();
      window.location.reload();
    }
  });
