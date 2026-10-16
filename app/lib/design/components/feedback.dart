import 'package:flutter/material.dart';

import '../../core/error/app_failure.dart';

class ErrorPanel extends StatelessWidget {
  const ErrorPanel({super.key, required this.error, required this.retry});
  final Object error;
  final VoidCallback retry;
  @override
  Widget build(BuildContext context) => Center(
    child: Padding(
      padding: const EdgeInsets.all(28),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            error is AppFailure ? (error as AppFailure).message : '暂时无法读取，请重试',
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 12),
          TextButton.icon(
            onPressed: retry,
            icon: const Icon(Icons.refresh),
            label: const Text('重试'),
          ),
        ],
      ),
    ),
  );
}

void showNotice(BuildContext context, String text) {
  if (context.mounted) {
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(text)));
  }
}

Future<bool> confirmAction(
  BuildContext context,
  String title,
  String text, {
  String action = '确定',
}) async =>
    await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        scrollable: true,
        title: Text(title),
        content: Text(text),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context, true),
            child: Text(action),
          ),
        ],
      ),
    ) ??
    false;

Future<String?> promptText(
  BuildContext context,
  String title, {
  String value = '',
  String hint = '',
  int maxLength = 128,
  int lines = 1,
  Future<void> Function(String)? save,
  bool trim = true,
  Widget Function(Widget child)? guard,
}) => showDialog<String>(
  context: context,
  builder: (context) {
    final dialog = _InputDialog(
      title: title,
      value: value,
      hint: hint,
      maxLength: maxLength,
      lines: lines,
      save: save,
      trim: trim,
    );
    return guard?.call(dialog) ?? dialog;
  },
);

class _InputDialog extends StatefulWidget {
  const _InputDialog({
    required this.title,
    required this.value,
    required this.hint,
    required this.maxLength,
    required this.lines,
    this.save,
    this.trim = true,
  });
  final String title, value, hint;
  final int maxLength, lines;
  final Future<void> Function(String)? save;
  final bool trim;
  @override
  State<_InputDialog> createState() => _InputDialogState();
}

class _InputDialogState extends State<_InputDialog> {
  late final controller = TextEditingController(text: widget.value);
  bool saving = false;
  String? failure;
  Future<void> submit() async {
    if (saving) return;
    final value = widget.trim ? controller.text.trim() : controller.text;
    if (widget.save == null) {
      Navigator.pop(context, value);
      return;
    }
    setState(() {
      saving = true;
      failure = null;
    });
    try {
      await widget.save!(value);
      if (mounted) Navigator.pop(context, value);
    } catch (error) {
      if (mounted) {
        setState(
          () => failure = error is AppFailure ? error.message : '保存失败，请重试',
        );
      }
    } finally {
      if (mounted) setState(() => saving = false);
    }
  }

  @override
  void dispose() {
    controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => PopScope(
    canPop: !saving,
    child: AlertDialog(
      scrollable: true,
      title: Text(widget.title),
      content: TextField(
        controller: controller,
        enabled: !saving,
        autofocus: true,
        maxLength: widget.maxLength,
        minLines: widget.lines,
        maxLines: widget.lines,
        decoration: InputDecoration(
          hintText: widget.hint,
          errorText: failure,
          errorMaxLines: 3,
        ),
      ),
      actions: [
        TextButton(
          onPressed: saving ? null : () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        TextButton(
          onPressed: saving ? null : submit,
          child: Text(saving ? '保存中…' : '确定'),
        ),
      ],
    ),
  );
}
