import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/error/app_failure.dart';
import '../../contacts/contacts_providers.dart';
import '../../contacts/presentation/contact_widgets.dart';
import '../data/group_repository.dart';
import '../domain/group.dart';

typedef _MembersAccess = ({
  GroupRepository repository,
  String groupId,
  bool Function() current,
  bool Function() owned,
});

class GroupMembersPage extends ConsumerStatefulWidget {
  const GroupMembersPage({super.key, required this.groupId});
  final String groupId;
  @override
  ConsumerState<GroupMembersPage> createState() => _GroupMembersPageState();
}

class _GroupMembersPageState extends ConsumerState<GroupMembersPage> {
  final items = <GroupMember>[];
  final selected = <String, GroupMember>{};
  final unconfirmed = <String>{};
  final results = <GroupRemovalItem>[];
  final input = TextEditingController();
  String query = '';
  bool loading = false, busy = false, more = true, choosing = false;
  bool _loadQueued = false;
  AppFailure? failure;
  int offset = 0, ticket = 0, _view = 0, _overlays = 0, _queuedLoad = 0;
  GroupRepository? _rowsRepository;
  String? _rowsGroup;

  @override
  void initState() {
    super.initState();
    _queueLoad();
  }

  void _queueLoad() {
    if (_loadQueued) return;
    _loadQueued = true;
    final view = _view, job = ++_queuedLoad;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (job != _queuedLoad) return;
      _loadQueued = false;
      if (mounted &&
          view == _view &&
          ModalRoute.of(context)?.isCurrent == true) {
        load();
      }
    });
  }

  void _invalidateView() {
    _view++;
    _queuedLoad++;
    _loadQueued = false;
    ticket++;
    items.clear();
    selected.clear();
    unconfirmed.clear();
    results.clear();
    _rowsRepository = null;
    _rowsGroup = null;
    loading = busy = choosing = false;
    failure = null;
    offset = 0;
    more = true;
  }

  @override
  void didUpdateWidget(covariant GroupMembersPage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.groupId != widget.groupId) {
      _invalidateView();
      _queueLoad();
    }
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    if (_overlays == 0 && route?.isCurrent == false) {
      _invalidateView();
    } else if (route?.isCurrent == true && items.isEmpty && !loading) {
      _queueLoad();
    }
  }

  @override
  void dispose() {
    _view++;
    ticket++;
    input.dispose();
    super.dispose();
  }

  _MembersAccess? _capture() {
    if (!mounted) return null;
    final repository = ref.read(groupRepositoryProvider);
    final route = ModalRoute.of(context);
    if (repository == null ||
        !repository.access.isCurrent ||
        route?.isCurrent != true) {
      return null;
    }
    final owner = repository.access.credentials;
    final generation = repository.access.generation;
    final id = widget.groupId, view = _view;
    bool owned() =>
        mounted &&
        view == _view &&
        widget.groupId == id &&
        identical(ref.read(groupRepositoryProvider), repository) &&
        repository.access.generation == generation &&
        repository.access.isCurrent &&
        identical(repository.access.session.state.credentials, owner);
    return (
      repository: repository,
      groupId: id,
      owned: owned,
      current: () => owned() && route?.isCurrent == true,
    );
  }

  void _observeOverlay(BuildContext context, bool completed) {
    final overlay = ModalRoute.of(context);
    if (!completed &&
        overlay?.isActive == true &&
        overlay?.isCurrent == false) {
      _invalidateView();
    }
  }

  Future<T?> _sheet<T>(
    Widget Function(BuildContext, ValueChanged<T>) body,
  ) async {
    var completed = false;
    _overlays++;
    try {
      return await showModalBottomSheet<T>(
        context: context,
        builder: (context) {
          _observeOverlay(context, completed);
          return body(context, (value) {
            completed = true;
            Navigator.pop(context, value);
          });
        },
      );
    } finally {
      _overlays--;
    }
  }

  Future<bool> _confirm(String title, Widget content) async {
    var completed = false;
    _overlays++;
    try {
      return await showDialog<bool>(
            context: context,
            builder: (context) {
              _observeOverlay(context, completed);
              void finish(bool value) {
                completed = true;
                Navigator.pop(context, value);
              }

              return AlertDialog(
                title: Text(title),
                content: content,
                actions: [
                  TextButton(
                    onPressed: () => finish(false),
                    child: const Text('取消'),
                  ),
                  FilledButton(
                    onPressed: () => finish(true),
                    child: const Text('确认'),
                  ),
                ],
              );
            },
          ) ==
          true;
    } finally {
      _overlays--;
    }
  }

  bool _rowCurrent(GroupMember member, _MembersAccess access) =>
      access.current() &&
      identical(_rowsRepository, access.repository) &&
      _rowsGroup == access.groupId &&
      items.any((row) => identical(row, member));

  Future<void> load({bool reset = true}) async {
    if (!mounted || busy || (loading && !reset) || (!more && !reset)) return;
    final access = _capture();
    if (access == null) return;
    final request = ++ticket;
    if (!identical(_rowsRepository, access.repository) ||
        _rowsGroup != access.groupId) {
      items.clear();
      selected.clear();
      unconfirmed.clear();
      results.clear();
      reset = true;
    }
    setState(() {
      _rowsRepository = access.repository;
      _rowsGroup = access.groupId;
      loading = true;
      failure = null;
      if (reset) {
        items.clear();
        offset = 0;
        query = input.text.trim();
      }
    });
    final requestOffset = offset, requestQuery = query;
    try {
      final list = await access.repository.members(
        access.groupId,
        query: requestQuery,
        offset: requestOffset,
      );
      if (access.current() && request == ticket) {
        setState(() {
          final ids = items.map((member) => member.id).toSet();
          items.addAll(list.where((member) => ids.add(member.id)));
          offset = requestOffset + list.length;
          more = list.length == 40;
        });
      }
    } on AppFailure catch (error) {
      if (access.current() && request == ticket) {
        setState(() => failure = error);
      }
    } finally {
      if (access.owned() && request == ticket) setState(() => loading = false);
    }
  }

  GroupProfile? _latestGroup() =>
      ref.read(groupProfileProvider(widget.groupId)).asData?.value;
  bool _canSelect(GroupMember member, GroupProfile? group) =>
      group?.summary.id == widget.groupId &&
      group?.summary.status != 2 &&
      group?.me?.canModerate(member) == true &&
      !unconfirmed.contains(member.id);

  void toggle(GroupMember member, _MembersAccess access) {
    if (busy ||
        !_rowCurrent(member, access) ||
        !_canSelect(member, _latestGroup())) {
      return;
    }
    setState(() {
      if (selected.containsKey(member.id)) {
        selected.remove(member.id);
      } else if (selected.length < 999) {
        selected[member.id] = member;
      } else {
        contactNotice(context, '每次最多选择 999 位成员');
      }
    });
  }

  Future<void> removeSelection(_MembersAccess access) async {
    if (!access.current() ||
        busy ||
        selected.isEmpty ||
        _latestGroup()?.canManage != true) {
      return;
    }
    final frozen = List<GroupMember>.unmodifiable(selected.values);
    final confirmed = await _confirm(
      '移除选中的 ${frozen.length} 位成员？',
      SizedBox(
        width: 420,
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxHeight: 320),
          child: ListView(
            shrinkWrap: true,
            children: [
              const Text('请核对账号 ID。移除后对方将退出本群。'),
              const Text('将逐一移除；遇到结果未确认时暂停，已移除的成员不会恢复。'),
              for (final member in frozen)
                ListTile(title: Text(member.name), subtitle: Text(member.id)),
            ],
          ),
        ),
      ),
    );
    if (!confirmed ||
        !mounted ||
        !access.current() ||
        busy ||
        _latestGroup()?.canManage != true ||
        frozen.any(
          (member) =>
              !identical(selected[member.id], member) ||
              unconfirmed.contains(member.id),
        )) {
      return;
    }
    setState(() {
      busy = true;
      results.clear();
    });
    try {
      await access.repository.removeSelected(
        access.groupId,
        frozen.map((member) => member.id).toList(),
        isCurrent: access.current,
        onResult: (item) {
          if (!access.current()) return;
          setState(() {
            results.add(item);
            if (item.state == GroupRemovalState.acknowledged) {
              selected.remove(item.userId);
              items.removeWhere((member) => member.id == item.userId);
            } else if (item.state == GroupRemovalState.unconfirmed) {
              selected.remove(item.userId);
              unconfirmed.add(item.userId);
            }
          });
        },
      );
      if (access.current()) refreshContacts(ref);
    } on AppFailure catch (error) {
      if (mounted && access.current()) contactNotice(context, error.message);
    } finally {
      if (access.owned()) setState(() => busy = false);
    }
  }

  Future<void> action(
    GroupMember member,
    GroupProfile group,
    _MembersAccess access,
  ) async {
    if (!_rowCurrent(member, access) || busy) return;
    final choice = await _sheet<String>(
      (context, finish) => SafeArea(
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              ListTile(
                title: Text(member.name),
                subtitle: Text(
                  '${member.id} · ${member.owner
                      ? '群主'
                      : member.admin
                      ? '管理员'
                      : '群成员'}',
                ),
              ),
              ListTile(
                leading: const Icon(Icons.person_outline),
                title: const Text('查看资料'),
                onTap: () => finish('profile'),
              ),
              if (group.me?.canModerate(member) == true) ...[
                ListTile(
                  leading: const Icon(Icons.volume_off_outlined),
                  title: Text(member.muted ? '解除禁言' : '禁言'),
                  onTap: () => finish('mute'),
                ),
                ListTile(
                  leading: const Icon(Icons.person_remove_outlined),
                  title: const Text('移出群聊'),
                  subtitle: unconfirmed.contains(member.id)
                      ? const Text('结果未确认，请刷新核对，本页不再重复移除')
                      : null,
                  enabled: !unconfirmed.contains(member.id),
                  onTap: unconfirmed.contains(member.id)
                      ? null
                      : () => finish('remove'),
                ),
              ],
              if (group.me?.owner == true && !member.owner) ...[
                ListTile(
                  leading: const Icon(Icons.admin_panel_settings_outlined),
                  title: Text(member.admin ? '取消管理员' : '设为管理员'),
                  onTap: () => finish('role'),
                ),
                ListTile(
                  leading: const Icon(Icons.swap_horiz),
                  title: const Text('转让群主'),
                  onTap: () => finish('transfer'),
                ),
              ],
            ],
          ),
        ),
      ),
    );
    if (choice == null || !mounted || !_rowCurrent(member, access) || busy) {
      return;
    }
    if (choice == 'remove' && unconfirmed.contains(member.id)) return;
    if (choice == 'profile') {
      context.push(contactPath(member.id, groupId: access.groupId));
      return;
    }
    var seconds = 0;
    if (choice == 'mute' && !member.muted) {
      final duration = await _sheet<int>(
        (context, finish) => SafeArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              for (final entry in {
                3600: '1 小时',
                86400: '1 天',
                604800: '7 天',
                2592000: '30 天',
              }.entries)
                ListTile(
                  title: Text(entry.value),
                  onTap: () => finish(entry.key),
                ),
            ],
          ),
        ),
      );
      if (duration == null || !mounted || !_rowCurrent(member, access)) return;
      seconds = duration;
    }
    if (choice == 'remove' || choice == 'transfer') {
      if (!await _confirm(
        choice == 'transfer' ? '转让群主？' : '移出群聊？',
        Text(
          choice == 'transfer'
              ? '将群主转让给 ${member.name}（${member.id}），你将失去群主权限。'
              : '将 ${member.name}（${member.id}）移出当前群聊。',
        ),
      )) {
        return;
      }
      if (!mounted || !_rowCurrent(member, access) || busy) return;
      if (choice == 'remove' && unconfirmed.contains(member.id)) return;
    }
    setState(() => busy = true);
    try {
      switch (choice) {
        case 'mute':
          await access.repository.muteMember(
            access.groupId,
            member.id,
            seconds,
          );
        case 'remove':
          await access.repository.remove(access.groupId, [member.id]);
        case 'role':
          await access.repository.role(
            access.groupId,
            member.id,
            !member.admin,
          );
        case 'transfer':
          await access.repository.transfer(access.groupId, member.id);
      }
      if (access.current()) refreshContacts(ref);
    } on AppFailure catch (error) {
      if (mounted && access.current()) contactNotice(context, error.message);
    } finally {
      if (access.owned()) setState(() => busy = false);
    }
    if (access.current()) await load();
  }

  String resultText(GroupRemovalItem item) => switch (item.state) {
    GroupRemovalState.acknowledged => '已收到移除确认',
    GroupRemovalState.rejected => '未移除：${item.failure?.message ?? '权限不足'}',
    GroupRemovalState.unconfirmed => '结果未确认，请刷新核对，本页不再重复移除',
    GroupRemovalState.notAttempted => '未执行',
  };

  @override
  Widget build(BuildContext context) {
    ref.listen(groupRepositoryProvider, (previous, next) {
      if (!identical(previous, next)) {
        setState(_invalidateView);
        _queueLoad();
      }
    });
    ModalRoute.of(context);
    if (ref.watch(groupRepositoryProvider) == null) {
      return const SizedBox.shrink();
    }
    final asyncGroup = ref.watch(groupProfileProvider(widget.groupId));
    final group = asyncGroup.asData?.value;
    final access = _capture();
    final manage =
        group?.summary.id == widget.groupId &&
        group?.summary.status != 2 &&
        group?.canManage == true;
    return Scaffold(
      appBar: AppBar(
        title: const Text('群成员'),
        actions: [
          if (manage && !choosing)
            TextButton(
              key: const ValueKey('group-batch-remove-entry'),
              onPressed: busy || access == null
                  ? null
                  : () {
                      if (access.current()) {
                        setState(() {
                          choosing = true;
                          results.clear();
                        });
                      }
                    },
              child: const Text('批量移除'),
            ),
        ],
      ),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(16),
            child: TextField(
              controller: input,
              onSubmitted: (_) => load(),
              decoration: InputDecoration(
                hintText: '搜索成员昵称或 ID',
                prefixIcon: const Icon(Icons.search),
                suffixIcon: IconButton(
                  tooltip: '搜索群成员',
                  onPressed: busy ? null : () => load(),
                  icon: const Icon(Icons.arrow_forward),
                ),
              ),
            ),
          ),
          if (busy) const LinearProgressIndicator(),
          if (results.isNotEmpty)
            Card(
              child: ExpansionTile(
                initiallyExpanded: true,
                title: Text(
                  '移除结果：已确认 ${results.where((item) => item.state == GroupRemovalState.acknowledged).length} / ${results.length}',
                ),
                children: [
                  SizedBox(
                    height: 160,
                    child: ListView.builder(
                      itemCount: results.length,
                      itemBuilder: (_, index) => ListTile(
                        title: Text(results[index].userId),
                        subtitle: Text(resultText(results[index])),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          Expanded(
            child: RefreshIndicator(
              onRefresh: load,
              child: CustomScrollView(
                physics: const AlwaysScrollableScrollPhysics(),
                slivers: [
                  if (asyncGroup.hasError)
                    SliverToBoxAdapter(
                      child: ContactProblem(
                        error: asyncGroup.error!,
                        retry: () => ref.invalidate(
                          groupProfileProvider(widget.groupId),
                        ),
                      ),
                    ),
                  SliverList.builder(
                    itemCount: items.length,
                    itemBuilder: (context, index) {
                      final member = items[index];
                      final roleText = [
                        if (member.owner) '群主' else if (member.admin) '管理员',
                        if (member.muted) '已禁言',
                      ].join(' · ');
                      if (choosing) {
                        return CheckboxListTile(
                          key: ValueKey('batch-member-${member.id}'),
                          value: selected.containsKey(member.id),
                          secondary: ContactAvatar(uri: member.avatar),
                          title: Text(member.name),
                          subtitle: Text(
                            '${member.id}${roleText.isEmpty ? '' : '\n$roleText'}',
                          ),
                          onChanged:
                              busy ||
                                  access == null ||
                                  !_canSelect(member, group)
                              ? null
                              : (_) => toggle(member, access),
                        );
                      }
                      return ListTile(
                        leading: ContactAvatar(uri: member.avatar),
                        title: Text(member.name),
                        subtitle: Text(
                          '${member.id}${roleText.isEmpty ? '' : '\n$roleText'}',
                        ),
                        onTap: busy || access == null || group == null
                            ? null
                            : () => action(member, group, access),
                      );
                    },
                  ),
                  SliverToBoxAdapter(
                    child: Column(
                      children: [
                        if (failure != null)
                          ContactProblem(
                            error: failure!,
                            retry: () => load(reset: items.isEmpty),
                          ),
                        if (loading)
                          const Padding(
                            padding: EdgeInsets.all(28),
                            child: Center(child: CircularProgressIndicator()),
                          )
                        else if (items.isEmpty && failure == null)
                          const Padding(
                            padding: EdgeInsets.all(28),
                            child: Center(child: Text('没有找到群成员')),
                          )
                        else if (more)
                          TextButton(
                            onPressed: () => load(reset: false),
                            child: const Text('加载更多'),
                          ),
                      ],
                    ),
                  ),
                ],
              ),
            ),
          ),
          if (choosing)
            SafeArea(
              top: false,
              child: Padding(
                padding: const EdgeInsets.all(16),
                child: Wrap(
                  alignment: WrapAlignment.center,
                  spacing: 12,
                  runSpacing: 8,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    Text('已选 ${selected.length} 人'),
                    TextButton(
                      onPressed: busy
                          ? null
                          : () => setState(() {
                              choosing = false;
                              selected.clear();
                            }),
                      child: const Text('退出选择'),
                    ),
                    FilledButton.icon(
                      key: const ValueKey('group-batch-remove-submit'),
                      onPressed:
                          busy || access == null || !manage || selected.isEmpty
                          ? null
                          : () => removeSelection(access),
                      icon: const Icon(Icons.person_remove_outlined),
                      label: const Text('移除所选成员'),
                    ),
                  ],
                ),
              ),
            ),
        ],
      ),
    );
  }
}
