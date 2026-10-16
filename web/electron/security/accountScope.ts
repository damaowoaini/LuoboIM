export interface AccountLease {
  readonly namespace: string;
  readonly generation: number;
}

// The main process revokes pending work before changing any cache directory.
// A completed logout/login for the same account is still a new generation.
export class AccountScope {
  private namespace?: string;
  private generation = 0;
  private listeners = new Set<() => void>();
  private windows = new WeakMap<object, AccountLease>();
  bindWindow(window: object): void {
    this.windows.set(window, this.lease());
  }
  ownsWindow(window: object): boolean {
    const lease = this.windows.get(window);
    return !!lease && this.owns(lease);
  }
  set(namespace?: string): void {
    if (namespace === this.namespace) return;
    if (namespace && (typeof namespace !== "string" || namespace.length > 2048))
      throw new Error("账号缓存归属无效");
    this.namespace = namespace;
    this.generation += 1;
    for (const listener of this.listeners) listener();
  }
  lease(): AccountLease {
    if (!this.namespace) throw new Error("请先登录后操作附件");
    return { namespace: this.namespace, generation: this.generation };
  }
  owns(lease: AccountLease): boolean {
    return this.namespace === lease.namespace && this.generation === lease.generation;
  }
  matches(namespace?: string): boolean {
    return !!namespace && this.namespace === namespace;
  }
  onChange(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
}

export const accountScope = new AccountScope();
