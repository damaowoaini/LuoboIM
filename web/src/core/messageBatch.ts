export type SendReceipt = { state: "sent" | "unconfirmed" | "stale" };
export type BatchRecipient = { userID?: string; groupID?: string };
export type RecipientReceipt = { recipient: BatchRecipient; state: "sent" | "partial" | "unconfirmed" | "not-sent" | "skipped" | "cancelled" };

/** Each selected target is attempted once. A failed/unknown send is never retried.
 * The caller retains the receipts and must not describe a partial batch as sent. */
export async function deliverMessageBatch<T>(options: {
  recipients: readonly BatchRecipient[];
  additional: string;
  isCurrent(): boolean;
  canDeliver(recipient: BatchRecipient): Promise<boolean>;
  createMessage(): Promise<T>;
  createAdditional(text: string): Promise<T>;
  send(message: T, recipient: BatchRecipient): Promise<SendReceipt>;
}): Promise<RecipientReceipt[]> {
  const receipts: RecipientReceipt[] = [];
  for (const original of options.recipients) {
    const recipient = { ...original };
    if (!options.isCurrent()) { receipts.push({ recipient, state: "cancelled" }); continue; }
    let message: T;
    try {
      if (!(await options.canDeliver(recipient))) { receipts.push({ recipient, state: "skipped" }); continue; }
      if (!options.isCurrent()) { receipts.push({ recipient, state: "cancelled" }); continue; }
      message = await options.createMessage();
    } catch { receipts.push({ recipient, state: "not-sent" }); continue; }
    if (!options.isCurrent()) { receipts.push({ recipient, state: "cancelled" }); continue; }
    let primary: SendReceipt;
    try { primary = await options.send(message, recipient); }
    catch { primary = { state: "unconfirmed" }; }
    if (primary.state !== "sent") { receipts.push({ recipient, state: primary.state === "stale" ? "cancelled" : "unconfirmed" }); continue; }
    if (!options.additional) { receipts.push({ recipient, state: "sent" }); continue; }
    if (!options.isCurrent()) { receipts.push({ recipient, state: "partial" }); continue; }
    try {
      const additional = await options.createAdditional(options.additional);
      if (!options.isCurrent()) { receipts.push({ recipient, state: "partial" }); continue; }
      const result = await options.send(additional, recipient);
      receipts.push({ recipient, state: result.state === "sent" ? "sent" : "partial" });
    } catch { receipts.push({ recipient, state: "partial" }); }
  }
  return receipts;
}
