import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/im_event.dart' show ImEventKind;
import '../contacts_providers.dart';
import '../data/contacts_repository.dart';
import '../domain/contact.dart';
import '../../group/data/group_repository.dart';
import '../../group/domain/group.dart';
import '../../complaint/data/complaint_repository.dart';
import '../../complaint/presentation/complaint_page.dart';
import 'contact_widgets.dart';

/// An optional original-source capability. Ordinary address-book routes do not
/// provide one. The caller owns its lifetime; this page never renews it.
final class UserProfileSource {
  UserProfileSource({
    required this.repository,
    required this.userId,
    required this.current,
    required this.canRetain,
    required this.changes,
    this.uiChanges,
    this.expiresAtMillis,
    this.now = DateTime.now,
    this.groupId,
    this.groupsRepository,
  }) : _owner = repository.access.credentials,
       _generation = repository.access.generation;
  final ContactsRepository repository;
  final String userId;
  final bool Function() current;
  // Separate from reading/relationship authority. This is the original
  // message retention capability; profile hydration never renews it.
  final bool Function() canRetain;
  final Stream<void> changes;
  final Listenable? uiChanges;
  final int? Function()? expiresAtMillis;
  final DateTime Function() now;
  final String? groupId;
  final GroupRepository? groupsRepository;
  final Object _owner;
  final int _generation;
  void check() {
    repository.access.check();
    if (!identical(repository.access.session.state.credentials, _owner) ||
        repository.access.generation != _generation ||
        !current()) {
      throw const AppFailure(FailureKind.cancelled);
    }
  }
}

class UserProfilePage extends ConsumerStatefulWidget {
  const UserProfilePage({
    super.key,
    required this.userId,
    this.groupId,
    this.source,
  });
  final String userId;
  final String? groupId;
  final UserProfileSource? source;
  @override
  ConsumerState<UserProfilePage> createState() => _UserProfilePageState();
}

