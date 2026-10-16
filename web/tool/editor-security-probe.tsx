import { createRoot } from "react-dom/client";
import { useState } from "react";
import EditableDiv from "../src/components/EditableDiv";
import { editorPlainText } from "../src/core/content/html";
const App = () => {
  const [html, setHtml] = useState(""),
    [result, setResult] = useState("尚未验证");
  return (
    <main style={{ fontFamily: "sans-serif", maxWidth: 900, margin: "30px auto" }}>
      <h1>真实消息编辑器安全验收</h1>
      <p>独立测试页，仅本地合成内容；不连接SDK或业务服务。</p>
      <button
        onClick={() =>
          setHtml(
            '<b class="at-el" contenteditable="false" data-id="synthetic-user" data-name="合成成员" onclick="globalThis.editorInjected=true">@合成成员 </b><img src=x onerror="globalThis.editorInjected=true"><script>globalThis.editorInjected=true</script>',
          )
        }
      >
        填入攻击草稿样例
      </button>
      <EditableDiv
        id="editable-div"
        role="textbox"
        aria-label="消息编辑器"
        html={html}
        onChange={(event) => setHtml(event.target.value)}
        style={{
          border: "1px solid #2163e8",
          padding: 16,
          minHeight: 100,
          whiteSpace: "pre-wrap",
          margin: "20px 0",
        }}
      />
      <button
        onClick={() => {
          const editor = document.getElementById("editable-div")!;
          const passed =
            !editor.querySelector("[onclick],[onerror],[onload],script,svg,iframe") &&
            !(window as unknown as { editorInjected?: boolean }).editorInjected;
          setResult(
            JSON.stringify(
              {
                status: passed ? "PASS" : "FAIL",
                text: editorPlainText(editor.innerHTML),
                html: editor.innerHTML,
                mentionPreserved: !!editor.querySelector(
                  'b.at-el[data-id="synthetic-user"]',
                ),
                limit: "真实React编辑器与浏览器clipboard；未连接SDK/业务服务。",
              },
              null,
              2,
            ),
          );
        }}
      >
        验证当前编辑内容
      </button>
      <pre role="status" style={{ whiteSpace: "pre-wrap" }}>
        {result}
      </pre>
    </main>
  );
};
createRoot(document.getElementById("root")!).render(<App />);
