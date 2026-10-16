import { SessionCoordinator } from './session';

// An authentication mutation may unmount when commit rotates the view
// generation. Observe the committed session itself instead of relying on a
// per-mutation success callback to navigate a component that no longer exists.
export function bindSessionView(
  session: SessionCoordinator,
  pathname: string,
  ready: (authenticated: boolean) => void,
  navigate: (path: string) => void,
): () => void {
  let disposed = false;
  const publish = () => {
    if (disposed) return;
    const authenticated = !!session.current;
    ready(authenticated);
    if (authenticated && pathname === '/login') navigate('/chat');
    else if (!authenticated && pathname !== '/login') navigate('/login');
  };
  const detach = session.onChange(publish);
  const generation = session.generation;
  void session.restore().then(() => {
    if (!disposed && generation === session.generation) publish();
  });
  return () => { disposed = true; detach(); };
}
