import 'package:flutter/material.dart';

class ChatBatchToolbar extends StatelessWidget {
  const ChatBatchToolbar({
    super.key,
    required this.count,
    required this.busy,
    required this.canMerge,
    required this.onDelete,
    required this.onMerge,
    required this.onCancel,
  });
  final int count;
  final bool busy, canMerge;
  final VoidCallback onDelete, onMerge, onCancel;
  @override
  Widget build(BuildContext context) => SafeArea(
    top: false,
    child: Material(
      child: Padding(
        padding: const EdgeInsets.all(8),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (busy) const LinearProgressIndicator(),
            Wrap(
              alignment: WrapAlignment.center,
              spacing: 8,
              children: [
                TextButton.icon(
                  onPressed: busy || count == 0 ? null : onDelete,
                  icon: const Icon(Icons.delete_outline),
                  label: Text('删除（$count）'),
                ),
                TextButton.icon(
                  onPressed: busy || !canMerge ? null : onMerge,
                  icon: const Icon(Icons.forward_to_inbox_outlined),
                  label: const Text('合并转发'),
                ),
                TextButton(
                  onPressed: onCancel,
                  child: Text(busy ? '停止后续操作' : '取消多选'),
                ),
              ],
            ),
            if (busy) const Text('已提交的请求无法取消；停止后会先核对结果。'),
          ],
        ),
      ),
    ),
  );
}

class ChatSelectionRow extends StatelessWidget {
  const ChatSelectionRow({
    super.key,
    required this.active,
    required this.eligible,
    required this.selected,
    required this.onToggle,
    required this.child,
  });
  final bool active, eligible, selected;
  final VoidCallback onToggle;
  final Widget child;
  @override
  Widget build(BuildContext context) {
    if (!active) return child;
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SizedBox(
          width: 48,
          child: eligible
              ? Checkbox(
                  value: selected,
                  semanticLabel: '选择这条消息',
                  onChanged: (_) => onToggle(),
                )
              : null,
        ),
        Expanded(
          child: GestureDetector(
            behavior: HitTestBehavior.opaque,
            onTap: eligible ? onToggle : null,
            child: IgnorePointer(child: child),
          ),
        ),
      ],
    );
  }
}
