// @vitest-environment jsdom
import { expect, it, vi } from "vitest";
import {
  activateRichAction,
  clipboardNodes,
  editorPlainText,
  sanitizeEditorHtml,
  sanitizeHtml,
} from "./html";
const payload =
  '<img src=x onerror="globalThis.injected=1"><svg onload="globalThis.injected=1"></svg><script>globalThis.injected=1</script>';
it("removes inline handlers and executable URLs while retaining controlled member actions", () => {
  const element = document.createElement("div");
  element.innerHTML = sanitizeHtml(
    `<span onclick="globalThis.injected=1" data-action="user" data-user-id="literal&amp;quot;payload" data-group-id="group">成员</span><a href="javascript:alert(1)">link</a>${payload}`,
  );
  expect(element.querySelector("[onclick],[onerror],[onload],script,svg")).toBeNull();
  expect(element.querySelector("a")?.hasAttribute("href")).toBe(false);
  const user = vi.fn();
  expect(
    activateRichAction(element.querySelector("span"), element, { user, edit: vi.fn() }),
  ).toBe(true);
  expect(user).toHaveBeenCalledWith("literal&quot;payload", "group");
});
it("pastes text/plain markup literally and does not reinterpret it during an editor round trip", () => {
  const literal = "<img src=x onerror=alert(1)> & 😀\nnext";
  const node = clipboardNodes(
    {
      getData: (type) =>
        type === "text/plain" ? literal : type === "text/html" ? payload : "",
    },
    document,
  )[0];
  expect(node.nodeType).toBe(Node.TEXT_NODE);
  const element = document.createElement("div");
  element.append(node);
  element.innerHTML = sanitizeEditorHtml(element.innerHTML);
  expect(element.querySelector("img")).toBeNull();
  expect(editorPlainText(element.innerHTML)).toBe(literal);
});
it("cleans HTML-only paste and retains internal mention metadata without handler code", () => {
  const element = document.createElement("div");
  element.append(
    ...clipboardNodes(
      { getData: (type) => (type === "text/html" ? payload : "") },
      document,
    ),
  );
  expect(element.querySelector("script,svg,[onerror],[onload]")).toBeNull();
  const internal =
    '<b class="at-el" contenteditable="false" data-id="user-1" data-name="名字" onclick="alert(1)">@名字 </b>';
  element.replaceChildren(
    ...clipboardNodes(
      {
        getData: (type) =>
          type === "application/x-luobo-editor" ? internal : "@名字 ",
      },
      document,
    ),
  );
  expect(element.querySelector("b")?.dataset.id).toBe("user-1");
  expect(element.querySelector("[onclick]")).toBeNull();
  expect(editorPlainText(element.innerHTML)).toBe("@名字 ");
});
it("converts emoji and line breaks to the actual message text rather than HTML entities", () => {
  expect(
    editorPlainText(
      '&lt;hello&gt; &amp;<br><img class="emojione" alt="😀" src="https://cdn.example/emoji.png">',
    ),
  ).toBe("<hello> &\n😀");
});
