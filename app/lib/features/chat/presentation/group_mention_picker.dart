import 'package:flutter/material.dart';

import '../../../core/error/app_failure.dart';
import '../../../design/components/feedback.dart';
import '../../group/data/group_repository.dart';
import '../../group/domain/group.dart';
import '../domain/chat_mention.dart';

class GroupMentionPicker extends StatefulWidget {
  const GroupMentionPicker({
    super.key,
    required this.repository,
    required this.groupId,
    required this.checkLease,
  });
  final GroupRepository repository;
  final String groupId;
  final void Function() checkLease;
  @override
  State<GroupMentionPicker> createState() => _GroupMentionPickerState();
}

class _GroupMentionPickerState extends State<GroupMentionPicker> {
  final input = TextEditingController();
  final members = <GroupMember>[];
  int ticket = 0, offset = 0;
  bool loading = false, more = true, allowAll = false;
  String query = '';
  AppFailure? failure;
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

  bool current(int captured) {
    if (!mounted || captured != ticket || !widget.repository.access.isCurrent) {
      return false;
    }
    try {
      widget.checkLease();
      return true;
    } on AppFailure {
      return false;
    }
  }

  Future<void> load({bool reset = true}) async {
    if (!mounted || loading && !reset || !more && !reset) return;
    final captured = ++ticket;
    setState(() {
      loading = true;
      failure = null;
      if (reset) {
        offset = 0;
        members.clear();
        query = input.text.trim();
      }
    });
    try {
      widget.checkLease();
      final profile = await widget.repository.profile(widget.groupId);
      if (!current(captured)) return;
      if (!profile.joined || profile.me == null) {
        throw const AppFailure(FailureKind.permissionDenied);
      }
      final page = await widget.repository.members(
        widget.groupId,
        query: query,
        offset: offset,
      );
      if (!current(captured)) return;
      setState(() {
        final ids = members.map((m) => m.id).toSet();
        members.addAll(page.where((m) => ids.add(m.id)));
        offset += page.length;
        more = page.length == 40;
        allowAll = profile.canManage;
      });
    } on AppFailure catch (error) {
      if (current(captured)) setState(() => failure = error);
    } finally {
      if (mounted && captured == ticket) setState(() => loading = false);
    }
  }

  void choose(MentionIdentity identity) {
    try {
      widget.checkLease();
      widget.repository.access.check();
      Navigator.pop(context, identity);
    } on AppFailure catch (error) {
      showNotice(context, error.message);
    }
  }

  @override
  Widget build(BuildContext context) => SafeArea(
    child: SizedBox(
      height: MediaQuery.sizeOf(context).height * .78,
      child: Column(
        children: [
          const ListTile(
            title: Text('选择要 @ 的成员'),
            subtitle: Text('最多选择 10 个不同身份；手写文字不会自动提醒。草稿仅保留文字，重开后请重新选择。'),
          ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: TextField(
              controller: input,
              maxLength: 256,
              onSubmitted: (_) => load(),
              decoration: InputDecoration(
                labelText: '按群昵称或账号 ID 搜索',
                suffixIcon: IconButton(
                  tooltip: '搜索群成员',
                  onPressed: () => load(),
                  icon: const Icon(Icons.search),
                ),
              ),
            ),
          ),
          if (loading) const LinearProgressIndicator(),
          Expanded(
            child: ListView(
              children: [
                if (allowAll && query.isEmpty)
                  ListTile(
                    leading: const Icon(Icons.groups_outlined),
                    title: const Text('@所有人'),
                    subtitle: const Text('提醒群内所有成员，仅群主和管理员可用'),
                    onTap: () =>
                        choose(const MentionIdentity(mentionAllId, '所有人')),
                  ),
                for (final member in members)
                  ListTile(
                    title: Text(member.name),
                    subtitle: Text('账号 ID：${member.id}'),
                    trailing: const Icon(Icons.alternate_email),
                    onTap: () =>
                        choose(MentionIdentity(member.id, member.name)),
                  ),
                if (failure != null)
                  ErrorPanel(error: failure!, retry: () => load()),
                if (!loading && failure == null && members.isEmpty)
                  const Padding(
                    padding: EdgeInsets.all(24),
                    child: Text('未找到群成员'),
                  ),
                if (more && failure == null)
                  TextButton(
                    onPressed: loading ? null : () => load(reset: false),
                    child: const Text('加载更多成员'),
                  ),
              ],
            ),
          ),
        ],
      ),
    ),
  );
}
