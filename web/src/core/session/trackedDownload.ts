/** The native completion event can arrive before the invoke acknowledgement. */
export async function startTrackedDownload(options: {
  register(): void;
  invoke(): Promise<unknown>;
  assertCurrent(): void;
  isCurrent(): boolean;
  rollback(): void;
}): Promise<void> {
  options.register();
  try {
    await options.invoke();
    options.assertCurrent();
  } catch (error) {
    if (options.isCurrent()) options.rollback();
    throw error;
  }
}
