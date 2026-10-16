import { sanitizeEditorHtml, clipboardNodes } from "@/core/content/html";
import type { JSX } from "react";
import deepEqual from "fast-deep-equal";
import * as React from "react";

import styles from "./editable-div.module.scss";

let savedRange: Range | null = null;

function normalizeHtml(str: string): string {
  return str && str.replace(/&nbsp;|\u202F|\u00A0/g, " ");
}

export function unescapeHTMLTags(str: string) {
  return str.replace(/&lt;/g, "<").replace(/&gt;/g, ">");
}

function replaceCaret(el: HTMLElement) {
  // Place the caret at the end of the element
  const target = document.createTextNode("");
  el.appendChild(target);
  // do not move caret if element was not focused
  const isTargetFocused = document.activeElement === el;
  if (target !== null && target.nodeValue !== null && isTargetFocused) {
    const sel = window.getSelection();
    if (sel !== null) {
      const range = document.createRange();
      range.setStart(target, target.nodeValue.length);
      range.collapse(true);
      sel.removeAllRanges();
      sel.addRange(range);
    }
    if (el instanceof HTMLElement) el.focus();
  }
}

export function deleteBeforeAt() {
  restoreSelection();
  const selection = window.getSelection();
  if (!selection?.isCollapsed) return;

  const range = document.createRange();
  const anchorNode = selection.anchorNode;
  if (!anchorNode || !(anchorNode instanceof Text)) return;

  const text = anchorNode.textContent!;
  const offset = selection.anchorOffset;
  const lastIndex = text.lastIndexOf("@", offset - 1);

  if (lastIndex !== -1) {
    range.setStart(anchorNode, lastIndex);
  } else {
    range.setStart(anchorNode, 0);
  }
  range.setEnd(anchorNode, offset);
  range.deleteContents();
  selection.removeAllRanges();
  selection.addRange(range);

  savedRange = range.cloneRange();
}

function saveSelection() {
  const selection = window.getSelection();
  if ((selection?.rangeCount ?? 0) > 0) {
    savedRange = selection?.getRangeAt(0).cloneRange() ?? null;
  }
}

export function restoreSelection() {
  const selection = window.getSelection();
  if (savedRange && selection) {
    selection.removeAllRanges();
    selection.addRange(savedRange);
  }
}

export function insertAtCursor(nodes: Node[]) {
  const inputElement = document.getElementById("editable-div");
  if (!inputElement) {
    console.error("Element with id 'editable-div' not found.");
    return;
  }
  inputElement.focus();
  restoreSelection();
  const selection = window.getSelection();
  if (!selection || selection.rangeCount === 0) {
    inputElement.append(...nodes); // Preserve the complete clipboard fragment.
    return;
  }

  const range = selection.getRangeAt(0).cloneRange();

  if (!inputElement.contains(range.commonAncestorContainer)) {
    console.error("Selection is not inside the editable-div.");
    return;
  }

  range.deleteContents();
  nodes.forEach((node) => {
    range.insertNode(node);
    range.setStartAfter(node);
  });

  range.collapse(false);
  selection.removeAllRanges();
  selection.addRange(range);

  savedRange = range.cloneRange();
}
/**
 * A simple component for an html element with editable contents.
 */
export default class EditableDiv extends React.Component<Props> {
  lastHtml: string = this.props.html;
  el: any =
    typeof this.props.innerRef === "function"
      ? { current: null }
      : React.createRef<HTMLElement>();

  getEl = () =>
    (this.props.innerRef && typeof this.props.innerRef !== "function"
      ? this.props.innerRef
      : this.el
    ).current as HTMLDivElement;

  onBlur = (e: React.FocusEvent<HTMLDivElement, Element>) => {
    const emitFunc = this.props.onBlur || this.emitChange;
    emitFunc(e);
    const selection = window.getSelection();
    if (selection && selection.rangeCount > 0) {
      savedRange = selection.getRangeAt(0).cloneRange();
    }
  };

  saveRange = (e: React.SyntheticEvent<any>) => {
    this.emitChange(e);
    saveSelection();
  };

