/** A selection can span several bubbles while the copy event targets their
 * parent. Check all protected message regions, not only event.target. */
export function blockPrivateSelectionCopy(event: Pick<ClipboardEvent,"preventDefault">, page: Document = document): boolean {
  const selection = page.getSelection();
  if (!selection || selection.isCollapsed) return false;
  for (const element of page.querySelectorAll('[data-message-retention="blocked"]')) {
    for (let index=0;index<selection.rangeCount;index++) {
      try {
        if (selection.getRangeAt(index).intersectsNode(element)) { event.preventDefault(); return true; }
      } catch { event.preventDefault(); return true; }
    }
  }
  return false;
}
