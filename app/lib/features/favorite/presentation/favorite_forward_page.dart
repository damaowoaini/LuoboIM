import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../design/components/feedback.dart';
import '../../../providers.dart';
import '../../chat/data/chat_repository.dart';
import '../../conversation/data/conversation_repository.dart';
import '../../conversation/domain/conversation.dart';
import '../../conversation/domain/recipient_identity.dart';
import '../../conversation/presentation/conversations_controller.dart';
import '../data/favorite_repository.dart';
import '../domain/favorite.dart';

class FavoriteForwardPage extends ConsumerStatefulWidget {
  const FavoriteForwardPage({
    super.key,
    required this.item,
    required this.repository,
  });
  final FavoriteItem item;
  final FavoriteRepository repository;
  @override
  ConsumerState<FavoriteForwardPage> createState() =>
      _FavoriteForwardPageState();
}

class _FavoriteForwardPageState extends ConsumerState<FavoriteForwardPage> {
  late final ConversationsController controller;
  MessageHistoryAccess? sourceAccess;
  StreamSubscription<void>? privacy;
  bool initialized = false;
  String query = '';
  bool sending = false;
  bool dispatching = false;

  @override
  void initState() {
    super.initState();
    if (!widget.repository.isCurrent ||
        !widget.item.canUseContent ||
        !widget.item.decoded.canForward) {
      return;
    }
    sourceAccess = ref
        .read(imGatewayProvider)
        .historyAccess(
          widget.repository.api.access.generation,
          conversationId: widget.item.conversationId.isEmpty
              ? null
              : widget.item.conversationId,
        );
    privacy = widget.repository.privacyChanges?.listen((_) => changed());
    controller = ConversationsController(
      ConversationRepository(
        ref.read(imGatewayProvider),
        widget.repository.api.access.session,
      ),
    )..addListener(changed);
    initialized = true;
    if (widget.repository.isCurrent) unawaited(controller.refresh());
  }

  void changed() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    unawaited(privacy?.cancel());
    if (initialized) {
      controller.removeListener(changed);
      controller.dispose();
    }
    super.dispose();
  }

  void checkSource() {
    widget.repository.checkContent(widget.item);
    sourceAccess?.check();
    if (!widget.item.canUseContent || !widget.item.decoded.canForward) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
  }

  Future<void> send(Conversation target) async {
    final repository = widget.repository;
    if (sending ||
        !repository.isCurrent ||
        !widget.item.canUseContent ||
        !widget.item.decoded.canForward ||
        !target.canSend ||
        !target.hasRecipientIdentity) {
      return;
    }
    setState(() => sending = true);
    ChatRepository? chat;
    try {
      checkSource();
      if (!await confirmAction(
        context,
        '转发给 ${target.title}？',
        '${target.recipientIdentityLabel}\n\n确认转发这条消息。',
        action: '发送',
      )) {
        return;
      }
      if (!mounted) return;
      checkSource();
      setState(() => dispatching = true);
      chat = ChatRepository(
        gateway: controller.repository.gateway,
        session: repository.api.access.session,
        conversationId: target.id,
      );
      final actual = await chat.conversation();
      if (!mounted) return;
      checkSource();
      if (actual.isGroup != target.isGroup ||
          actual.recipientStableId != target.recipientStableId) {
        throw const AppFailure(FailureKind.dataChanged);
      }
      await chat.forwardMessageJson(
        widget.item.content,
        checkSource: checkSource,
      );
      if (mounted && repository.isCurrent) Navigator.pop(context, true);
    } on AppFailure catch (error) {
      if (mounted && repository.isCurrent) showNotice(context, error.message);
    } catch (_) {
      if (mounted && repository.isCurrent) showNotice(context, '消息未发送，请重试');
    } finally {
      chat?.clear();
      if (mounted) {
        setState(() {
          sending = false;
          dispatching = false;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    if (!widget.repository.isCurrent) {
      return const Scaffold(body: Center(child: Text('登录已失效，请重新登录')));
    }
    if (!widget.item.canUseContent || !widget.item.decoded.canForward) {
      return Scaffold(
        appBar: AppBar(title: const Text('转发到聊天')),
        body: Center(
          child: Text(widget.item.decoded.privacy.notice ?? '这条收藏不能作为原消息转发'),
        ),
      );
    }
    if (!initialized || !widget.repository.isCurrent) {
      return const Scaffold(body: Center(child: Text('登录已失效，请重新登录')));
    }
    final items = controller.items
        .where(
          (item) =>
              item.canSend &&
              item.hasRecipientIdentity &&
              item.matchesRecipient(query),
        )
        .toList();
    return PopScope(
      canPop: !sending,
      child: Scaffold(
        appBar: AppBar(title: const Text('转发到聊天')),
        body: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 800),
            child: Column(
              children: [
                if (dispatching || controller.loading)
                  const LinearProgressIndicator(),
                Padding(
                  padding: const EdgeInsets.all(16),
                  child: TextField(
                    enabled: !sending,
                    onChanged: (value) =>
                        setState(() => query = value.trim().toLowerCase()),
                    decoration: const InputDecoration(
                      labelText: '按名称、账号或群 ID 搜索已加载的聊天',
                      prefixIcon: Icon(Icons.search),
                    ),
                  ),
                ),
                Expanded(
                  child: RefreshIndicator(
                    onRefresh: controller.refresh,
                    child: ListView.builder(
                      physics: const AlwaysScrollableScrollPhysics(),
                      padding: const EdgeInsets.symmetric(horizontal: 16),
                      itemCount: items.length + 1,
                      itemBuilder: (context, index) {
                        if (index == items.length) {
                          return Column(
                            children: [
                              if (controller.failure != null)
                                ErrorPanel(
                                  error: controller.failure!,
                                  retry: controller.refresh,
                                ),
                              if (items.isEmpty &&
                                  !controller.loading &&
                                  controller.failure == null)
                                const Padding(
                                  padding: EdgeInsets.all(24),
                                  child: Text('没有找到可转发的聊天。'),
                                ),
                              if (controller.hasMore)
                                TextButton(
                                  onPressed: sending || controller.loadingMore
                                      ? null
                                      : controller.loadMore,
                                  child: Text(
                                    controller.loadingMore ? '正在加载…' : '加载更多聊天',
                                  ),
                                ),
                            ],
                          );
                        }
                        final item = items[index];
                        return ListTile(
                          leading: Icon(
                            item.isGroup
                                ? Icons.groups_outlined
                                : Icons.person_outline,
                          ),
                          title: Text(item.title),
                          subtitle: Text(item.recipientIdentityLabel),
                          trailing: const Icon(Icons.send_outlined),
                          onTap: sending ? null : () => send(item),
                        );
                      },
                    ),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
