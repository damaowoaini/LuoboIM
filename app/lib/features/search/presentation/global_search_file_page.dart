import 'dart:async';
import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../core/error/app_failure.dart';
import '../../chat/domain/chat_message.dart';
import '../../chat/presentation/message_privacy_content.dart';
import '../domain/global_search.dart';
import 'global_search_controller.dart';
import 'global_search_matches_page.dart';

class GlobalSearchFilePage extends StatefulWidget {
  const GlobalSearchFilePage({super.key, required this.controller,
      required this.hit, this.openExternal});
  final GlobalSearchController controller;
  final GlobalMessageHit hit;
  final Future<bool> Function(Uri)? openExternal;
  @override
  State<GlobalSearchFilePage> createState() => _GlobalSearchFilePageState();
}

class _GlobalSearchFilePageState extends State<GlobalSearchFilePage> {
  ChatMessage? message;
  AppFailure? failure;
  bool loading = false, opening = false;
  int _viewEpoch = 0;
  bool get current => mounted && widget.controller.usable &&
      widget.hit.visibleAt(widget.controller.repository.now());
  bool get active => current && ModalRoute.of(context)?.isCurrent == true;
  @override
  void initState() { super.initState(); unawaited(load()); }
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (ModalRoute.of(context)?.isCurrent == false) ++_viewEpoch;
  }
  Future<void> load() async {
    if (!current || loading) return;
    setState(() { loading = true; failure = null; });
    try {
      final value = await widget.controller.repository.readExact(widget.hit);
      if (current) {
        if (value.kind != ChatMessageKind.file) {
          throw const AppFailure(FailureKind.invalidResponse);
        }
        setState(() => message = value);
      }
    } on AppFailure catch (error) {
      if (current) setState(() => failure = error);
    } finally {
      if (mounted) setState(() => loading = false);
    }
  }
  Future<void> open() async {
    if (!active || opening || loading) return;
    final view = _viewEpoch;
    final route = ModalRoute.of(context);
    bool intentCurrent() => active && view == _viewEpoch &&
        identical(route, ModalRoute.of(context));
    setState(() => opening = true);
    try {
      final value = await widget.controller.repository.readExact(widget.hit);
      final uri = value.media;
      if (!intentCurrent() || !value.privacy.canRetainAt(widget.controller.repository.now())) return;
      if (value.kind != ChatMessageKind.file || uri == null || uri.scheme != 'https') {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      // From this dispatch the OS operation is started, not cancelable by UI.
      final opened = await (widget.openExternal?.call(uri) ??
          launchUrl(uri, mode: LaunchMode.externalApplication));
      if (mounted && !opened && intentCurrent()) {
        ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('无法打开文件，请稍后重试')));
      }
    } on AppFailure catch (error) {
      if (mounted && intentCurrent()) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(error.message)));
    } catch (_) {
      if (mounted && intentCurrent()) ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('无法打开文件，请稍后重试')));
    } finally {
      if (mounted) setState(() => opening = false);
    }
  }
  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.controller,
    builder: (context, _) {
      final value = message;
      return Scaffold(
        appBar: AppBar(title: const Text('文件详情')),
        body: !current
            ? const Center(child: Text('结果已不可用，请返回重新搜索'))
            : ListView(padding: const EdgeInsets.all(20), children: [
                if (loading) const LinearProgressIndicator(),
                if (failure != null) ...[
                  Text(failure!.message),
                  TextButton(onPressed: loading ? null : load, child: const Text('重试读取')),
                ],
                if (value != null)
                  MessagePrivacyContent(
                    privacy: value.privacy, now: widget.controller.repository.now,
                    child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
                      const Icon(Icons.insert_drive_file_outlined, size: 56),
                      const SizedBox(height: 16),
                      Text(value.privacy.allowsRetention ? value.fileName ?? '文件' : '[文件隐私状态受限]'),
                      if (value.privacy.allowsRetention && value.fileBytes != null)
                        Text('${value.fileBytes} 字节'),
                      Text('来自 ${widget.hit.title}'),
                      Text('会话 ID：${widget.hit.conversationId}'),
                      Text('${value.senderName} · ${value.sentAt.toLocal()}'),
                      const SizedBox(height: 16),
                      if (!value.privacy.allowsRetention)
                        const Text('阅后即焚或隐私状态无法确认的文件不能交给外部应用'),
                      FilledButton.icon(onPressed: opening || loading || !value.privacy.canRetainAt(widget.controller.repository.now())
                          ? null : open,
                          icon: const Icon(Icons.open_in_new), label: Text(opening ? '正在打开' : '使用外部应用打开')),
                      OutlinedButton(onPressed: () {
                        if (active) openGlobalSource(context, widget.controller.repository.selection(widget.hit));
                      }, child: const Text('查看原消息')),
                    ]),
                  ),
              ]),
      );
    },
  );
}
