import 'dart:async';

import 'package:flutter/material.dart';

import '../domain/message_privacy.dart';

/// Hides known-expired content and disposes media children. This is a UI cache
/// boundary, not evidence that native SQLite/files have been physically erased.
class MessagePrivacyContent extends StatefulWidget {
  const MessagePrivacyContent({
    super.key,
    required this.privacy,
    required this.child,
    this.now,
    this.onExpired,
    this.expired,
    this.retainableOnly = false,
  });
  final MessagePrivacy privacy;
  final Widget child;
  final DateTime Function()? now;
  final VoidCallback? onExpired;
  final Widget? expired;
  final bool retainableOnly;
  @override
  State<MessagePrivacyContent> createState() => _MessagePrivacyContentState();
}

class _MessagePrivacyContentState extends State<MessagePrivacyContent>
    with WidgetsBindingObserver {
  Timer? timer;
  StreamSubscription<void>? subscription;
  void bind() {
    unawaited(subscription?.cancel());
    subscription = widget.privacy.changes?.listen((_) {
      if (mounted) setState(schedule);
    });
  }

  bool notified = false;
  DateTime get now => (widget.now ?? DateTime.now)();
  bool get allowed =>
      widget.privacy.canViewAt(now) &&
      (!widget.retainableOnly || widget.privacy.canRetainAt(now));
  void schedule() {
    timer?.cancel();
    if (notified) return;
    if (!allowed) {
      if (!notified) {
        notified = true;
        widget.onExpired?.call();
      }
      return;
    }
    final remaining = widget.privacy.millisecondsUntilExpiry(now);
    if (remaining == null) return;
    final delay = (remaining + 1).clamp(1, 86400000);
    timer = Timer(Duration(milliseconds: delay), () {
      if (!mounted) return;
      setState(schedule);
    });
  }

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    bind();
    schedule();
  }

  @override
  void didUpdateWidget(covariant MessagePrivacyContent oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!identical(oldWidget.privacy.changes, widget.privacy.changes)) bind();
    schedule();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) setState(schedule);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    timer?.cancel();
    unawaited(subscription?.cancel());
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => notified || !allowed
      ? widget.expired ?? const Text('阅后即焚内容已到期')
      : widget.child;
}
