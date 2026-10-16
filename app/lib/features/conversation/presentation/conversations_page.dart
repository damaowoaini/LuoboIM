import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/config/app_config.dart';
import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../design/tokens.dart';
import '../../../providers.dart';
import '../../group/presentation/group_creation_action.dart';
import '../data/conversation_repository.dart';
import '../domain/conversation.dart';
import 'conversations_controller.dart';
import 'conversation_unread_signal.dart';

class ConversationsPage extends ConsumerStatefulWidget {
  const ConversationsPage({super.key});
  @override
  ConsumerState<ConversationsPage> createState() => _ConversationsPageState();
}

class _ConversationsPageState extends ConsumerState<ConversationsPage> {
  late final ConversationsController controller;
  final search = TextEditingController();
  bool searching = false;
  final _scroll = ScrollController();
  final _viewport = GlobalKey(debugLabel: 'conversation-viewport');
  final _rowContexts = <String, BuildContext>{};
  ConversationUnreadSignal? _unreadSignal;
  late final Object? _unreadOwner;
  late final int _unreadGeneration, _unreadInteractive;
  int _unreadView = 0, _unreadQuery = 0;
  bool _unreadBusy = false;
  TransitionRoute<bool>? _unreadDialog;
  int _lookupView = 0;
  bool _lookupMenuOpen = false;
  _LookupPopupTicket? _pendingLookupPopup, _openedLookupPopup;

  @override
  void initState() {
    super.initState();
    controller = ConversationsController(
      ConversationRepository(
        ref.read(imGatewayProvider),
        ref.read(appRuntimeProvider).session,
      ),
    );
    _unreadOwner = controller.repository.session.state.credentials;
    _unreadGeneration = controller.repository.session.state.generation;
    _unreadInteractive = controller.repository.session.interactiveRevision;
    controller.addListener(_changed);
    unawaited(controller.refresh());
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final signal = ConversationUnreadScope.maybeOf(context);
    if (!identical(signal, _unreadSignal)) {
      _unreadSignal?.removeListener(_unreadRequested);
      _unreadSignal = signal;
      signal?.addListener(_unreadRequested);
    }
    if (!_lookupMenuOpen && _unreadDialog?.isActive != true &&
        ModalRoute.of(context)?.isCurrent == false) {
      _retireUnread();
    }
    if (!_lookupMenuOpen && ModalRoute.of(context)?.isCurrent == false) {
      ++_lookupView;
    }
  }