  render() {
    const { tagName, html, innerRef, ...props } = this.props;

    return React.createElement(
      tagName || "div",
      {
        ...props,
        ref:
          typeof innerRef === "function"
            ? (current: HTMLElement) => {
                innerRef(current);
                this.el.current = current;
              }
            : innerRef || this.el,
        onInput: this.emitChange,
        onBlur: this.onBlur,
        onKeyUp: this.props.onKeyUp || this.saveRange,
        onMouseUp: this.props.onMouseUp || this.saveRange,
        onKeyDown: this.props.onKeyDown || this.emitChange,
        onPaste: this.props.onPaste || this.pasteLint,
        onCopy: this.props.onCopy || this.onCopy,
        onDoubleClick: (event: React.MouseEvent) => {
          const image =
            event.target instanceof HTMLImageElement ? event.target : undefined;
          if (image?.classList.contains("image-el") && image.src.startsWith("blob:"))
            window.screenshotPreview?.(image.src);
        },
        contentEditable: !this.props.limit,
        dangerouslySetInnerHTML: { __html: sanitizeEditorHtml(html) },
        className: `${styles.input} ${this.props.className}`,
      },
      this.props.children,
    );
  }

  shouldComponentUpdate(nextProps: Props): boolean {
    const { props } = this;
    const el = this.getEl();

    // We need not rerender if the change of props simply reflects the user's edits.
    // Rerendering in this case would make the cursor/caret jump

    // Rerender if there is no element yet... (somehow?)
    if (!el) return true;

    // ...or if html really changed... (programmatically, not by user edit)
    if (normalizeHtml(nextProps.html) !== normalizeHtml(el.innerHTML)) {
      return true;
    }

    // Handle additional properties
    return (
      props.limit !== nextProps.limit ||
      props.tagName !== nextProps.tagName ||
      props.className !== nextProps.className ||
      props.innerRef !== nextProps.innerRef ||
      props.placeholder !== nextProps.placeholder ||
      !deepEqual(props.style, nextProps.style)
    );
  }

  componentDidUpdate() {
    const el = this.getEl();
    if (!el) return;

    // Perhaps React (whose VDOM gets outdated because we often prevent
    // rerendering) did not update the DOM. So we update it manually now.
    const safeHtml = sanitizeEditorHtml(this.props.html);
    if (safeHtml !== el.innerHTML) {
      el.innerHTML = safeHtml;
    }
    this.lastHtml = safeHtml;
    replaceCaret(el);
  }

  onCopy = (e: React.ClipboardEvent<HTMLInputElement>) => {
    e.preventDefault();

    const selection = window.getSelection();
    if (!selection?.rangeCount) return;
    const range = selection.getRangeAt(0);

    const tempEl = document.createElement("div");
    tempEl.appendChild(range!.cloneContents());

    e.clipboardData.setData("text/html", tempEl.innerHTML);
    e.clipboardData.setData(
      "application/x-luobo-editor",
      sanitizeEditorHtml(tempEl.innerHTML),
    );
    e.clipboardData.setData("text/plain", tempEl.textContent!);
  };

  pasteLint = (e: React.ClipboardEvent<HTMLInputElement>) => {
    const clp = e.clipboardData;
    e.preventDefault();
    const selection = window.getSelection();
    const isFullSelection = selection?.toString() === this.getEl().textContent;

    if (isFullSelection) {
      this.getEl().textContent = "";
    }

    // Handle image paste
    const items = clp?.items;
    if (items) {
      for (let i = 0; i < items.length; i++) {
        const item = items[i];
        if (item.type.indexOf("image") !== -1) {
          const blob = item.getAsFile();
          if (blob) {
            const objUrl = URL.createObjectURL(blob);
            const image = new Image();
            image.setAttribute("class", "image-el");
            image.src = objUrl;
            insertAtCursor([image]);
            this.emitChange(e);
            return;
          }
        }
      }
    }

    insertAtCursor(clipboardNodes(clp, document));
    this.emitChange(e);
  };

  emitChange = (originalEvt: React.SyntheticEvent<any>) => {
    const el = this.getEl();
    if (!el) return;

    const html = el.innerHTML;
    if (this.props.onChange && html !== this.lastHtml) {
      // Clone event with Object.assign to avoid
      // "Cannot assign to read only property 'target' of object"
      const evt = Object.assign({}, originalEvt, {
        target: {
          value: html,
        },
      });
      this.props.onChange(evt);
    }
    this.lastHtml = html;
  };
}

export type EditableDivEvent = React.SyntheticEvent<any, Event> & {
  target: { value: string };
};
type Modify<T, R> = Pick<T, Exclude<keyof T, keyof R>> & R;
type DivProps = Modify<
  JSX.IntrinsicElements["div"],
  { onChange: (event: EditableDivEvent) => void }
>;

export interface Props extends DivProps {
  placeholder?: string;
  html: string;
  limit?: string;
  tagName?: string;
  className?: string;
  style?: React.CSSProperties;
  innerRef?: React.RefObject<HTMLElement> | ((instance: HTMLElement) => void);
}
