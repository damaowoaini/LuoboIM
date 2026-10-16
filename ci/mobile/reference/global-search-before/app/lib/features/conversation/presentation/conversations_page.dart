import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/config/app_config.dart';
import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../design/tokens.dart';
import '../../../providers.dart';
import '../data/conversation_repository.dart';
import '../domain/conversation.dart';
import 'conversations_controller.dart';

class ConversationsPage extends ConsumerStatefulWidget {
  const ConversationsPage({super.key});
  @override
  ConsumerState<ConversationsPage> createState() => _ConversationsPageState();
}

class _ConversationsPageState extends ConsumerState<ConversationsPage> {
  late final ConversationsController controller;
  final search = TextEditingController();
  bool searching = false;

  @override
  void initState() {
    super.initState();
    controller = ConversationsController(
      ConversationRepository(
        ref.read(imGatewayProvider),
        ref.read(appRuntimeProvider).session,
      ),
    );
    controller.addListener(_changed);
    unawaited(controller.refresh());
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    controller.removeListener(_changed);
    controller.dispose();
    search.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final query = search.text.trim().toLowerCase();
    final items = controller.items
        .where(
          (item) =>
              query.isEmpty ||
              item.title.toLowerCase().contains(query) ||
              item.preview.toLowerCase().contains(query),
        )
        .toList();
    return Scaffold(
      appBar: AppBar(
        title: const Text(
          Brand.name,
          style: TextStyle(fontWeight: FontWeight.w700),
        ),
        actions: [
          IconButton(
            tooltip: '搜索当前会话',
            icon: const Icon(Icons.search),
            onPressed: () => setState(() {
              searching = !searching;
              if (!searching) search.clear();
            }),
          ),
          PopupMenuButton<String>(
            tooltip: '更多操作',
            onSelected: (value) async {
              if (value == 'meetings') {
                context.push('/meetings');
                return;
              }
              if (value == 'logout') {
                final confirmed = await showDialog<bool>(
                  context: context,
                  builder: (context) => AlertDialog(
                    title: const Text('退出登录'),
                    content: const Text('退出后可以重新登录，聊天记录会保留在此账号下。'),
                    actions: [
                      TextButton(
                        onPressed: () => Navigator.pop(context, false),
                        child: const Text('取消'),
                      ),
                      FilledButton(
                        onPressed: () => Navigator.pop(context, true),
                        child: const Text('退出'),
                      ),
                    ],
                  ),
                );
                if (confirmed == true) {
                  try {
                    await ref.read(appRuntimeProvider).session.signOut();
                  } catch (_) {
                    if (context.mounted) _notice('退出未完成，请重试');
                  }
                }
              }
            },
            itemBuilder: (_) => const [
              PopupMenuItem(value: 'meetings', child: Text('会议（预约与管理）')),
              PopupMenuItem(value: 'logout', child: Text('退出登录')),
            ],
          ),
        ],
      ),
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 800),
          child: Column(
            children: [
              if (searching)
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
                  child: TextField(
                    controller: search,
                    autofocus: true,
                    decoration: const InputDecoration(
                      hintText: '搜索当前会话',
                      prefixIcon: Icon(Icons.search),
                    ),
                    onChanged: (_) => setState(() {}),
                  ),
                ),
              if (controller.connection != null)
                _banner(controller.connection!, Icons.cloud_sync_outlined),
              if (controller.failure != null)
                MaterialBanner(
                  content: Text(controller.failure!.message),
                  actions: [
                    TextButton(
                      onPressed: controller.refresh,
                      child: const Text('重试'),
                    ),
                  ],
                ),
              Expanded(
                child: RefreshIndicator(
                  onRefresh: controller.refresh,
                  child: NotificationListener<ScrollNotification>(
                    onNotification: (event) {
                      if (event.metrics.extentAfter < 240 && query.isEmpty) {
                        unawaited(controller.loadMore());
                      }
                      return false;
                    },
                    child: CustomScrollView(
                      physics: const AlwaysScrollableScrollPhysics(),
                      slivers: [
                        if (controller.loading && controller.items.isEmpty)
                          const SliverFillRemaining(
                            hasScrollBody: false,
                            child: Center(child: CircularProgressIndicator()),
                          )
                        else if (items.isEmpty)
                          SliverFillRemaining(
                            hasScrollBody: false,
                            child: _empty(query.isNotEmpty),
                          )
                        else
                          SliverList.builder(
                            itemCount: items.length,
                            itemBuilder: (_, index) => _row(items[index]),
                          ),
                        if (controller.loadingMore)
                          const SliverToBoxAdapter(
                            child: Padding(
                              padding: EdgeInsets.all(16),
                              child: Center(child: CircularProgressIndicator()),
                            ),
                          ),
                        const SliverToBoxAdapter(child: SizedBox(height: 24)),
                      ],
                    ),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _banner(String label, IconData icon) => Container(
    width: double.infinity,
    color: const Color(0xffeaf1ff),
    padding: const EdgeInsets.all(12),
    child: Row(
      children: [
        Icon(icon, size: 18, color: AppColors.primary),
        const SizedBox(width: 8),
        Expanded(
          child: Text(label, style: const TextStyle(color: AppColors.primary)),
        ),
      ],
    ),
  );

  Widget _empty(bool hasQuery) => Padding(
    padding: const EdgeInsets.all(28),
    child: Column(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        Icon(
          hasQuery ? Icons.search_off : Icons.forum_outlined,
          size: 56,
          color: AppColors.secondary,
        ),
        const SizedBox(height: 20),
        Text(
          hasQuery ? '没有找到匹配的会话' : '还没有会话',
          style: const TextStyle(fontSize: 20, fontWeight: FontWeight.w600),
        ),
        const SizedBox(height: 8),
        Text(
          hasQuery ? '试试其他名称或关键词' : '新的聊天会显示在这里',
          style: const TextStyle(color: AppColors.secondary),
        ),
      ],
    ),
  );

  Widget _row(Conversation item) => Material(
    color: item.pinned ? const Color(0xffedf3ff) : Colors.white,
    child: InkWell(
      onTap: () => context.push('/chat/${Uri.encodeComponent(item.id)}'),
      onLongPress: () => _actions(item),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 15),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.center,
          children: [
            Badge(
              isLabelVisible: item.unread > 0,
              label: Text(item.unread > 99 ? '99+' : '${item.unread}'),
              backgroundColor: item.muted
                  ? AppColors.secondary
                  : AppColors.error,
              child: CircleAvatar(
                radius: 25,
                backgroundColor: const Color(0xffdfeaff),
                backgroundImage: item.avatar == null
                    ? null
                    : NetworkImage(item.avatar.toString()),
                onBackgroundImageError: item.avatar == null ? null : (_, _) {},
                child: item.avatar == null
                    ? (item.isGroup
                          ? const Icon(Icons.groups, color: AppColors.primary)
                          : Text(
                              item.title.isEmpty
                                  ? '?'
                                  : item.title.characters.first,
                              style: const TextStyle(
                                color: AppColors.primary,
                                fontWeight: FontWeight.w600,
                              ),
                            ))
                    : null,
              ),
            ),
            const SizedBox(width: 15),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          item.title,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            fontSize: 16,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                      Text(
                        _time(item.updatedAt),
                        style: const TextStyle(
                          fontSize: 12,
                          color: AppColors.secondary,
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 6),
                  Row(
                    children: [
                      if (item.mentions)
                        const Text(
                          '[有人@我] ',
                          style: TextStyle(color: AppColors.error),
                        ),
                      if (item.draft.isNotEmpty)
                        const Text(
                          '[草稿] ',
                          style: TextStyle(color: AppColors.error),
                        ),
                      Expanded(
                        child: Text(
                          item.draft.isNotEmpty ? item.draft : item.preview,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            fontSize: 14,
                            color: AppColors.secondary,
                          ),
                        ),
                      ),
                      if (item.muted)
                        const Icon(
                          Icons.notifications_off_outlined,
                          size: 16,
                          color: AppColors.secondary,
                        ),
                    ],
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    ),
  );

  String _time(DateTime value) {
    if (value.millisecondsSinceEpoch == 0) return '';
    final now = DateTime.now();
    if (now.year == value.year &&
        now.month == value.month &&
        now.day == value.day) {
      return '${value.hour.toString().padLeft(2, '0')}:${value.minute.toString().padLeft(2, '0')}';
    }
    return '${value.month}/${value.day}';
  }

  void _notice(String message) {
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  Future<void> _actions(Conversation item) async {
    if (controller.pendingActions.contains(item.id)) return;
    final repository = controller.repository;
    late final MessageHistoryAccess lease;
    try {
      lease = repository.gateway.historyAccess(
        repository.generation,
        conversationId: item.id,
      );
    } on AppFailure {
      return;
    }
    bool accepts() {
      if (!mounted || !repository.current) return false;
      try {
        lease.check();
        return true;
      } on AppFailure {
        return false;
      }
    }

    final action = await showModalBottomSheet<String>(
      context: context,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            for (final entry in <String, String>{
              'pin': item.pinned ? '取消置顶' : '置顶会话',
              'mute': item.muted ? '关闭免打扰' : '开启免打扰',
              'read': '标记已读',
              'hide': '从列表移除',
              'delete': '移除会话并删除聊天记录',
            }.entries)
              ListTile(
                title: Text(
                  entry.value,
                  style: TextStyle(
                    color: entry.key == 'delete' ? AppColors.error : null,
                  ),
                ),
                onTap: () => Navigator.pop(context, entry.key),
              ),
          ],
        ),
      ),
    );
    if (action == null || !mounted || !accepts()) return;
    if (action == 'delete') {
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('删除聊天记录？'),
          content: Text('将移除「${item.title}」会话及其聊天记录，此操作无法撤销。'),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('删除'),
            ),
          ],
        ),
      );
      if (confirmed != true) return;
    }
    if (!mounted || !accepts()) return;
    final failure = await controller.perform(
      item.id,
      () => switch (action) {
        'pin' => repository.pin(item),
        'mute' => repository.mute(item),
        'read' => repository.markRead(item.id),
        'hide' => repository.hide(item.id),
        'delete' => repository.deleteWithMessages(item.id),
        _ => Future.value(),
      },
    );
    if (failure != null && mounted) _notice(failure.message);
  }
}
