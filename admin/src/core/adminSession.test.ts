import { describe, it, expect } from 'vitest';
import { AdminSessions, StaleAdminSessionError } from './adminSession';
const values = (id = 'a') => ({
  account: id,
  imUserID: `im-${id}`,
  adminToken: `admin-${id}`,
  imToken: `token-${id}`,
  level: 100,
});
const fixture = () => {
  const records = new Map<string, string>();
  const store = {
    getItem: (key: string) => records.get(key) || null,
    setItem: (key: string, value: string) => {
      records.set(key, value);
    },
    removeItem: (key: string) => {
      records.delete(key);
    },
  };
  return { store, records, session: new AdminSessions(store, 'https://admin.example') };
};
describe('administrator credential boundary', () => {
  it('requires a fresh login after the legacy shared-management credential cutover', () => {
    const {session,records}=fixture();
    records.set('luobo.admin.session',JSON.stringify({version:1,issuer:'https://admin.example',...values()}));
    expect(session.current).toBeUndefined();
    session.commit(values('fresh-delegation'),session.generation);
    expect(JSON.parse(records.get('luobo.admin.session')!)).toMatchObject({version:2,account:'fresh-delegation'});
  });
  it('saves both credentials and identity atomically and does not restore legacy unscoped keys', () => {
    const { session, records } = fixture();
    records.set('IMAccountToken', 'legacy');
    expect(session.current).toBeUndefined();
    session.commit(values(), session.generation);
    expect(records.size).toBe(1);
    expect(session.current?.imUserID).toBe('im-a');
  });
  it('fails closed on partial, corrupt, or foreign-origin persisted sessions', () => {
    const { session, records } = fixture();
    for (const record of [
      'not json',
      JSON.stringify({ version: 2, issuer: 'https://other.example', ...values() }),
      JSON.stringify({ version: 2, issuer: 'https://admin.example', ...values(), imToken: '' }),
    ]) {
      records.set('luobo.admin.session', record);
      expect(session.current).toBeUndefined();
    }
  });
  it('does not navigate with partially stored credentials when persistence fails', () => {
    const { session, store } = fixture();
    store.setItem = () => {
      throw new Error('disk full');
    };
    expect(() => session.commit(values(), 0)).toThrow('disk full');
    expect(session.current).toBeUndefined();
  });
  it('keeps logout revoked even when persistent deletion fails', () => {
    const { session, store } = fixture();
    session.commit(values(), 0);
    store.removeItem = () => {
      throw new Error('storage blocked');
    };
    expect(() => session.clear()).toThrow();
    expect(session.current).toBeUndefined();
  });
  it('does not borrow another window account before the storage event is delivered', () => {
    const { session, records } = fixture();
    session.commit(values(), 0);
    records.set(
      'luobo.admin.session',
      JSON.stringify({ version: 2, issuer: 'https://admin.example', ...values('other-window') }),
    );
    expect(session.current).toBeUndefined();
  });
  it('aborts old work and rejects late login or response after logout and account replacement', () => {
    const { session } = fixture();
    session.commit(values(), 0);
    const generation = session.generation;
    const lease = session.lease();
    session.clear();
    session.commit(values('second'), session.generation);
    expect(lease.signal.aborted).toBe(true);
    expect(() => lease.assertCurrent()).toThrow(StaleAdminSessionError);
    expect(() => session.commit(values('late'), generation)).toThrow(StaleAdminSessionError);
    expect(session.current?.account).toBe('second');
  });
});
it('revokes malformed/expired/kicked/missing authentication but preserves business permission failures', () => {
  for (const code of [1501, 1502, 1503, 1504, 1505, 1506, 1507]) {
    const { session } = fixture();
    session.commit(values(), 0);
    const lease = session.lease();
    expect(session.rejectAuthentication(code)).toBe(true);
    expect(session.current).toBeUndefined();
    expect(lease.signal.aborted).toBe(true);
  }
  for (const code of [1001, 1002, 1500, 500]) {
    const { session } = fixture();
    session.commit(values(), 0);
    const lease = session.lease();
    expect(session.rejectAuthentication(code)).toBe(false);
    expect(session.current?.account).toBe('a');
    expect(lease.signal.aborted).toBe(false);
  }
});
