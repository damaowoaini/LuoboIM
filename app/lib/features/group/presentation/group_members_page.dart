import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/error/app_failure.dart';
import '../../contacts/contacts_providers.dart';
import '../../contacts/presentation/contact_widgets.dart';
import '../domain/group.dart';

class GroupMembersPage extends ConsumerStatefulWidget {
  const GroupMembersPage({super.key, required this.groupId});
  final String groupId;
  @override
  ConsumerState<GroupMembersPage> createState() => _GroupMembersPageState();
}

class _GroupMembersPageState extends ConsumerState<GroupMembersPage> {
  final items = <GroupMember>[];
  final input = TextEditingController();
  String query = '';
  bool loading = false, busy = false, more = true;
  AppFailure? failure;
  int offset = 0, ticket = 0;
  @override
  void initState() {
    super.initState();
    Future.microtask(load);
  }

  @override
  void dispose() {
    ticket++;
    input.dispose();
    super.dispose();
  }

  Future<void> load({bool reset = true}) async {
    if (!mounted || busy || (loading && !reset) || (!more && !reset)) return;
    final repository = ref.read(groupRepositoryProvider);
    if (repository == null) return;
    final current = ++ticket;
    setState(() {
      loading = true;
      failure = null;
      if (reset) {
        items.clear();
        offset = 0;
        query = input.text.trim();
      }
    });
    try {
      final result = await repository.members(
        widget.groupId,
        query: query,
        offset: offset,
      );
      if (mounted && current == ticket && repository.access.isCurrent) {
        setState(() {
          final ids = items.map((member) => member.id).toSet();
          items.addAll(result.where((member) => ids.add(member.id)));
          offset += result.length;
          more = result.length == 40;
        });
      }
    } on AppFailure catch (error) {
      if (mounted && current == ticket && repository.access.isCurrent) {
        setState(() => failure = error);
      }
    } finally {
      if (mounted && current == ticket) setState(() => loading = false);
    }
  }

  Future<void> action(GroupMember member, GroupProfile group) async {
    final repository = ref.read(groupRepositoryProvider);
    if (repository == null || busy) return;
    final choice = await showModalBottomSheet<String>(
      context: context,
      builder: (context) => SafeArea(
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              ListTile(
                title: Text(member.name),
                subtitle: Text(
                  member.owner
                      ? '群主'
                      : member.admin
                      ? '管理员'
                      : '群成员',
                ),
              ),
              ListTile(
                leading: const Icon(Icons.person_outline),
                title: const Text('查看资料'),
                onTap: () => Navigator.pop(context, 'profile'),
              ),
              if (group.me?.canModerate(member) == true) ...[
                ListTile(
                  leading: const Icon(Icons.volume_off_outlined),
                  title: Text(member.muted ? '解除禁言' : '禁言'),
                  onTap: () => Navigator.pop(context, 'mute'),
                ),
                ListTile(
                  leading: const Icon(Icons.person_remove_outlined),
                  title: const Text('移出群聊'),
                  onTap: () => Navigator.pop(context, 'remove'),
                ),
              ],
              if (group.me?.owner == true && !member.owner) ...[
                ListTile(
                  leading: const Icon(Icons.admin_panel_settings_outlined),
                  title: Text(member.admin ? '取消管理员' : '设为管理员'),
                  onTap: () => Navigator.pop(context, 'role'),
                ),
                ListTile(
                  leading: const Icon(Icons.swap_horiz),
                  title: const Text('转让群主'),
                  onTap: () => Navigator.pop(context, 'transfer'),
                ),
              ],
            ],
          ),
        ),
      ),
    );
    if (choice == null || !mounted || !repository.access.isCurrent) return;
    if (choice == 'profile') {
      context.push(contactPath(member.id, groupId: widget.groupId));
      return;
    }
    var seconds = 0;
    if (choice == 'mute' && !member.muted) {
      final selected = await showModalBottomSheet<int>(
        context: context,
        builder: (context) => SafeArea(
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
                  onTap: () => Navigator.pop(context, entry.key),
                ),
            ],
          ),
        ),
      );
      if (selected == null || !mounted || !repository.access.isCurrent) return;
      seconds = selected;
    }
    if (choice == 'remove' || choice == 'transfer') {
      if (!await contactConfirm(
        context,
        choice == 'transfer' ? '转让群主？' : '移出群聊？',
        choice == 'transfer'
            ? '将群主转让给 ${member.name}，你将失去群主权限。'
            : '将 ${member.name} 移出当前群聊。',
      )) {
        return;
      }
      if (!mounted || !repository.access.isCurrent) return;
    }
    setState(() => busy = true);
    try {
      switch (choice) {
        case 'mute':
          await repository.muteMember(widget.groupId, member.id, seconds);
        case 'remove':
          await repository.remove(widget.groupId, [member.id]);
        case 'role':
          await repository.role(widget.groupId, member.id, !member.admin);
        case 'transfer':
          await repository.transfer(widget.groupId, member.id);
      }
      if (mounted && repository.access.isCurrent) refreshContacts(ref);
    } on AppFailure catch (error) {
      if (mounted && repository.access.isCurrent) {
        contactNotice(context, error.message);
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
    if (mounted && repository.access.isCurrent) await load();
  }

  @override
  Widget build(BuildContext context) {
    if (ref.watch(groupRepositoryProvider) == null) {
      return const SizedBox.shrink();
    }
    final group = ref.watch(groupProfileProvider(widget.groupId));
    return Scaffold(
      appBar: AppBar(title: const Text('群成员')),
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
          Expanded(
            child: RefreshIndicator(
              onRefresh: load,
              child: CustomScrollView(
                physics: const AlwaysScrollableScrollPhysics(),
                slivers: [
                  if (group.hasError)
                    SliverToBoxAdapter(
                      child: ContactProblem(
                        error: group.error!,
                        retry: () => ref.invalidate(
                          groupProfileProvider(widget.groupId),
                        ),
                      ),
                    ),
                  SliverList.builder(
                    itemCount: items.length,
                    itemBuilder: (context, index) {
                      final member = items[index];
                      return ListTile(
                        leading: ContactAvatar(uri: member.avatar),
                        title: Text(member.name),
                        subtitle: Text(
                          [
                            if (member.owner) '群主' else if (member.admin) '管理员',
                            if (member.muted) '已禁言',
                          ].join(' · '),
                        ),
                        onTap: busy || group.value == null
                            ? null
                            : () => action(member, group.requireValue),
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
        ],
      ),
    );
  }
}
