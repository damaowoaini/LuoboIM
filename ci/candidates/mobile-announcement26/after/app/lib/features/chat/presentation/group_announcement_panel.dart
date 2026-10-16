import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/im_event.dart';
import '../../../core/tenant/session_coordinator.dart';
import '../../../design/tokens.dart';
import '../../../providers.dart';
import '../../contacts/contacts_providers.dart';
import '../../conversation/domain/conversation.dart';
import '../../group/data/group_repository.dart';
import '../data/chat_repository.dart';
import '../data/group_announcement_store.dart';

/// Reads group metadata, not message content. Opening its own detail is allowed;
/// covering either original route retires that view's callbacks monotonically.
class GroupAnnouncementPanel extends ConsumerStatefulWidget {
  const GroupAnnouncementPanel({
    super.key,
    required this.repository,
    required this.conversationId,
    required this.groupId,
    required this.hint,
    required this.sourceCurrent,
  });
  final ChatRepository repository;
  final String conversationId, groupId;
  final ConversationHint hint;
  final bool Function() sourceCurrent;
  @override
  ConsumerState<GroupAnnouncementPanel> createState() =>
      _GroupAnnouncementPanelState();
}

class _GroupAnnouncementPanelState extends ConsumerState<GroupAnnouncementPanel>
    with WidgetsBindingObserver {
  late final ChatRepository _source = widget.repository;
  late final String _cid = widget.conversationId, _group = widget.groupId;
  late final bool Function() _sourceCurrent = widget.sourceCurrent;
  late final GroupRepository? _groups;
  late final GroupAnnouncementStore _store;
  late final String _accountKey;
  StreamSubscription<ImEvent>? _events;
  StreamSubscription<int>? _invalidations, _access;
  StreamSubscription<SessionSnapshot>? _sessions;
  ModalRoute<dynamic>? _sourceRoute;
  DialogRoute<void>? _detail;
  final _detailChanges = ValueNotifier<int>(0);
  bool _detailSignalDisposed = false, _uiQueued = false;
  bool _started = false,
      _ownerRetired = false,
      _viewRetired = false,
      _disposed = false;
  bool _metadataDirty = false;
  bool _foreground = true, _prompt = false, _loading = false, _busy = false;
  int _epoch = 0, _read = 0;
  String? _seenVersion, _failure;
  GroupAnnouncementSnapshot? _snapshot;

  bool get _ownerCurrent =>
      mounted &&
      !_disposed &&
      !_ownerRetired &&
      _source.current &&
      _source.conversationId == _cid &&
      identical(widget.repository, _source) &&
      widget.conversationId == _cid &&
      widget.groupId == _group &&
      _sourceCurrent() &&
      _groups != null &&
      _groups!.access.isCurrent &&
      _groups!.access.generation == _source.generation &&
      identical(_groups!.access.session, _source.session) &&
      identical(ref.read(groupRepositoryProvider), _groups) &&
      identical(ref.read(groupAnnouncementStoreProvider), _store);
  bool get _sourceActive =>
      _ownerCurrent &&
      _foreground &&
      _sourceRoute?.isActive == true &&
      _sourceRoute?.isCurrent == true;
  bool get _routeAvailable =>
      _sourceRoute?.isActive == true &&
      (_sourceRoute?.isCurrent == true ||
          _detail?.isActive == true && _detail?.isCurrent == true);
  bool _current(int epoch) =>
      _ownerCurrent &&
      _foreground &&
      !_viewRetired &&
      epoch == _epoch &&
      _routeAvailable;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    final lifecycle = WidgetsBinding.instance.lifecycleState;
    _foreground = lifecycle == null || lifecycle == AppLifecycleState.resumed;
    _prompt = widget.hint == ConversationHint.announcement;
    // Freeze both original repositories and the store before any await.
    _groups = ref.read(groupRepositoryProvider);
    _store = ref.read(groupAnnouncementStoreProvider);
    _accountKey = _groups?.access.credentials.accountKey ?? '';
    _events = _source.gateway.events.listen((event) {
      if (event.generation != _source.generation || !_ownerCurrent) {
        return;
      }
      if (event.kind == ImEventKind.groupsChanged &&
          (event.resourceIds.isEmpty || event.resourceIds.contains(_group))) {
        _metadataDirty = true;
        _invalidateView();
        if (_sourceActive) {
          unawaited(_load(_epoch));
        }
      }
    });
    void ownerChanged() {
      if (!_ownerCurrent) {
        _retireOwner();
      }
    }

    _invalidations = _source.session.invalidations.listen(
      (_) => ownerChanged(),
    );
    _access = _source.session.accessChanges.listen((_) => ownerChanged());
    _sessions = _source.session.changes.listen((_) => ownerChanged());
  }

  void _notify() {
    void apply() {
      _uiQueued = false;
      if (!_detailSignalDisposed) {
        ++_detailChanges.value;
      }
      if (mounted && !_disposed) {
        setState(() {});
      }
    }

    if (SchedulerBinding.instance.schedulerPhase ==
        SchedulerPhase.persistentCallbacks) {
      if (!_uiQueued) {
        _uiQueued = true;
        WidgetsBinding.instance.addPostFrameCallback((_) => apply());
      }
    } else {
      apply();
    }
  }

  void _invalidateView() {
    if (_viewRetired) {
      return;
    }
    _viewRetired = true;
    ++_epoch;
    ++_read;
    _snapshot = null;
    _failure = null;
    _loading = _busy = false;
    _notify();
  }

  void _retireOwner() {
    if (_ownerRetired) {
      return;
    }
    _ownerRetired = true;
    _invalidateView();
  }

  void _routeChanged() {
    if (_sourceRoute?.isActive != true ||
        _sourceRoute?.isCurrent != true &&
            !(_detail?.isActive == true && _detail?.isCurrent == true)) {
      _invalidateView();
    }
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    if (_sourceRoute == null && route != null) {
      _sourceRoute = route;
      route.secondaryAnimation?.addListener(_routeChanged);
    }
    _routeChanged();
    if (!_started) {
      _started = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (_sourceActive) {
          unawaited(_load(_epoch));
        }
      });
    }
  }

  @override
  void didUpdateWidget(GroupAnnouncementPanel oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!identical(widget.repository, _source) ||
        widget.conversationId != _cid ||
        widget.groupId != _group) {
      _retireOwner();
    } else if (widget.hint == ConversationHint.announcement &&
        oldWidget.hint != widget.hint) {
      _prompt = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (_sourceActive) {
          unawaited(_load(_epoch));
        }
      });
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    _foreground = state == AppLifecycleState.resumed;
    if (!_foreground) {
      _invalidateView();
    } else {
      _notify(); // A new explicit read is allowed; no old callback is renewed.
      if (_metadataDirty && _sourceActive) {
        unawaited(
          _load(_epoch),
        ); // Fresh passive metadata, never a replayed action.
      }
    }
  }

  Future<void> _load(int epoch) async {
    if (!_sourceActive ||
        epoch != _epoch ||
        _loading ||
        _busy ||
        _detail != null) {
      return;
    }
    _viewRetired = false;
    final read = ++_read;
    _loading = true;
    _failure = null;
    _notify();
    void check() {
      if (!_current(epoch) || read != _read) {
        throw const AppFailure(FailureKind.cancelled);
      }
    }

    try {
      final profile = await _groups!.profile(_group, checkSource: check);
      check();
      final snapshot = GroupAnnouncementSnapshot.fromProfile(profile);
      if (snapshot.groupId != _group) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      final dismissed = await _store.dismissed(
        _accountKey,
        snapshot,
        current: () => _current(epoch) && read == _read,
      );
      check();
      final changed = _seenVersion != null && _seenVersion != snapshot.version;
      _seenVersion = snapshot.version;
      _metadataDirty = false;
      _prompt = (_prompt || changed) && !dismissed && !snapshot.empty;
      _snapshot = _prompt ? snapshot : null;
      _loading = false;
      _notify();
    } catch (error) {
      if (_current(epoch) && read == _read) {
        _loading = false;
        _failure = error is AppFailure ? error.message : '群公告读取失败，请重试';
        _notify();
      }
    }
  }

  bool _selected(int epoch, GroupAnnouncementSnapshot snapshot) =>
      _current(epoch) && identical(_snapshot, snapshot);

  Future<void> _dismiss(
    int epoch,
    GroupAnnouncementSnapshot snapshot, {
    DialogRoute<void>? route,
    NavigatorState? navigator,
  }) async {
    if (!_selected(epoch, snapshot) || _busy) {
      return;
    }
    _busy = true;
    _failure = null;
    _notify();
    try {
      await _store.dismiss(
        _accountKey,
        snapshot,
        current: () => _selected(epoch, snapshot),
      );
      if (!_selected(epoch, snapshot)) {
        return;
      }
      _snapshot = null;
      _prompt = false;
      _busy = false;
      _notify();
      if (route != null && navigator != null) {
        _closeOwn(route, navigator);
      }
    } catch (_) {
      if (_selected(epoch, snapshot)) {
        _busy = false;
        _failure = '关闭提醒未确认，请重试';
        _notify();
      }
    }
  }

  void _closeOwn(DialogRoute<void> route, NavigatorState navigator) {
    if (route.isActive &&
        route.isCurrent &&
        identical(route.navigator, navigator) &&
        navigator.mounted) {
      navigator.pop();
    }
  }

  Future<void> _open(int epoch, GroupAnnouncementSnapshot snapshot) async {
    if (!_sourceActive || !_selected(epoch, snapshot) || _detail != null) {
      return;
    }
    final navigator = _sourceRoute!.navigator;
    if (navigator == null || !navigator.mounted) {
      return;
    }
    final textScaler = MediaQuery.textScalerOf(context);
    late final DialogRoute<void> route;
    route = DialogRoute<void>(
      context: context,
      builder: (_) => _AnnouncementOwnedRoute(
        onCovered: _invalidateView,
        child: ValueListenableBuilder<int>(
          valueListenable: _detailChanges,
          builder: (context, _, _) {
            final valid =
                _selected(epoch, snapshot) &&
                identical(_detail, route) &&
                route.isActive &&
                route.isCurrent;
            return MediaQuery(
              data: MediaQuery.of(context).copyWith(textScaler: textScaler),
              child: AlertDialog(
                title: const Text('群公告'),
                content: ConstrainedBox(
                  constraints: BoxConstraints(
                    maxHeight: MediaQuery.sizeOf(context).height * .55,
                  ),
                  child: SingleChildScrollView(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(valid ? snapshot.text : '会话或群资料已变化，请返回聊天后重新读取公告'),
                        if (valid && _failure != null)
                          Text(
                            _failure!,
                            style: const TextStyle(color: AppColors.error),
                          ),
                        if (valid && _busy) const LinearProgressIndicator(),
                      ],
                    ),
                  ),
                ),
                actions: [
                  TextButton(
                    onPressed: () => _closeOwn(route, navigator),
                    child: const Text('返回聊天'),
                  ),
                  if (valid)
                    TextButton(
                      onPressed: _busy
                          ? null
                          : () => _dismiss(
                              epoch,
                              snapshot,
                              route: route,
                              navigator: navigator,
                            ),
                      child: const Text('关闭本条提醒'),
                    ),
                ],
              ),
            );
          },
        ),
      ),
    );
    _detail = route;
    try {
      await navigator.push<void>(route);
      await route.completed;
    } finally {
      if (identical(_detail, route)) {
        _detail = null;
      }
      if (!mounted) {
        _disposeDetailSignal();
      } else {
        _notify();
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    ref.watch(groupRepositoryProvider);
    ref.watch(groupAnnouncementStoreProvider);
    if (!_ownerCurrent) {
      _retireOwner();
      return const SizedBox.shrink();
    }
    final epoch = _epoch, snapshot = _snapshot;
    if (snapshot == null) {
      if (!_prompt) {
        return const SizedBox.shrink();
      }
      if (_loading) {
        return const LinearProgressIndicator(semanticsLabel: '正在读取群公告');
      }
      if (_failure == null && !_viewRetired) {
        return const SizedBox.shrink();
      }
      return Row(
        children: [
          Expanded(
            child: Text(
              _failure ?? '群公告提示已暂停',
              style: const TextStyle(color: AppColors.secondary),
            ),
          ),
          TextButton(
            onPressed: _sourceActive ? () => _load(epoch) : null,
            child: const Text('重新读取群公告'),
          ),
        ],
      );
    }
    return Material(
      key: const ValueKey('group-announcement-banner'),
      color: AppColors.primary.withValues(alpha: .08),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.center,
            children: [
              Expanded(
                child: TextButton(
                  onPressed: !_busy && _sourceActive
                      ? () => _open(epoch, snapshot)
                      : null,
                  style: TextButton.styleFrom(alignment: Alignment.centerLeft),
                  child: Text(
                    '群公告：${snapshot.text}',
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ),
              IconButton(
                tooltip: '关闭本条群公告提醒',
                constraints: const BoxConstraints(minWidth: 44, minHeight: 44),
                visualDensity: VisualDensity.standard,
                onPressed: !_sourceActive || _busy
                    ? null
                    : () => _dismiss(epoch, snapshot),
                icon: const Icon(Icons.close),
              ),
            ],
          ),
          if (_failure != null)
            Padding(
              padding: const EdgeInsets.all(8),
              child: Text(
                _failure!,
                style: const TextStyle(color: AppColors.error),
              ),
            ),
        ],
      ),
    );
  }

  void _disposeDetailSignal() {
    if (!_detailSignalDisposed) {
      _detailSignalDisposed = true;
      _detailChanges.dispose();
    }
  }

  @override
  void dispose() {
    _disposed = true;
    WidgetsBinding.instance.removeObserver(this);
    _sourceRoute?.secondaryAnimation?.removeListener(_routeChanged);
    _ownerRetired = true;
    _viewRetired = true;
    ++_epoch;
    ++_read;
    _snapshot = null;
    _events?.cancel();
    _invalidations?.cancel();
    _access?.cancel();
    _sessions?.cancel();
    // A still-owned dialog can outlive this subtree briefly. Withdraw its
    // content next frame, and release the signal only after its route completes.
    if (_detail case final route?) {
      _notify();
      unawaited(route.completed.then<void>((_) => _disposeDetailSignal()));
    } else {
      _disposeDetailSignal();
    }
    super.dispose();
  }
}

class _AnnouncementOwnedRoute extends StatefulWidget {
  const _AnnouncementOwnedRoute({required this.onCovered, required this.child});
  final VoidCallback onCovered;
  final Widget child;
  @override
  State<_AnnouncementOwnedRoute> createState() =>
      _AnnouncementOwnedRouteState();
}

class _AnnouncementOwnedRouteState extends State<_AnnouncementOwnedRoute> {
  late final VoidCallback _covered = widget.onCovered;
  bool _retired = false;
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    if (!_retired && route?.isActive == true && route?.isCurrent != true) {
      _retired = true;
      _covered();
    }
  }

  @override
  Widget build(BuildContext context) => widget.child;
}
