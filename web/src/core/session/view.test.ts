import { expect, it } from 'vitest';
import { SessionCoordinator, type Session, type SessionStorage } from './session';
import { bindSessionView } from './view';
const response = {userID:'same-user',imToken:'synthetic-im',chatToken:'synthetic-chat',issuer:'https://route.example',serverInfo:{serverID:'tenant-a',serverName:'Tenant A',domains:['tenant.example']}};
it('navigates a remounted login after its mutation owner unmounted during credential persistence', async () => {
  let finishWrite: (() => void) | undefined;
  const storage: SessionStorage = {read:async()=>undefined,clear:async()=>{},write:async()=>new Promise<void>(resolve=>{finishWrite=resolve})};
  const session = new SessionCoordinator(storage);
  await session.restore();
  const oldRoutes: string[]=[];
  const detachOld=bindSessionView(session,'/login',()=>{},path=>oldRoutes.push(path));
  const commit=session.commit(response);
  await Promise.resolve();
  detachOld(); // RouterProvider key changes while write is still pending.
  const routes: string[]=[]; const readiness: boolean[]=[];
  const detachNew=bindSessionView(session,'/login',value=>readiness.push(value),path=>routes.push(path));
  await Promise.resolve();
  expect(routes).toEqual([]);
  finishWrite!(); await commit;
  expect(oldRoutes).toEqual([]);
  expect(routes).toEqual(['/chat']);
  expect(readiness.at(-1)).toBe(true);
  detachNew();
});
it('restores a persisted login into chat and revokes a private view immediately on logout', async () => {
  let stored: Session | undefined;
  const session=new SessionCoordinator({read:async()=>stored,write:async value=>{stored=value},clear:async()=>{stored=undefined}});
  await session.commit(response);
  const routes: string[]=[];const ready: boolean[]=[];
  const detachLogin=bindSessionView(session,'/login',value=>ready.push(value),path=>routes.push(path));
  await Promise.resolve();expect(routes).toEqual(['/chat']);detachLogin();
  const detachPrivate=bindSessionView(session,'/contact',value=>ready.push(value),path=>routes.push(path));
  await session.clear();
  expect(ready.at(-1)).toBe(false);expect(routes.at(-1)).toBe('/login');
  detachPrivate();
});
it('does not let an unmounted view navigate after a delayed restore', async () => {
  let resolveRead: ((value: undefined) => void) | undefined;
  const session=new SessionCoordinator({read:()=>new Promise(resolve=>{resolveRead=resolve}),write:async()=>{},clear:async()=>{}});
  const routes: string[]=[];const detach=bindSessionView(session,'/chat',()=>{},path=>routes.push(path));
  detach();resolveRead!(undefined);await session.restore();await Promise.resolve();
  expect(routes).toEqual([]);
});
