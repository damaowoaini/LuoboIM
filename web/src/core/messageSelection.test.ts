// @vitest-environment jsdom
import {afterEach,describe,expect,it,vi} from "vitest";
import {blockPrivateSelectionCopy} from "./messageSelection";
afterEach(()=>{document.body.replaceChildren();document.getSelection()?.removeAllRanges();});
function select(start:Node,end:Node){const range=document.createRange();range.setStart(start,0);range.setEnd(end,end.textContent?.length??0);document.getSelection()?.removeAllRanges();document.getSelection()?.addRange(range);}
describe("actual cross-bubble browser selection",()=>{
 it("blocks a parent-targeted selection that includes a private bubble between ordinary messages",()=>{
  document.body.innerHTML='<p id="before">normal first</p><p data-message-retention="blocked">private</p><p id="after">normal last</p>';
  select(document.getElementById("before")!.firstChild!,document.getElementById("after")!.firstChild!);
  const event={preventDefault:vi.fn()};expect(blockPrivateSelectionCopy(event)).toBe(true);expect(event.preventDefault).toHaveBeenCalledOnce();
 });
 it("preserves copying an ordinary selection outside protected messages",()=>{
  document.body.innerHTML='<p id="normal">normal</p><p data-message-retention="blocked">private</p>';
  select(document.getElementById("normal")!.firstChild!,document.getElementById("normal")!.firstChild!);
  const event={preventDefault:vi.fn()};expect(blockPrivateSelectionCopy(event)).toBe(false);expect(event.preventDefault).not.toHaveBeenCalled();
 });
});
