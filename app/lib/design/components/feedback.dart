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
  bool Function()? isCurrent,
  Widget Function(Widget child)? guard,
}) async =>
    await showDialog<bool>(
      context: context,
      builder: (context) {
        final route = ModalRoute.of(context)!,
            navigator = Navigator.of(context);
        void finish(bool value) {
          if (route.isActive &&
              route.isCurrent &&
              navigator.mounted &&
              identical(route.navigator, navigator) &&
              (!value || isCurrent?.call() != false)) {
            navigator.pop(value);
          }
        }

        final dialog = AlertDialog(
          scrollable: true,
          title: Text(title),
          content: Text(text),
          actions: [
            TextButton(onPressed: () => finish(false), child: const Text('取消')),
            TextButton(onPressed: () => finish(true), child: Text(action)),
          ],
        );
        return guard?.call(dialog) ?? dialog;
      },
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
  bool Function()? isCurrent,
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
      isCurrent: isCurrent,
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
    this.isCurrent,
  });
  final String title, value, hint;
  final int maxLength, lines;
  final Future<void> Function(String)? save;
  final bool trim;
  final bool Function()? isCurrent;
  @override
  State<_InputDialog> createState() => _InputDialogState();
}

class _InputDialogState extends State<_InputDialog> {
  late final controller = TextEditingController(text: widget.value);
  bool saving = false;
  String? failure;
  ModalRoute<dynamic>? _route;
  NavigatorState? _navigator;
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _route ??= ModalRoute.of(context);
    _navigator ??= Navigator.of(context);
  }

  bool get _current =>
      mounted &&
      widget.isCurrent?.call() != false &&
      _route?.isActive == true &&
      _route?.isCurrent == true &&
      _navigator?.mounted == true &&
      identical(_route?.navigator, _navigator);
  void _finish([String? value]) {
    if (_current) _navigator!.pop(value);
  }

  Future<void> submit() async {
    if (saving || !_current) return;
    final value = widget.trim ? controller.text.trim() : controller.text;
    if (widget.save == null) {
      _finish(value);
      return;
    }
    setState(() {
      saving = true;
      failure = null;
    });
    try {
      await widget.save!(value);
      _finish(value);
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
        TextButton(onPressed: saving ? null : _finish, child: const Text('取消')),
        TextButton(
          onPressed: saving ? null : submit,
          child: Text(saving ? '保存中…' : '确定'),
        ),
      ],
    ),
  );
}
