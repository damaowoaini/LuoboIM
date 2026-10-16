import { sessionCoordinator } from "./browser";
export function subscribeSessionEvent(channel: string, callback: (...args: any[]) => void): (() => void) | undefined {
  return window.electronAPI?.subscribe(channel, sessionCoordinator.guard(callback));
}
export function subscribeSessionEventOnce(channel: string, callback: (...args: any[]) => void): void {
  window.electronAPI?.subscribeOnce(channel, sessionCoordinator.guard(callback));
}