  void _retireLookupShortcut() {
    _retireUnread();
    ++_lookupView;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) {
        setState(() {});
      }
    });
    WidgetsBinding.instance.ensureVisualUpdate();
  }

  bool _lookupCurrent(_LookupPopupTicket ticket) =>
      mounted && ticket.view == _lookupView &&
      controller.repository.current && ticket.owner != null &&
      identical(controller.repository.session.state.credentials, ticket.owner) &&
      ModalRoute.of(context)?.isCurrent == true;

  void _lookupOpened() {
    // itemBuilder prepared this one popup. Rebuilding the button must never
    // mint a replacement lease for a route that is already open.
    final ticket = _pendingLookupPopup;
    _lookupMenuOpen = true;
    _openedLookupPopup = ticket;
    if (ticket != null) ticket.opened = true;
  }

  void _lookupCovered(_LookupPopupTicket ticket) {
    if (ticket.retired || ticket.consumed) return;
    ticket.retired = true;
    _retireLookupShortcut();
  }

  void _lookupClosed(_LookupPopupTicket ticket) {
    ticket.consumed = true;
    if (identical(_openedLookupPopup, ticket)) {
      _openedLookupPopup = null;
      _lookupMenuOpen = false;
    }
    if (identical(_pendingLookupPopup, ticket)) _pendingLookupPopup = null;
  }

  void _lookupShortcut(_LookupShortcutChoice choice) {
    final ticket = _openedLookupPopup;
    if (ticket == null || !identical(ticket.id, choice.ticket) ||
        !ticket.opened || ticket.consumed) {
      return;
    }
    final allowed = !ticket.retired && _lookupCurrent(ticket) &&
        const ['user', 'group', 'calls', 'unread'].contains(choice.kind);
    _lookupClosed(ticket);
    if (!allowed) return;
    if (choice.kind == 'unread') { unawaited(_nextUnread()); return; }
    context.push(choice.kind == 'calls' ? '/calls' : '/contacts/search?kind=${choice.kind}');
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _unreadSignal?.removeListener(_unreadRequested);
    _scroll.dispose();
    controller.removeListener(_changed);
    controller.dispose();
    search.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final lookupOwner = controller.repository.session.state.credentials;
    final lookupView = _lookupView;
    final query = searching ? search.text.trim().toLowerCase() : '';
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
          GroupCreationAction(sourceCurrent: () => mounted && controller.repository.current,
            checkVerification: true),
          IconButton(tooltip: '扫一扫', icon: const Icon(Icons.qr_code_scanner),
              onPressed: () => context.push('/qr/scan')),
          IconButton(tooltip: '全局搜索', icon: const Icon(Icons.travel_explore),
              onPressed: () => context.push('/search')),
          IconButton(
            tooltip: '搜索当前会话',
            icon: const Icon(Icons.search),
            onPressed: () => setState(() {
              _retireUnread();
              ++_unreadQuery;
              searching = !searching;
              if (!searching) search.clear();
            }),
          ),
          PopupMenuButton<Object>(
            tooltip: '更多操作',
            onOpened: _lookupOpened,
            onSelected: (value) async {
              if (value is _LookupShortcutChoice) {
                _lookupShortcut(value);
                return;
              }
              final ticket = _openedLookupPopup;
              if (ticket != null) _lookupClosed(ticket);
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
            itemBuilder: (_) {
              final ticket = _LookupPopupTicket(lookupOwner, lookupView);
              _pendingLookupPopup = ticket;
              return [
                PopupMenuItem<Object>(
                  value: _LookupShortcutChoice('user', ticket.id), height: 48,
                  child: _LookupMenuGuard(
                    onCovered: () => _lookupCovered(ticket),
                    onClosed: () => _lookupClosed(ticket),
                    child: const Text('添加好友'))),
                PopupMenuItem<Object>(
                  value: _LookupShortcutChoice('group', ticket.id), height: 48,
                  child: _LookupMenuGuard(
                    onCovered: () => _lookupCovered(ticket),
                    onClosed: () => _lookupClosed(ticket),
                    child: const Text('搜索加群'))),
                PopupMenuItem<Object>(value: _LookupShortcutChoice('unread', ticket.id), height: 48,
                  child: _LookupMenuGuard(onCovered: () => _lookupCovered(ticket),
                    onClosed: () => _lookupClosed(ticket), child: const Text('下一未读会话'))),
                PopupMenuItem<Object>(value: _LookupShortcutChoice('calls', ticket.id), height: 48,
                  child: _LookupMenuGuard(onCovered: () => _lookupCovered(ticket),
                    onClosed: () => _lookupClosed(ticket), child: const Text('通话记录'))),
                const PopupMenuItem<Object>(value: 'meetings', child: Text('会议（预约与管理）')),
                const PopupMenuItem<Object>(value: 'logout', child: Text('退出登录')),
              ];
            },
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
                    onChanged: (_) {
                      ++_unreadQuery;
                      _retireUnread();
                      setState(() {});
                    },
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
                      if (event is ScrollStartNotification && event.dragDetails != null ||
                          event is ScrollUpdateNotification && event.dragDetails != null) {
                        _retireUnread(stopAnimation: false);
                      }
                      if (event.metrics.extentAfter < 240 && query.isEmpty) {
                        unawaited(controller.loadMore());
                      }
                      return false;
                    },
                    child: CustomScrollView(
                      key: _viewport,
                      controller: _scroll,
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
                            itemBuilder: (_, index) => _UnreadRowProbe(
                              key: ValueKey(items[index].id),
                              registered: (context) => _rowContexts[items[index].id] = context,
                              retired: (context) {
                                final id = items[index].id;
                                if (identical(_rowContexts[id], context)) _rowContexts.remove(id);
                              }, child: _row(items[index])) ,
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

  void _unreadRequested() { unawaited(_nextUnread()); }

  bool _unreadCurrent(int view, int query) => mounted && view == _unreadView &&
      query == _unreadQuery && _unreadOwner != null &&
      identical(controller.repository.session.state.credentials, _unreadOwner) &&
      controller.repository.session.isInteractiveCurrent(_unreadGeneration, _unreadInteractive) &&
      controller.repository.current && ModalRoute.of(context)?.isCurrent == true;

  void _retireUnread({bool stopAnimation = true}) {
    final retired = ++_unreadView;
    controller.cancelUnread(notify: false);
    if (stopAnimation) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && retired == _unreadView && !controller.locatingUnread && _scroll.hasClients) {
          _scroll.jumpTo(_scroll.offset); // Cancel only the retired view's scroll activity.
        }
      });
    }
  }

  Rect? _bounds(BuildContext? element) => conversationUnreadBounds(element);

  String? _visibleUnreadAnchor() {
    final viewport = _bounds(_viewport.currentContext);
    if (viewport == null) return null;
    final visible = _rowContexts.entries.where((entry) {
      final bounds = _bounds(entry.value);
      return bounds != null && bounds.overlaps(viewport);
    }).toList()..sort((a, b) => _bounds(a.value)!.top.compareTo(_bounds(b.value)!.top));
    return visible.isEmpty ? null : visible.first.key;
  }

  Future<bool> _seekUnread(UnreadConversationLocation location) async {
    final id = location.id;
    if (id == null) return false;
    return revealUnreadConversation(id: id, scroll: _scroll,
      rowContexts: _rowContexts, viewport: () => _viewport.currentContext,
      orderedIds: () => controller.items.map((item) => item.id).toList(growable: false),
      current: () => mounted && location.current);
  }

  Future<void> _nextUnread() async {
    final view = _unreadView, query = _unreadQuery;
    if (!_unreadCurrent(view, query)) return;
    if (_unreadBusy || controller.locatingUnread) { _notice('正在定位未读会话，请稍候'); return; }
    _unreadBusy = true;
    UnreadConversationLocation? location;
    try {
      if (searching && search.text.trim().isNotEmpty) {
        final dialog = DialogRoute<bool>(context: context, builder: (dialogContext) =>
          _LookupMenuGuard(onCovered: _retireUnread, onClosed: () {}, child: AlertDialog(
            title: const Text('查找下一未读会话'),
            content: const Text('当前正在筛选会话，是否切换到全部会话再查找？'),
            actions: [
              TextButton(onPressed: () {
                if (dialogContext.mounted && identical(ModalRoute.of(dialogContext), _unreadDialog) &&
                    ModalRoute.of(dialogContext)?.isCurrent == true) {
                  Navigator.pop(dialogContext, false); // Safe cancellation of this exact owned dialog.
                }
              }, child: const Text('取消')),
              FilledButton(onPressed: () {
                if (dialogContext.mounted && identical(ModalRoute.of(dialogContext), _unreadDialog) &&
                    ModalRoute.of(dialogContext)?.isCurrent == true) {
                  final allowed = identical(_unreadOwner, controller.repository.session.state.credentials) &&
                      view == _unreadView && query == _unreadQuery && controller.repository.current;
                  Navigator.pop(dialogContext, allowed); // A retired intent may close, never renew.
                }
              }, child: const Text('查看全部会话')),
            ])));
        _unreadDialog = dialog;
        final accepted = await Navigator.of(context).push(dialog);
        await dialog.completed;
        if (identical(_unreadDialog, dialog)) _unreadDialog = null;
        if (!mounted || !_unreadCurrent(view, query) || accepted != true) return;
        setState(() => searching = false); // Preserve the user's text for reopening.
      }
      if (!_unreadCurrent(view, query)) return;
      final anchor = _visibleUnreadAnchor();
      location = await controller.nextUnread(visibleAnchor: anchor,
        sourceCurrent: () => _unreadCurrent(view, query));
      if (!mounted || !location.current) return;
      if (location.id == null) { _notice('暂无未读会话'); return; }
      final reached = await _seekUnread(location);
      if (!mounted || !location.current) return;
      controller.completeUnread(location, reached: reached);
      _notice(reached ? '已定位到下一未读会话' : '暂时无法定位，请重试');
    } on AppFailure catch (failure) {
      if (mounted && _unreadCurrent(view, query)) _notice(failure.message);
    } catch (_) {
      if (mounted && _unreadCurrent(view, query)) _notice('暂时无法定位，请重试');
    } finally {
      if (location != null) controller.completeUnread(location, reached: false);
      _unreadBusy = false;
    }
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

// Normal opening/closing of this owned popup does not retire its entry. A
// foreign route covering the popup does: returning must not revive that choice.
class _LookupMenuGuard extends StatefulWidget {
  const _LookupMenuGuard({required this.onCovered, required this.onClosed, required this.child});
  final VoidCallback onCovered, onClosed;
  final Widget child;
  @override
  State<_LookupMenuGuard> createState() => _LookupMenuGuardState();
}

class _LookupMenuGuardState extends State<_LookupMenuGuard> {
  bool covered = false, observingPop = false;
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    if (!observingPop && route != null) {
      observingPop = true;
      final onClosed = widget.onClosed;
      // The route owns cancellation, not whichever button widget happens to
      // be current when PopupMenuButton's asynchronous callback runs.
      unawaited(route.popped.then<void>((_) { onClosed(); }));
    }
    if (!covered && route?.isActive == true && route?.isCurrent == false) {
      covered = true;
      widget.onCovered();
    }
  }
  @override
  void dispose() {
    widget.onClosed();
    super.dispose();
  }
  @override
  Widget build(BuildContext context) => widget.child;
}

// Widgets carry only an opaque id. The original credential object remains in
// this State's private popup lease and is never encoded in a route or value.
final class _LookupPopupTicket {
  _LookupPopupTicket(this.owner, this.view);
  final Object id = Object();
  final Object? owner;
  final int view;
  bool opened = false, retired = false, consumed = false;
}
final class _LookupShortcutChoice {
  const _LookupShortcutChoice(this.kind, this.ticket);
  final String kind;
  final Object ticket;
}

/// Only mounted sliver rows are retained; no key/context for an unseen row.
class _UnreadRowProbe extends StatefulWidget {
  const _UnreadRowProbe({super.key, required this.registered, required this.retired, required this.child});
  final ValueChanged<BuildContext> registered, retired;
  final Widget child;
  @override
  State<_UnreadRowProbe> createState() => _UnreadRowProbeState();
}
class _UnreadRowProbeState extends State<_UnreadRowProbe> {
  @override
  Widget build(BuildContext context) { widget.registered(context); return widget.child; }
  @override
  void dispose() { widget.retired(context); super.dispose(); }
}
