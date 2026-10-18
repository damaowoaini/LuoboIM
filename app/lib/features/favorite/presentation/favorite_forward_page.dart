import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../design/components/feedback.dart';
import '../../../providers.dart';
import '../../chat/data/chat_repository.dart';
import '../../chat/presentation/chat_page.dart';
import '../../chat/presentation/saved_emoji_sheet.dart' show EmojiPortalGuard;
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

class _FavoriteForwardPageState extends ConsumerState<FavoriteForwardPage>
    with WidgetsBindingObserver {
  late final ConversationsController controller;
  MessageHistoryAccess? sourceAccess;
  StreamSubscription<void>? privacy;
  bool initialized = false;
  String query = '';
  bool sending = false;
  bool dispatching = false, unconfirmed = false;
  late final _original = widget;
  late final _owner = widget.repository.api.access.credentials;
  OpenImGateway? _gateway;
  ModalRoute<dynamic>? _route;
  DialogRoute<bool>? _confirmation;
  NavigatorState? _navigator;
  bool _retired = false, _foreground = true;
  int _view = 0;
  ({String id, bool group, String recipient, String label})? _reviewTarget;
  bool get _ownerCurrent =>
      widget.repository.isCurrent &&
      identical(widget.repository, _original.repository) &&
      identical(widget.item, _original.item) &&
      identical(
        widget.repository.api.access.session.state.credentials,
        _owner,
      ) &&
      (_gateway == null || identical(ref.read(imGatewayProvider), _gateway));
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
          (!value || view == _view && _ownerCurrent && _visible && !_retired)) {
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
        sending ||
        !_ownerCurrent ||
        !_visible ||
        _route?.isCurrent != true) {
      return;
    }
    final view = _view;
    final navigator = _navigator!, sourceRoute = _route!;
    final gateway = _gateway!,
        session = _original.repository.api.access.session;
    final generation = _original.repository.api.access.generation;
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
    setState(() => sending = true);
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
        setState(() => sending = false);
      }
    }
  }

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    final lifecycle = WidgetsBinding.instance.lifecycleState;
    _foreground = lifecycle == null || lifecycle == AppLifecycleState.resumed;
    if (!widget.repository.isCurrent ||
        !widget.item.canUseContent ||
        !widget.item.decoded.canForward) {
      return;
    }
    _gateway = ref.read(imGatewayProvider);
    sourceAccess = _gateway!.historyAccess(
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
    _retireView();
    _route?.secondaryAnimation?.removeListener(_routeChanged);
    WidgetsBinding.instance.removeObserver(this);
    unawaited(privacy?.cancel());
    if (initialized) {
      controller.removeListener(changed);
      controller.dispose();
    }
    super.dispose();
  }

  void checkSource(int view) {
    if (view != _view ||
        !_ownerCurrent ||
        !_visible ||
        _retired ||
        sourceAccess == null) {
      throw const AppFailure(FailureKind.cancelled);
    }
    widget.repository.checkContent(widget.item);
    sourceAccess!.check();
    if (!widget.item.canUseContent || !widget.item.decoded.canForward) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
  }

  Future<void> send(Conversation target) async {
    final repository = widget.repository;
    if (sending ||
        unconfirmed ||
        !_visible ||
        _route?.isCurrent != true ||
        !_ownerCurrent ||
        !widget.item.canUseContent ||
        !widget.item.decoded.canForward ||
        !target.canSend ||
        !target.hasRecipientIdentity) {
      return;
    }
    setState(() {
      sending = true;
      _retired = false;
    });
    final view = _view;
    void check() => checkSource(view);
    ChatRepository? chat;
    var sendDispatched = false;
    try {
      check();
      final confirmed = await _confirm(
        view,
        '转发给 ${target.title}？',
        '${target.recipientIdentityLabel}\n\n确认转发这条消息。',
      );
      if (!confirmed) {
        return;
      }
      check();
      setState(() => dispatching = true);
      chat = ChatRepository(
        gateway: controller.repository.gateway,
        session: repository.api.access.session,
        conversationId: target.id,
      );
      final actual = await chat.conversation();
      if (!mounted) return;
      check();
      if (actual.isGroup != target.isGroup ||
          actual.recipientStableId != target.recipientStableId) {
        throw const AppFailure(FailureKind.dataChanged);
      }
      await chat.forwardMessageJson(
        widget.item.content,
        checkSource: check,
        onDispatched: () {
          check();
          sendDispatched = true;
        },
      );
      if (mounted &&
          view == _view &&
          _ownerCurrent &&
          _visible &&
          !_retired &&
          _route?.isCurrent == true) {
        Navigator.pop(context, true);
      }
    } catch (error) {
      if (sendDispatched &&
          !(error is AppFailure &&
              (error.code == 1302 || error.code == 1303))) {
        unconfirmed = true;
        _reviewTarget = (
          id: target.id,
          group: target.isGroup,
          recipient: target.recipientStableId,
          label: target.recipientIdentityLabel,
        );
      }
      if (mounted && _ownerCurrent && _visible) {
        showNotice(
          context,
          unconfirmed
              ? '转发结果未确认，请先返回会话核对，不会自动重试。'
              : error is AppFailure
              ? error.message
              : '消息未发送，请重试',
        );
      }
    } finally {
      _confirmation = null;
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
    if (!_ownerCurrent) {
      return const Scaffold(body: Center(child: Text('登录已失效，请重新登录')));
    }
    if (!unconfirmed &&
        (!widget.item.canUseContent || !widget.item.decoded.canForward)) {
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
                if (unconfirmed) ...[
                  const Padding(
                    padding: EdgeInsets.all(16),
                    child: Text('转发结果未确认，请先返回会话核对，不会自动重试。'),
                  ),
                  if (_reviewTarget case final target?)
                    TextButton(
                      onPressed: sending ? null : _review,
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
                    enabled: !sending && !unconfirmed,
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
                          onTap: sending || unconfirmed
                              ? null
                              : () => send(item),
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
