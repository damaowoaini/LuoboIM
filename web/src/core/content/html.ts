import DOMPurify from "dompurify";
const attributes = [
  "href",
  "target",
  "rel",
  "class",
  "src",
  "alt",
  "type",
  "role",
  "tabindex",
  "data-action",
  "data-user-id",
  "data-group-id",
  "data-message-id",
];
export const sanitizeHtml = (html: string) =>
  DOMPurify.sanitize(html, {
    ALLOWED_TAGS: ["a", "br", "span", "img", "b", "i", "em", "strong", "button"],
    ALLOWED_ATTR: attributes,
    ALLOW_DATA_ATTR: false,
  });
export const sanitizeEditorHtml = (html: string) =>
  DOMPurify.sanitize(html, {
    ALLOWED_TAGS: ["br", "div", "p", "span", "img", "b", "i", "em", "strong"],
    ALLOWED_ATTR: ["class", "src", "alt", "contenteditable", "data-id", "data-name"],
    ALLOW_DATA_ATTR: false,
    ALLOWED_URI_REGEXP:
      /^(?:(?:https?|blob):|data:image\/(?:png|gif|jpe?g|webp);base64,|[^a-z]|[a-z+.-]+(?:[^a-z+.-:]|$))/i,
  });
export function editorPlainText(html: string): string {
  const document = new DOMParser().parseFromString(
    sanitizeEditorHtml(html),
    "text/html",
  );
  for (const image of document.querySelectorAll("img"))
    image.replaceWith(
      document.createTextNode(image.classList.contains("emojione") ? image.alt : ""),
    );
  for (const br of document.querySelectorAll("br"))
    br.replaceWith(document.createTextNode("\n"));
  for (const block of document.querySelectorAll("div,p"))
    if (block.nextSibling) block.append(document.createTextNode("\n"));
  return document.body.textContent || "";
}
export function clipboardNodes(
  clipboard: Pick<DataTransfer, "getData">,
  document: Document,
): Node[] {
  const internal = clipboard.getData("application/x-luobo-editor");
  if (internal) {
    const template = document.createElement("template");
    template.innerHTML = sanitizeEditorHtml(internal);
    return [...template.content.childNodes];
  }
  const plain = clipboard.getData("text/plain");
  // Text is never parsed as HTML, including literal markup copied from a message.
  if (plain) return [document.createTextNode(plain)];
  const template = document.createElement("template");
  template.innerHTML = sanitizeEditorHtml(clipboard.getData("text/html") || "");
  return [...template.content.childNodes];
}
export function activateRichAction(
  target: EventTarget | null,
  root: Document | HTMLElement,
  actions: {
    user(userID: string, groupID: string): void;
    edit(messageID: string): void;
  },
): boolean {
  if (!(target instanceof Element)) return false;
  const element = target.closest<HTMLElement>("[data-action]");
  if (!element || !root.contains(element)) return false;
  if (element.dataset.action === "user" && element.dataset.userId) {
    actions.user(element.dataset.userId, element.dataset.groupId || "");
    return true;
  }
  if (element.dataset.action === "edit-revoke" && element.dataset.messageId) {
    actions.edit(element.dataset.messageId);
    return true;
  }
  return false;
}
