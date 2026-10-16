import 'dart:async';

import 'package:flutter/material.dart';

import '../../../core/error/app_failure.dart';
import '../../../design/components/feedback.dart';
import '../../conversation/data/conversation_repository.dart';
import '../../conversation/domain/conversation.dart';
import '../../conversation/domain/recipient_identity.dart';
import '../../conversation/presentation/conversations_controller.dart';
import '../data/chat_repository.dart';
import '../domain/chat_snapshot.dart';
import '../domain/chat_batch.dart';

/// Holds the source session for the entire picker/confirmation/send sequence.
/// The source message is re-read after confirmation; its cached JSON alone is
/// insufficient permission to forward a deleted or changed message.
class ChatForwardPage extends StatefulWidget {
  // Only the separate merge constructor may omit a snapshot. Keep the
  // single-message public parameter non-null despite the nullable shared field.
  const ChatForwardPage({
    super.key,
    required this.source,
    required ChatFavoriteSnapshot snapshot,
  }) : snapshot = snapshot, // ignore: prefer_initializing_formals
       batch = null;
  const ChatForwardPage.merge({
    super.key,
    required this.source,
    required ChatBatchIntent selection,
  }) : snapshot = null,
       batch = selection;
  final ChatRepository source;
  final ChatFavoriteSnapshot? snapshot;
  final ChatBatchIntent? batch;

  @override
  State<ChatForwardPage> createState() => _ChatForwardPageState();
}

class _ChatForwardPageState extends State<ChatForwardPage> {
  ConversationsController? controller;
  String query = '';
  bool busy = false;
  bool dispatching = false, unconfirmed = false;
  StreamSubscription<void>? _sourcePrivacy;
  bool get current => widget.source.current && (widget.batch?.current ?? true);

  @override
  void initState() {
    super.initState();
    if (!current) return;
    final value = controller = ConversationsController(
      ConversationRepository(widget.source.gateway, widget.source.session),
    );
    value.addListener(changed);
    _sourcePrivacy = widget.source.gateway.privacyLedger.changes.listen(
      (_) => changed(),
    );
    unawaited(value.refresh());
  }

  void changed() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    unawaited(_sourcePrivacy?.cancel());
    controller?.removeListener(changed);
    controller?.dispose();
    super.dispose();
  }

  Future<void> send(Conversation destination) async {
    if (busy ||
        unconfirmed ||
        !current ||
        !destination.canSend ||
        !destination.hasRecipientIdentity) {
      return;
    }
    setState(() => busy = true);
    ChatRepository? target;
    var sendDispatched = false;
    try {
      if (!await confirmAction(
        context,
        '转发给 ${destination.title}？',
        '${destination.recipientIdentityLabel}\n\n${widget.batch == null ? '确认转发这条消息。' : '确认合并转发选中的 ${widget.batch!.messageIds.length} 条消息。'}',
        action: '发送',
      )) {
        return;
      }
      if (!mounted || !current) return;
      setState(() => dispatching = true);
      ChatFavoriteSnapshot? retained;
      if (widget.batch case final batch?) {
        batch.check();
      } else {
        final original = await widget.source.findSource(
          widget.snapshot!.messageId,
        );
        if (!mounted || !current) return;
        if (original == null) throw const AppFailure(FailureKind.dataChanged);
        if (!original.privacy.allowsRetention) {
          if (mounted && current) {
            showNotice(context, original.privacy.notice ?? '此消息暂不能保留');
          }
          return;
        }
        retained = await widget.source.favoriteSnapshot(
          widget.snapshot!.messageId,
        );
      }
      if (!mounted || !current) return;
      target = ChatRepository(
        gateway: widget.source.gateway,
        session: widget.source.session,
        conversationId: destination.id,
      );
      final actual = await target.conversation();
      if (!mounted || !current) return;
      if (actual.isGroup != destination.isGroup ||
          actual.recipientStableId != destination.recipientStableId) {
        throw const AppFailure(FailureKind.dataChanged);
      }
      if (widget.batch case final batch?) {
        batch.check();
        await target.forwardMergedSelection(
          source: widget.source,
          intent: batch,
          onDispatched: () => sendDispatched = true,
        );
      } else {
        await target.forwardMessageJson(retained!.content);
      }
      if (mounted && current) Navigator.pop(context, true);
    } catch (error) {
      if (sendDispatched) unconfirmed = true;
      if (mounted && current) {
        showNotice(
          context,
          unconfirmed
              ? '转发结果未确认，请先返回会话核对，不会自动重试。'
              : error is AppFailure
              ? error.message
              : '消息未转发',
        );
      }
    } finally {
      target?.clear();
      if (mounted) {
        setState(() {
          busy = false;
          dispatching = false;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final value = controller;
    if (!current || value == null) {
      return Scaffold(
        body: Center(
          child: Text(
            widget.batch == null ? '登录已失效，请重新登录' : '所选消息或会话已变化，请返回重新选择',
          ),
        ),
      );
    }
    final items = value.items
        .where(
          (item) =>
              item.canSend &&
              item.hasRecipientIdentity &&
              item.matchesRecipient(query),
        )
        .toList();
    return PopScope(
      canPop: !busy,
      child: Scaffold(
        appBar: AppBar(title: const Text('转发到聊天')),
        body: Column(
          children: [
            if (dispatching || value.loading) const LinearProgressIndicator(),
            if (unconfirmed)
              const Padding(
                padding: EdgeInsets.all(16),
                child: Text('转发结果未确认，请先返回会话核对，不会自动重试。'),
              ),
            Padding(
              padding: const EdgeInsets.all(16),
              child: TextField(
                enabled: !busy && !unconfirmed,
                onChanged: (text) =>
                    setState(() => query = text.trim().toLowerCase()),
                decoration: const InputDecoration(
                  labelText: '按名称、账号或群 ID 搜索已加载的聊天',
                  prefixIcon: Icon(Icons.search),
                ),
              ),
            ),
            Expanded(
              child: RefreshIndicator(
                onRefresh: busy ? () async {} : value.refresh,
                child: ListView(
                  physics: const AlwaysScrollableScrollPhysics(),
                  children: [
                    for (final item in items)
                      ListTile(
                        leading: Icon(
                          item.isGroup
                              ? Icons.groups_outlined
                              : Icons.person_outline,
                        ),
                        title: Text(item.title),
                        subtitle: Text(item.recipientIdentityLabel),
                        trailing: const Icon(Icons.send_outlined),
                        onTap: busy || unconfirmed ? null : () => send(item),
                      ),
                    if (value.failure case final failure?)
                      ListTile(
                        title: Text(failure.message),
                        trailing: TextButton(
                          onPressed: busy ? null : value.refresh,
                          child: const Text('重试'),
                        ),
                      ),
                    if (items.isEmpty &&
                        !value.loading &&
                        value.failure == null)
                      const Padding(
                        padding: EdgeInsets.all(24),
                        child: Center(child: Text('没有找到可转发的聊天')),
                      ),
                    if (value.hasMore)
                      TextButton(
                        onPressed: busy || value.loadingMore
                            ? null
                            : value.loadMore,
                        child: Text(value.loadingMore ? '正在加载…' : '加载更多聊天'),
                      ),
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
