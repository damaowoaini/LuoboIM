import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../design/tokens.dart';
import '../../contacts/data/contacts_repository.dart';
import '../../contacts/domain/contact.dart';
import '../domain/chat_card.dart';
import 'chat_controller.dart';
import 'saved_emoji_sheet.dart' show EmojiPortalGuard;

class ChatCardPickerPage extends StatefulWidget {
  const ChatCardPickerPage({
    super.key,
    required this.controller,
    required this.contacts,
    required this.access,
    required this.sourceCurrent,
    required this.sourceChanges,
  });
  final ChatController controller;
  final ContactsRepository contacts;
  final MessageHistoryAccess access;
  final bool Function() sourceCurrent;
  final Listenable sourceChanges;
  @override
  State<ChatCardPickerPage> createState() => _ChatCardPickerPageState();
}

class _ChatCardPickerPageState extends State<ChatCardPickerPage>
    with WidgetsBindingObserver {
  final _intent = newCardIntent();
  final _dialogChanges = ValueNotifier<int>(0);
  final _subscriptions = <StreamSubscription<dynamic>>[];
  ModalRoute<dynamic>? _route, _dialog;
  NavigatorState? _navigator;
  List<Contact> _friends = const [];
  String _query = '', _notice = '';
  bool _loading = false,
      _readSucceeded = false,
      _readFailed = false,
      _confirming = false,
      _openingDialog = false;
  bool _retired = false, _foreground = true;
  int _read = 0;
  late final _owner = widget.contacts.access.credentials;
  late final _generation = widget.contacts.access.generation;
  late final _issuer = _owner.issuer, _tenant = _owner.tenantId;

  bool get _owns {
    if (!mounted ||
        _retired ||
        !widget.controller.usable ||
        !widget.contacts.access.isCurrent) {
      return false;
    }
    try {
      widget.access.check();
      return widget.sourceCurrent() &&
          identical(widget.contacts.access.session.state.credentials, _owner) &&
          widget.contacts.access.generation == _generation &&
          _owner.issuer == _issuer &&
          _owner.tenantId == _tenant &&
          _route?.isActive == true &&
          _navigator?.mounted == true &&
          identical(_route?.navigator, _navigator);
    } on AppFailure {
      return false;
    }
  }

  bool get _current =>
      _owns &&
      _foreground &&
      (_route?.isCurrent == true || _dialog?.isCurrent == true);
  bool get _unknown => widget.controller.isCardIntentUnconfirmed(_intent);

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    widget.controller.addListener(_changed);
    widget.sourceChanges.addListener(_changed);
    final session = widget.controller.repository.session;
    _subscriptions.addAll([
      session.invalidations.listen((_) => _changed()),
      session.changes.listen((_) => _changed()),
      session.accessChanges.listen((_) => _changed()),
    ]);
    WidgetsBinding.instance.addPostFrameCallback((_) => unawaited(_load()));
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _route ??= ModalRoute.of(context);
    _navigator ??= Navigator.of(context);
    if (_route?.isActive == true &&
        _route?.isCurrent != true &&
        !_openingDialog &&
        _dialog?.isActive != true) {
      _retire(notify: false);
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    _foreground = state == AppLifecycleState.resumed;
    if (!_foreground) {
      _retire();
    }
  }

  void _retire({bool notify = true}) {
    if (_retired) {
      return;
    }
    _retired = true;
    ++_read;
    _friends = const [];
    if (notify && mounted) {
      void repaint() {
        if (!mounted) {
          return;
        }
        _dialogChanges.value++;
        setState(() {});
      }

      if (SchedulerBinding.instance.schedulerPhase ==
          SchedulerPhase.persistentCallbacks) {
        WidgetsBinding.instance.addPostFrameCallback((_) => repaint());
        WidgetsBinding.instance.ensureVisualUpdate();
      } else {
        repaint();
      }
    }
  }

  void _changed() {
    if (!_owns) {
      _retire();
    } else if (mounted) {
      setState(() {});
    }
  }

  Future<void> _load() async {
    if (!_current || _loading || _confirming || _unknown) {
      return;
    }
    final read = ++_read;
    setState(() {
      _loading = true;
      _readFailed = false;
      _notice = '';
    });
    try {
      // getFriendList is the SDK's complete address book, not the loaded
      // conversation page or an arbitrary first40 recipient subset.
      final rows = await widget.contacts.friends();
      if (!_current || read != _read) {
        return;
      }
      final ids = <String>{};
      if (rows.any(
        (v) =>
            ChatCard.parse(userId: v.id, name: v.name) == null ||
            !ids.add(v.id),
      )) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      setState(() {
        _friends = List.unmodifiable(rows);
        _readSucceeded = true;
      });
    } catch (error) {
      if (_current && read == _read) {
        setState(() {
          _readFailed = true;
          _notice = error is AppFailure ? error.message : '好友列表读取失败，请重试';
        });
      }
    } finally {
      if (mounted && read == _read) {
        setState(() => _loading = false);
      }
    }
  }

  Future<void> _choose(Contact contact) async {
    if (!_current ||
        _confirming ||
        _loading ||
        _unknown ||
        widget.controller.conversation?.canSend != true ||
        !_friends.any((v) => identical(v, contact))) {
      return;
    }
    final origin = _route, navigator = _navigator;
    if (origin == null || navigator == null || !origin.isCurrent) {
      return;
    }
    setState(() {
      _confirming = true;
      _openingDialog = true;
      _notice = '';
    });
    final target = widget.controller.conversation!;
    bool current() =>
        _current &&
        _friends.any((v) => identical(v, contact)) &&
        widget.controller.conversation?.userId == target.userId &&
        widget.controller.conversation?.groupId == target.groupId &&
        widget.controller.conversation?.isGroup == target.isGroup;
    try {
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (context) {
          final route = ModalRoute.of(context)!;
          final dialogNavigator = Navigator.of(context);
          _dialog = route;
          _openingDialog = false;
          void finish(bool value) {
            if (route.isCurrent &&
                route.isActive &&
                dialogNavigator.mounted &&
                identical(route.navigator, dialogNavigator)) {
              dialogNavigator.pop(value);
            }
          }

          return EmojiPortalGuard(
            onCovered: _retire,
            child: AnimatedBuilder(
              animation: _dialogChanges,
              builder: (_, _) => !_owns
                  ? AlertDialog(
                      title: const Text('操作已失效'),
                      content: const Text('请返回聊天后重新选择'),
                      actions: [
                        TextButton(
                          onPressed: () => finish(false),
                          child: const Text('关闭'),
                        ),
                      ],
                    )
                  : AlertDialog(
                      title: const Text('发送好友名片？'),
                      content: SingleChildScrollView(
                        child: Column(
                          mainAxisSize: MainAxisSize.min,
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(
                              contact.name.isEmpty ? contact.id : contact.name,
                            ),
                            Text('账号 ID：${contact.id}'),
                            const SizedBox(height: 16),
                            Text('发送到：${target.title}'),
                            Text(
                              '${target.isGroup ? '群 ID' : '账号 ID'}：${target.isGroup ? target.groupId : target.userId}',
                            ),
                          ],
                        ),
                      ),
                      actions: [
                        TextButton(
                          onPressed: () => finish(false),
                          child: const Text('取消'),
                        ),
                        FilledButton(
                          onPressed: () {
                            if (current()) finish(true);
                          },
                          child: const Text('发送'),
                        ),
                      ],
                    ),
            ),
          );
        },
      );
      final dialog = _dialog;
      if (dialog != null) await dialog.completed;
      _dialog = null;
      _openingDialog = false;
      if (confirmed != true || !current()) {
        return;
      }
      final result = await widget.controller.sendCard(
        contact.id,
        intentId: _intent,
        access: widget.access,
        checkSource: () {
          if (!current()) {
            throw const AppFailure(FailureKind.cancelled);
          }
        },
      );
      if (!_current) {
        return;
      }
      if (result.kind == CardSendKind.sent) {
        if (origin.isCurrent &&
            navigator.mounted &&
            identical(origin.navigator, navigator)) {
          navigator.pop();
        }
      } else {
        setState(
          () => _notice = result.kind == CardSendKind.unconfirmed
              ? '发送结果未确认，请返回聊天核对，勿重复发送'
              : '尚未发送，请检查会话状态后重试',
        );
      }
    } finally {
      _openingDialog = false;
      if (mounted) {
        setState(() => _confirming = false);
      }
    }
  }

  @override
  void dispose() {
    _retired = true;
    ++_read;
    WidgetsBinding.instance.removeObserver(this);
    widget.controller.removeListener(_changed);
    widget.sourceChanges.removeListener(_changed);
    for (final s in _subscriptions) {
      unawaited(s.cancel());
    }
    _dialogChanges.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    ModalRoute.of(context);
    final query = _query.trim().toLowerCase();
    final rows = _friends
        .where(
          (v) =>
              v.displayName.toLowerCase().contains(query) ||
              v.name.toLowerCase().contains(query) ||
              v.id.toLowerCase().contains(query),
        )
        .toList();
    return Scaffold(
      appBar: AppBar(title: const Text('发送好友名片')),
      body: !_owns
          ? const Center(child: Text('此操作已失效，请返回聊天后重新选择'))
          : SafeArea(
              child: Column(
                children: [
                  Padding(
                    padding: const EdgeInsets.all(16),
                    child: TextField(
                      decoration: const InputDecoration(
                        labelText: '搜索好友昵称或账号 ID',
                        prefixIcon: Icon(Icons.search),
                      ),
                      onChanged: (value) {
                        if (_current && !_confirming) {
                          setState(() => _query = value);
                        }
                      },
                      enabled: !_confirming && !_unknown,
                    ),
                  ),
                  if (_loading) const LinearProgressIndicator(),
                  if (_notice.isNotEmpty)
                    Padding(
                      padding: const EdgeInsets.all(12),
                      child: Text(_notice),
                    ),
                  if (!_unknown)
                    TextButton(
                      onPressed: _loading || _confirming ? null : _load,
                      child: const Text('重新读取好友列表'),
                    ),
                  if (_confirming)
                    const Padding(
                      padding: EdgeInsets.all(8),
                      child: Text('正在处理，请稍候'),
                    ),
                  Expanded(
                    child: !_readSucceeded && !_readFailed
                        ? const Center(child: Text('正在读取好友列表…'))
                        : rows.isEmpty
                        ? Center(
                            child: Text(
                              _readFailed
                                  ? '好友列表未能加载'
                                  : query.isEmpty
                                  ? '暂无好友可分享'
                                  : '没有匹配的好友',
                            ),
                          )
                        : ListView.builder(
                            itemCount: rows.length,
                            itemBuilder: (_, index) {
                              final contact = rows[index];
                              return ListTile(
                                key: ValueKey((
                                  'share-card-friend',
                                  contact.id,
                                )),
                                minVerticalPadding: 12,
                                leading: CircleAvatar(
                                  backgroundColor: AppColors.primary.withValues(
                                    alpha: .1,
                                  ),
                                  child: const Icon(
                                    Icons.person_outline,
                                    color: AppColors.primary,
                                  ),
                                ),
                                title: Text(contact.displayName),
                                subtitle: Text('账号 ID：${contact.id}'),
                                enabled:
                                    !_confirming &&
                                    !_unknown &&
                                    widget.controller.conversation?.canSend ==
                                        true,
                                onTap: () => _choose(contact),
                              );
                            },
                          ),
                  ),
                ],
              ),
            ),
    );
  }
}
