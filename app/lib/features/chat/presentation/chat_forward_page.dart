import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../design/components/feedback.dart';
import '../../../providers.dart';
import '../../conversation/data/conversation_repository.dart';
import '../../conversation/domain/conversation.dart';
import '../../conversation/domain/recipient_identity.dart';
import '../../conversation/presentation/conversations_controller.dart';
import '../data/chat_repository.dart';
import '../domain/chat_snapshot.dart';
import '../domain/chat_batch.dart';
import 'chat_page.dart';
import 'saved_emoji_sheet.dart' show EmojiPortalGuard;

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

class _ChatForwardPageState extends State<ChatForwardPage>
    with WidgetsBindingObserver {
  ConversationsController? controller;
  String query = '';
  bool busy = false;
  bool dispatching = false, unconfirmed = false;
  StreamSubscription<void>? _sourcePrivacy;
  late final _original = widget;
  late final _owner = widget.source.session.state.credentials;
  MessageHistoryAccess? _sourceAccess;
  void Function()? _singleSourceCheck;
  ModalRoute<dynamic>? _route;
  DialogRoute<bool>? _confirmation;
  NavigatorState? _navigator;
  bool _retired = false, _foreground = true;
  int _view = 0;
  ({String id, bool group, String recipient, String label})? _reviewTarget;
  bool get _ownerCurrent =>
      widget.source.current &&
      identical(widget.source, _original.source) &&
      identical(widget.snapshot, _original.snapshot) &&
      identical(widget.batch, _original.batch) &&
      identical(widget.source.session.state.credentials, _owner);
  bool get current => _ownerCurrent && (widget.batch?.current ?? true);
  bool get _visible =>
      mounted &&
      _foreground &&
      _route?.isActive == true &&
      _navigator?.mounted == true &&
      identical(_route?.navigator, _navigator) &&
      (_route?.isCurrent == true ||
          _confirmation?.isActive == true &&
              _confirmation?.isCurrent == true &&
              identical(_confirmation?.navigator, _navigator));
  bool get _actionCurrent => !_retired && _visible && current;
  void _retireView() {
    _retired = true;
    ++_view;
  }

  void _routeChanged() {
    if (_route?.isActive != true ||
        _route?.isCurrent != true &&
            !(_confirmation?.isActive == true &&
                _confirmation?.isCurrent == true &&
                identical(_confirmation?.navigator, _navigator))) {
      _retireView();
    }
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    if (_route == null && route != null) {
      _route = route;
      route.secondaryAnimation?.addListener(_routeChanged);
    }
    _navigator ??= Navigator.of(context);
    _routeChanged();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    _foreground = state == AppLifecycleState.resumed;
    if (!_foreground) {
      _retireView();
    }
  }

  void _checkAction(int view) {
    if (view != _view || !_actionCurrent || _sourceAccess == null) {
      throw const AppFailure(FailureKind.cancelled);
    }
    _sourceAccess!.check();
    widget.batch?.check();
    if (widget.batch == null) {
      final check = _singleSourceCheck;
      if (check == null) {
        throw const AppFailure(FailureKind.cancelled);
      }
      check();
    }
  }

  Future<bool> _confirm(int view, String title, String text) async {
    final navigator = _navigator;
    if (navigator == null) {
      throw const AppFailure(FailureKind.cancelled);
    }
    late final DialogRoute<bool> dialog;
    void finish(bool value) {
      if (identical(_confirmation, dialog) &&
          dialog.isActive &&
          dialog.isCurrent &&
          navigator.mounted &&
          identical(dialog.navigator, navigator) &&
          (!value || view == _view && _actionCurrent)) {
        navigator.pop(value);
      }
    }

    dialog = DialogRoute<bool>(
      context: context,
      builder: (_) => EmojiPortalGuard(
        onCovered: _retireView,
        child: AlertDialog(
          scrollable: true,
          title: Text(title),
          content: Text(text),
          actions: [
            TextButton(onPressed: () => finish(false), child: const Text('取消')),
            TextButton(onPressed: () => finish(true), child: const Text('发送')),
          ],
        ),
      ),
    );
    // Record this exact owned route before push changes the source route.
    _confirmation = dialog;
    try {
      final result = await navigator.push<bool>(dialog);
      await dialog.completed;
      return result == true;
    } finally {
      if (identical(_confirmation, dialog)) {
        _confirmation = null;
      }
    }
  }

  Future<void> _review() async {
    final target = _reviewTarget;
    if (target == null ||
        busy ||
        !_ownerCurrent ||
        !_visible ||
        _route?.isCurrent != true) {
      return;
    }
    final view = _view;
    final navigator = _navigator!, sourceRoute = _route!;
    final gateway = _original.source.gateway,
        session = _original.source.session;
    final generation = _original.source.generation;
    final container = ProviderScope.containerOf(context, listen: false);
    bool originalOwnerCurrent() =>
        mounted &&
        _ownerCurrent &&
        session.state.generation == generation &&
        navigator.mounted &&
        sourceRoute.isActive &&
        identical(_route, sourceRoute) &&
        identical(sourceRoute.navigator, navigator) &&
        identical(
          ProviderScope.containerOf(context, listen: false),
          container,
        ) &&
        identical(container.read(appRuntimeProvider).session, session) &&
        identical(container.read(imGatewayProvider), gateway);
    if (!originalOwnerCurrent()) {
      return;
    }
    setState(() => busy = true);
    final repository = ChatRepository(
      gateway: gateway,
      session: session,
      conversationId: target.id,
    );
    try {
      final actual = await repository.conversation();
      if (!originalOwnerCurrent() ||
          !_visible ||
          _view != view ||
          _route?.isCurrent != true) {
        return;
      }
      if (actual.isGroup != target.group ||
          actual.recipientStableId != target.recipient) {
        throw const AppFailure(FailureKind.dataChanged);
      }
      // A new explicit read of this original destination grants no replay.
      late final MaterialPageRoute<void> ownedReviewRoute;
      bool reviewCurrent() =>
          originalOwnerCurrent() &&
          ownedReviewRoute.isActive &&
          identical(ownedReviewRoute.navigator, navigator);
      ownedReviewRoute = MaterialPageRoute<void>(
        builder: (_) {
          if (!reviewCurrent()) {
            return const Scaffold(
              body: Center(child: Text('原账号或会话已失效，请返回重新核对')),
            );
          }
          return ChatPage(
            conversationId: target.id,
            sourceCurrent: reviewCurrent,
          );
        },
      );
      // A legitimate review covers this picker; ChatPage owns its child routes.
      unawaited(navigator.push<void>(ownedReviewRoute));
    } on AppFailure catch (error) {
      if (mounted && _ownerCurrent && _visible) {
        showNotice(context, error.message);
      }
    } finally {
      repository.clear();
      if (mounted) {
        setState(() => busy = false);
      }
    }
  }

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    final lifecycle = WidgetsBinding.instance.lifecycleState;
    _foreground = lifecycle == null || lifecycle == AppLifecycleState.resumed;
    if (!current) return;
    try {
      _sourceAccess = widget.source.gateway.historyAccess(
        widget.source.generation,
        conversationId: widget.source.conversationId,
      );
      if (widget.snapshot case final snapshot?) {
        _singleSourceCheck = widget.source.forwardSourceCheck(
          snapshot,
          access: _sourceAccess!,
        );
      }
    } on AppFailure {
      _retireView();
    }
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
    _retireView();
    _route?.secondaryAnimation?.removeListener(_routeChanged);
    WidgetsBinding.instance.removeObserver(this);
    unawaited(_sourcePrivacy?.cancel());
    controller?.removeListener(changed);
    controller?.dispose();
    super.dispose();
  }

  Future<void> send(Conversation destination) async {
    if (busy ||
        unconfirmed ||
        !_visible ||
        _route?.isCurrent != true ||
        !current ||
        !destination.canSend ||
        !destination.hasRecipientIdentity) {
      return;
    }
    setState(() {
      busy = true;
      _retired = false;
    });
    final view = _view;
    void checkAction() => _checkAction(view);
    ChatRepository? target;
    var sendDispatched = false;
    try {
      checkAction();
      final confirmed = await _confirm(
        view,
        '转发给 ${destination.title}？',
        '${destination.recipientIdentityLabel}\n\n${widget.batch == null ? '确认转发这条消息。' : '确认合并转发选中的 ${widget.batch!.messageIds.length} 条消息。'}',
      );
      if (!confirmed) {
        return;
      }
      checkAction();
      setState(() => dispatching = true);
      ChatFavoriteSnapshot? retained;
      if (widget.batch case final batch?) {
        batch.check();
      } else {
        final original = await _original.source.findSource(
          _original.snapshot!.messageId,
        );
        if (view != _view || !_actionCurrent) {
          throw const AppFailure(FailureKind.cancelled);
        }
        _sourceAccess!.check();
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
      checkAction();
      target = ChatRepository(
        gateway: widget.source.gateway,
        session: widget.source.session,
        conversationId: destination.id,
      );
      final actual = await target.conversation();
      checkAction();
      if (actual.isGroup != destination.isGroup ||
          actual.recipientStableId != destination.recipientStableId) {
        throw const AppFailure(FailureKind.dataChanged);
      }
      if (widget.batch case final batch?) {
        batch.check();
        await target.forwardMergedSelection(
          source: widget.source,
          intent: batch,
          onDispatched: () {
            checkAction();
            sendDispatched = true;
          },
        );
      } else {
        await target.forwardMessageJson(
          retained!.content,
          checkSource: checkAction,
          revalidateSource: () async {
            checkAction();
            final original = await _original.source.findSource(
              _original.snapshot!.messageId,
            );
            if (original == null) {
              throw const AppFailure(FailureKind.dataChanged);
            }
            checkAction();
          },
          onDispatched: () {
            checkAction();
            sendDispatched = true;
          },
        );
      }
      if (mounted &&
          view == _view &&
          _actionCurrent &&
          _route?.isCurrent == true) {
        Navigator.pop(context, true);
      }
    } catch (error) {
      if (sendDispatched &&
          !(error is AppFailure &&
              (error.code == 1302 || error.code == 1303))) {
        unconfirmed = true;
        _reviewTarget = (
          id: destination.id,
          group: destination.isGroup,
          recipient: destination.recipientStableId,
          label: destination.recipientIdentityLabel,
        );
      }
      if (mounted && current && _visible) {
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
      _confirmation = null;
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
    if (!_ownerCurrent || value == null || (!current && !unconfirmed)) {
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
            if (unconfirmed) ...[
              const Padding(
                padding: EdgeInsets.all(16),
                child: Text('转发结果未确认，请先返回会话核对，不会自动重试。'),
              ),
              if (_reviewTarget case final target?)
                TextButton(
                  onPressed: busy ? null : _review,
                  child: Text('前往核对 · ${target.label}'),
                ),
            ] else if (_retired)
              const Padding(
                padding: EdgeInsets.all(16),
                child: Text('此转发已失效，请重新确认收件人。'),
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