class _UserProfilePageState extends ConsumerState<UserProfilePage>
    with WidgetsBindingObserver {
  bool busy = false;
  int _conversationView = 0;
  final _sourceSubscriptions = <StreamSubscription<dynamic>>[];
  final _sourceChanges = ValueNotifier<int>(0);
  AsyncValue<Contact> _sourceProfile = const AsyncLoading();
  AsyncValue<GroupProfile> _sourceGroup = const AsyncLoading();
  ModalRoute<dynamic>? _sourceRoute, _sourceDialog;
  NavigatorState? _sourceNavigator;
  bool _sourceRetired = false, _openingSourceDialog = false;
  int _sourceRead = 0;
  Timer? _sourceExpiry;
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    final source = widget.source;
    if (source != null) {
      source.uiChanges?.addListener(_checkSourceChange);
      final session = source.repository.access.session;
      _sourceSubscriptions.addAll([
        source.changes.listen((_) => _checkSourceChange()),
        session.invalidations.listen((_) => _checkSourceChange()),
        session.changes.listen((_) => _checkSourceChange()),
        session.accessChanges.listen((_) => _checkSourceChange()),
        source.repository.gateway.events.listen((event) {
          if (source.groupId != null &&
              event.kind == ImEventKind.groupsChanged) {
            _retireSource();
          } else {
            _checkSourceChange();
          }
        }),
      ]);
      WidgetsBinding.instance.addPostFrameCallback((_) {
        _watchSourceExpiry();
        unawaited(_loadSource());
      });
    }
  }

  bool get _sourceCurrent {
    final source = widget.source;
    if (_sourceRetired) {
      return false;
    }
    if (source == null) {
      return true;
    }
    if (!mounted ||
        source.userId != widget.userId ||
        source.groupId != widget.groupId ||
        !identical(ref.read(contactsRepositoryProvider), source.repository) ||
        (source.groupId != null &&
            (source.groupsRepository == null ||
                !identical(
                  ref.read(groupRepositoryProvider),
                  source.groupsRepository,
                ) ||
                !identical(
                  source.groupsRepository!.access.credentials,
                  source.repository.access.credentials,
                ))) ||
        _sourceRoute?.isActive != true ||
        _sourceNavigator?.mounted != true ||
        !identical(_sourceRoute?.navigator, _sourceNavigator) ||
        !(_sourceRoute?.isCurrent == true ||
            _sourceDialog?.isCurrent == true)) {
      return false;
    }
    try {
      source.check();
      return true;
    } on AppFailure {
      return false;
    }
  }

  void _checkSource() {
    if (!_sourceCurrent) {
      throw const AppFailure(FailureKind.cancelled);
    }
  }

  bool get _canCopyId {
    if (!_sourceCurrent) {
      return false;
    }
    try {
      return widget.source?.canRetain() ?? true;
    } on AppFailure {
      return false;
    }
  }

  void _retireSource() {
    if (_sourceRetired) {
      return;
    }
    _sourceRetired = true;
    ++_sourceRead;
    _sourceExpiry?.cancel();
    ++_conversationView;
    _sourceProfile = const AsyncLoading();
    _sourceGroup = const AsyncLoading();
    // Guard/data retirement above is synchronous. Only a build-phase repaint
    // must wait; ordinary expiry/session events repaint on the next frame.
    void repaint() {
      if (mounted) {
        ++_sourceChanges.value;
        setState(() {});
      }
    }

    if (mounted) {
      if (SchedulerBinding.instance.schedulerPhase ==
          SchedulerPhase.persistentCallbacks) {
        WidgetsBinding.instance.addPostFrameCallback((_) => repaint());
        WidgetsBinding.instance.ensureVisualUpdate();
      } else {
        repaint();
      }
    }
  }

  void _checkSourceChange() {
    if (widget.source == null || !mounted) {
      return;
    }
    if (!_sourceCurrent) {
      _retireSource();
    } else {
      _watchSourceExpiry();
      setState(() {});
    }
  }

  void _watchSourceExpiry() {
    _sourceExpiry?.cancel();
    final source = widget.source;
    final expiry = source?.expiresAtMillis?.call();
    if (source == null || expiry == null || !mounted || _sourceRetired) {
      return;
    }
    final remaining = expiry - source.now().millisecondsSinceEpoch;
    if (remaining <= 0) {
      _retireSource();
      return;
    }
    _sourceExpiry = Timer(
      Duration(milliseconds: remaining.clamp(1, 86400000).toInt()),
      _checkSourceChange,
    );
  }

  Future<void> _loadSource() async {
    final source = widget.source;
    if (source == null || !_sourceCurrent) {
      return;
    }
    final read = ++_sourceRead;
    setState(() {
      _sourceProfile = const AsyncLoading();
      _sourceGroup = const AsyncLoading();
    });
    try {
      final value = await source.repository.profile(
        widget.userId,
        checkSource: _checkSource,
      );
      if (!_sourceCurrent || read != _sourceRead) {
        return;
      }
      final group = await _readSourceGroup();
      if (!_sourceCurrent || read != _sourceRead) {
        return;
      }
      setState(() {
        _sourceProfile = AsyncData(value);
        if (group != null) {
          _sourceGroup = AsyncData(group);
        }
      });
    } catch (error, stack) {
      if (_sourceCurrent && read == _sourceRead) {
        setState(() => _sourceProfile = AsyncError(error, stack));
      }
    }
  }

  Future<GroupProfile?> _readSourceGroup() async {
    _checkSource();
    final source = widget.source;
    if (source?.groupId == null) {
      return null;
    }
    final repository = source!.groupsRepository;
    if (repository == null) {
      throw const AppFailure(FailureKind.cancelled);
    }
    final value = await repository.profile(
      source.groupId!,
      checkSource: _checkSource,
    );
    _checkSource();
    if (value.summary.id != source.groupId) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return value;
  }

  bool _groupAllows(
    Contact user,
    GroupProfile? group, {
    bool friendRequest = false,
  }) {
    if (group == null) {
      return widget.groupId == null;
    }
    final me = widget.source!.repository.access.credentials.userId == user.id;
    if (!me && !user.friend && group.hideMemberProfiles && !group.canManage) {
      return false;
    }
    return !friendRequest ||
        !group.preventMemberFriends ||
        group.canManage ||
        group.me?.id == user.id;
  }

  Widget _guardSourceDialog(Widget child) => Builder(
    builder: (context) {
      if (widget.source == null) {
        return child;
      }
      final route = ModalRoute.of(context)!, navigator = Navigator.of(context);
      _sourceDialog = route;
      _openingSourceDialog = false;
      return _ProfileDialogLifetime(
        onCovered: _retireSource,
        child: AnimatedBuilder(
          animation: _sourceChanges,
          builder: (_, _) => _sourceCurrent
              ? child
              : AlertDialog(
                  title: const Text('操作已失效'),
                  content: const Text('请返回后重新打开资料'),
                  actions: [
                    TextButton(
                      onPressed: () {
                        if (route.isCurrent &&
                            route.isActive &&
                            navigator.mounted &&
                            identical(route.navigator, navigator)) {
                          navigator.pop();
                        }
                      },
                      child: const Text('关闭'),
                    ),
                  ],
                ),
        ),
      );
    },
  );
  Future<T?> _sourceDialogAction<T>(Future<T?> Function() show) async {
    if (!_sourceCurrent) {
      return null;
    }
    if (widget.source == null) {
      return show();
    }
    if (_openingSourceDialog || _sourceDialog?.isActive == true) {
      return null;
    }
    _openingSourceDialog = true;
    try {
      final value = await show();
      final route = _sourceDialog;
      if (route != null) await route.completed;
      return _sourceCurrent ? value : null;
    } finally {
      _sourceDialog = null;
      _openingSourceDialog = false;
    }
  }

  Future<String?> _input(
    String title, {
    String value = '',
    String hint = '',
    int maxLength = 128,
    int lines = 1,
  }) => _sourceDialogAction(
    () => contactInput(
      context,
      title,
      value: value,
      hint: hint,
      maxLength: maxLength,
      lines: lines,
      guard: widget.source == null ? null : _guardSourceDialog,
      isCurrent: widget.source == null ? null : () => _sourceCurrent,
    ),
  );
  Future<bool> _confirm(
    String title,
    String text, {
    String action = '确定',
  }) async =>
      await _sourceDialogAction(
        () => contactConfirm(
          context,
          title,
          text,
          action: action,
          guard: widget.source == null ? null : _guardSourceDialog,
          isCurrent: widget.source == null ? null : () => _sourceCurrent,
        ),
      ) ??
      false;
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    _sourceRoute ??= route;
    _sourceNavigator ??= Navigator.of(context);
    if (route?.isCurrent == false) {
      if (widget.source == null) {
        ++_conversationView;
      } else if (!_openingSourceDialog && _sourceDialog?.isActive != true) {
        _retireSource();
      }
    }
  }

  @override
  void didUpdateWidget(covariant UserProfilePage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.userId != widget.userId ||
        oldWidget.groupId != widget.groupId ||
        !identical(oldWidget.source, widget.source)) {
      ++_conversationView;
      if (oldWidget.source != null || widget.source != null) {
        _retireSource();
      }
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (widget.source != null && state != AppLifecycleState.resumed) {
      _retireSource();
    }
  }

  @override
  void deactivate() {
    if (widget.source != null) {
      _retireSource();
    }
    super.deactivate();
  }

  @override
  void dispose() {
    ++_conversationView;
    ++_sourceRead;
    _sourceExpiry?.cancel();
    WidgetsBinding.instance.removeObserver(this);
    for (final subscription in _sourceSubscriptions) {
      unawaited(subscription.cancel());
    }
    widget.source?.uiChanges?.removeListener(_checkSourceChange);
    _sourceChanges.dispose();
    super.dispose();
  }

  Future<void> run(
    Future<void> Function(ContactsRepository) operation, {
    String? success,
    bool Function()? isCurrent,
    ContactsRepository? expectedRepository,
    bool Function(Contact)? policy,
    bool friendRequest = false,
  }) async {
    final repository = ref.read(contactsRepositoryProvider);
    final view = _conversationView,
        target = widget.userId,
        source = widget.source;
    bool current() =>
        mounted &&
        view == _conversationView &&
        widget.userId == target &&
        repository != null &&
        repository.access.isCurrent &&
        identical(ref.read(contactsRepositoryProvider), repository) &&
        (expectedRepository == null ||
            identical(expectedRepository, repository)) &&
        _sourceCurrent &&
        isCurrent?.call() != false;
    if (repository == null || busy || !current()) {
      return;
    }
    setState(() => busy = true);
    try {
      if (source != null) {
        final latest = await repository.profile(
          target,
          checkSource: () {
            if (!current()) {
              throw const AppFailure(FailureKind.cancelled);
            }
          },
        );
        final group = await _readSourceGroup();
        if (!current() || latest.id != target) {
          throw const AppFailure(FailureKind.cancelled);
        }
        if (group != null) {
          setState(() => _sourceGroup = AsyncData(group));
        }
        if (!_groupAllows(latest, group, friendRequest: friendRequest) ||
            policy?.call(latest) == false) {
          throw const AppFailure(FailureKind.permissionDenied);
        }
      }
      await operation(repository);
      if (mounted && current()) {
        refreshContacts(ref);
        if (source != null) {
          unawaited(_loadSource());
        }
        if (success != null) contactNotice(context, success);
      }
    } on AppFailure catch (error) {
      if (mounted && current()) {
        contactNotice(context, error.message);
      }
    } finally {
      if (mounted) {
        setState(() => busy = false);
      }
    }
  }

  Future<void> openConversation(
    ContactsRepository repository,
    String id,
  ) async {
    final view = _conversationView;
    final owner = repository.access.credentials;
    final group = widget.groupId;
    bool current() =>
        mounted &&
        view == _conversationView &&
        widget.userId == id &&
        widget.groupId == group &&
        repository.access.isCurrent &&
        identical(repository.access.session.state.credentials, owner) &&
        identical(ref.read(contactsRepositoryProvider), repository) &&
        _sourceCurrent &&
        _sourceRoute?.isCurrent == true;
    if (!current()) {
      return;
    }
    await run(
      (repo) async {
        final conversation = await repo.conversation(
          id,
          checkSource: widget.source == null ? null : _checkSource,
        );
        if (mounted && current()) {
          context.push('/chat/${Uri.encodeComponent(conversation)}');
        }
      },
      isCurrent: current,
      expectedRepository: repository,
      policy: (user) => !user.blocked,
    );
  }

  @override
  Widget build(BuildContext context) {
    final repository = ref.watch(contactsRepositoryProvider);
    final profile = widget.source == null
        ? ref.watch(contactProfileProvider(widget.userId))
        : _sourceProfile;
    final group = widget.groupId == null
        ? null
        : widget.source == null
        ? ref.watch(groupProfileProvider(widget.groupId!))
        : _sourceGroup;
    if (repository == null) {
      return const SizedBox.shrink();
    }
    return Scaffold(
      appBar: AppBar(title: const Text('个人资料')),
      body: widget.source != null && !_sourceCurrent
          ? const Center(child: Text('消息内容已不可用'))
          : profile.when(
              loading: () => const Center(child: CircularProgressIndicator()),
              error: (e, _) => ContactProblem(
                error: e,
                retry: widget.source == null
                    ? () =>
                          ref.invalidate(contactProfileProvider(widget.userId))
                    : () => unawaited(_loadSource()),
              ),
              data: (user) {
                final me = repository.access.credentials.userId == user.id;
                if (widget.groupId != null &&
                    (group?.isLoading == true || group?.hasError == true)) {
                  return group!.when(
                    data: (_) => const SizedBox.shrink(),
                    loading: () =>
                        const Center(child: CircularProgressIndicator()),
                    error: (e, _) => ContactProblem(
                      error: e,
                      retry: () =>
                          ref.invalidate(groupProfileProvider(widget.groupId!)),
                    ),
                  );
                }
                final groupValue = group?.value;
                if (groupValue != null &&
                    !me &&
                    !user.friend &&
                    groupValue.hideMemberProfiles &&
                    !groupValue.canManage) {
                  return const Center(child: Text('该群已禁止查看成员资料'));
                }
                final allowRequest =
                    !user.friend &&
                    !user.blocked &&
                    user.canRequestFriend &&
                    (groupValue == null ||
                        !groupValue.preventMemberFriends ||
                        groupValue.canManage ||
                        groupValue.me?.id == user.id);
                return Center(
                  child: ConstrainedBox(
                    constraints: const BoxConstraints(maxWidth: 720),
                    child: ListView(
                      padding: const EdgeInsets.all(20),
                      children: [
                        Card(
                          elevation: 0,
                          child: Padding(
                            padding: const EdgeInsets.all(24),
                            child: Column(
                              children: [
                                ContactAvatar(uri: user.avatar, radius: 38),
                                const SizedBox(height: 16),
                                Text(
                                  user.displayName,
                                  textAlign: TextAlign.center,
                                  style: const TextStyle(
                                    fontSize: 24,
                                    fontWeight: FontWeight.w600,
                                  ),
                                ),
                                if (user.remark.isNotEmpty)
                                  Text('昵称：${user.name}'),
                                const SizedBox(height: 8),
                                Wrap(
                                  crossAxisAlignment: WrapCrossAlignment.center,
                                  alignment: WrapAlignment.center,
                                  children: [
                                    if (widget.source == null)
                                      SelectableText('ID：${user.id}')
                                    else
                                      Text('ID：${user.id}'),
                                    IconButton(
                                      tooltip: '复制用户 ID',
                                      style: IconButton.styleFrom(
                                        minimumSize: const Size(44, 44),
                                        visualDensity: VisualDensity.standard,
                                        tapTargetSize:
                                            MaterialTapTargetSize.padded,
                                      ),
                                      onPressed: _canCopyId
                                          ? () async {
                                              if (!_canCopyId) {
                                                return;
                                              }
                                              await Clipboard.setData(
                                                ClipboardData(text: user.id),
                                              );
                                              if (context.mounted &&
                                                  _canCopyId) {
                                                contactNotice(
                                                  context,
                                                  'ID 已复制',
                                                );
                                              }
                                            }
                                          : null,
                                      icon: const Icon(
                                        Icons.copy_outlined,
                                        size: 18,
                                      ),
                                    ),
                                  ],
                                ),
                              ],
                            ),
                          ),
                        ),
                        const SizedBox(height: 16),
                        Card(
                          elevation: 0,
                          child: ListTile(
                            leading: const Icon(Icons.photo_library_outlined),
                            title: Text(me ? '我的动态' : '个人动态'),
                            trailing: const Icon(Icons.chevron_right),
                            onTap: () {
                              if (_sourceCurrent) {
                                context.push(
                                  '/moments/user/${Uri.encodeComponent(user.id)}',
                                );
                              }
                            },
                          ),
                        ),
                        if (!me && user.friend)
                          Card(
                            elevation: 0,
                            child: ListTile(
                              title: const Text('备注'),
                              subtitle: Text(
                                user.remark.isEmpty ? '设置好友备注' : user.remark,
                              ),
                              trailing: const Icon(Icons.chevron_right),
                              enabled: !busy,
                              onTap: () async {
                                if (!_sourceCurrent) {
                                  return;
                                }
                                final value = await _input(
                                  '好友备注',
                                  value: user.remark,
                                );
                                if (value != null &&
                                    mounted &&
                                    repository.access.isCurrent &&
                                    _sourceCurrent) {
                                  await run(
                                    (repo) => repo.remark(
                                      user.id,
                                      value,
                                      checkSource: widget.source == null
                                          ? null
                                          : _checkSource,
                                    ),
                                    expectedRepository: repository,
                                    policy: (latest) =>
                                        latest.friend &&
                                        latest.id !=
                                            repository
                                                .access
                                                .credentials
                                                .userId,
                                  );
                                }
                              },
                            ),
                          ),
                        if (!me)
                          Card(
                            elevation: 0,
                            child: SwitchListTile(
                              title: const Text('加入黑名单'),
                              subtitle: const Text('屏蔽来自该用户的消息'),
                              value: user.blocked,
                              onChanged: busy
                                  ? null
                                  : (value) async {
                                      if (!_sourceCurrent) {
                                        return;
                                      }
                                      if (!await _confirm(
                                        value ? '加入黑名单？' : '移出黑名单？',
                                        value
                                            ? '将屏蔽该用户的消息，可随时在黑名单中恢复。'
                                            : '恢复接收该用户的消息。',
                                      )) {
                                        return;
                                      }
                                      if (mounted &&
                                          repository.access.isCurrent &&
                                          _sourceCurrent) {
                                        await run(
                                          (repo) => repo.block(
                                            user.id,
                                            value,
                                            checkSource: widget.source == null
                                                ? null
                                                : _checkSource,
                                          ),
                                          expectedRepository: repository,
                                          policy: (latest) =>
                                              latest.id !=
                                                  repository
                                                      .access
                                                      .credentials
                                                      .userId &&
                                              latest.blocked == user.blocked,
                                        );
                                      }
                                    },
                            ),
                          ),
                        if (!me)
                          ListTile(
                            leading: const Icon(Icons.flag_outlined),
                            title: const Text('投诉此用户'),
                            trailing: const Icon(Icons.chevron_right),
                            enabled: !busy,
                            onTap: () {
                              if (!_sourceCurrent) {
                                return;
                              }
                              Navigator.push(
                                context,
                                MaterialPageRoute(
                                  builder: (_) => ComplaintPage(
                                    target: ComplaintTarget.user,
                                    targetId: user.id,
                                    targetName: user.displayName,
                                  ),
                                ),
                              );
                            },
                          ),
                        const SizedBox(height: 20),
                        if (!user.blocked)
                          FilledButton.icon(
                            onPressed: busy
                                ? null
                                : () => openConversation(repository, user.id),
                            icon: const Icon(Icons.chat_bubble_outline),
                            label: const Text('发消息'),
                          ),
                        if (!me && allowRequest)
                          Padding(
                            padding: const EdgeInsets.only(top: 12),
                            child: OutlinedButton(
                              onPressed: busy
                                  ? null
                                  : () async {
                                      if (!_sourceCurrent) {
                                        return;
                                      }
                                      final reason = await _input(
                                        '添加好友',
                                        hint: '填写验证信息',
                                        maxLength: 256,
                                        lines: 3,
                                      );
                                      if (reason != null &&
                                          mounted &&
                                          repository.access.isCurrent &&
                                          _sourceCurrent) {
                                        await run(
                                          (repo) => repo.requestFriend(
                                            user.id,
                                            reason,
                                            checkSource: widget.source == null
                                                ? null
                                                : _checkSource,
                                          ),
                                          success: '好友申请已发送',
                                          expectedRepository: repository,
                                          policy: (latest) =>
                                              !latest.friend &&
                                              !latest.blocked &&
                                              latest.canRequestFriend &&
                                              latest.id !=
                                                  repository
                                                      .access
                                                      .credentials
                                                      .userId,
                                          friendRequest: true,
                                        );
                                      }
                                    },
                              child: const Text('添加好友'),
                            ),
                          ),
                        if (!me && !user.friend && !user.canRequestFriend)
                          const Padding(
                            padding: EdgeInsets.all(16),
                            child: Text(
                              '对方暂不接受好友申请',
                              textAlign: TextAlign.center,
                            ),
                          ),
                        if (!me && user.friend)
                          TextButton(
                            onPressed: busy
                                ? null
                                : () async {
                                    if (!_sourceCurrent) {
                                      return;
                                    }
                                    if (!await _confirm(
                                      '删除好友？',
                                      '你将从通讯录中移除该好友。',
                                      action: '删除',
                                    )) {
                                      return;
                                    }
                                    if (!mounted ||
                                        !repository.access.isCurrent ||
                                        !_sourceCurrent) {
                                      return;
                                    }
                                    await run(
                                      (repo) => repo.deleteFriend(
                                        user.id,
                                        checkSource: widget.source == null
                                            ? null
                                            : _checkSource,
                                      ),
                                      success: '好友已删除',
                                      expectedRepository: repository,
                                      policy: (latest) =>
                                          latest.friend &&
                                          latest.id !=
                                              repository
                                                  .access
                                                  .credentials
                                                  .userId,
                                    );
                                  },
                            child: Text(
                              '删除好友',
                              style: TextStyle(
                                color: Theme.of(context).colorScheme.error,
                              ),
                            ),
                          ),
                        if (busy)
                          const Padding(
                            padding: EdgeInsets.all(16),
                            child: Center(child: CircularProgressIndicator()),
                          ),
                      ],
                    ),
                  ),
                );
              },
            ),
    );
  }
}

class _ProfileDialogLifetime extends StatefulWidget {
  const _ProfileDialogLifetime({required this.onCovered, required this.child});
  final VoidCallback onCovered;
  final Widget child;
  @override
  State<_ProfileDialogLifetime> createState() => _ProfileDialogLifetimeState();
}

class _ProfileDialogLifetimeState extends State<_ProfileDialogLifetime> {
  bool _retired = false;
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    if (!_retired && route?.isActive == true && route?.isCurrent != true) {
      _retired = true;
      widget.onCovered();
    }
  }

  @override
  Widget build(BuildContext context) => widget.child;
}
