export type ContactLookupMode = "userID" | "phoneNumber";
export type ContactLookupResult =
  | { state: "found"; userID: string }
  | { state: "missing" | "invalid" | "ambiguous" };
type ResponseUser = { userID?: unknown };
interface LookupAPI {
  findByIDs(userIDs: string[]): Promise<unknown>;
  searchPhone(phone: string): Promise<{ total: number; users: unknown }>;
}

// Different namespaces are explicit. A phone number may equal another user's
// ID: never combine OR results then pick users[0], or silently fall back modes.
export async function resolveContactLookup(
  mode: ContactLookupMode,
  raw: string,
  api: LookupAPI,
): Promise<ContactLookupResult> {
  const value = raw.trim();
  if (!value || value.length > 128 || /[\u0000-\u0020\u007f]/.test(value))
    return { state: "invalid" };
  if (mode === "userID") {
    const users = await api.findByIDs([value]);
    if (!Array.isArray(users)) throw new Error("Invalid exact user lookup response");
    if (users.length === 0) return { state: "missing" };
    // A substituted ID, duplicate row or unexpected extra row is never a card.
    if (users.length !== 1 || (users[0] as ResponseUser)?.userID !== value)
      return { state: "ambiguous" };
    return { state: "found", userID: value };
  }
  if (mode !== "phoneNumber" || !/^\d{6,32}$/.test(value))
    return { state: "invalid" };
  const { total, users } = await api.searchPhone(value);
  if (!Number.isSafeInteger(total) || total < 0 || !Array.isArray(users))
    throw new Error("Invalid exact phone lookup response");
  if (total === 0 && users.length === 0) return { state: "missing" };
  const id = (users[0] as ResponseUser)?.userID;
  if (total !== 1 || users.length !== 1 || typeof id !== "string" || !id)
    return { state: "ambiguous" };
  return { state: "found", userID: id };
}
