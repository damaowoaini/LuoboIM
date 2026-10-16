import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/error/app_failure.dart';
import '../../contacts/contacts_providers.dart';
import '../../contacts/presentation/contact_widgets.dart';
import '../../contacts/presentation/select_contacts_page.dart';
import '../data/group_repository.dart';
import '../domain/group.dart';
import '../../complaint/data/complaint_repository.dart';
import '../../complaint/presentation/complaint_page.dart';

class GroupPage extends ConsumerStatefulWidget {
  const GroupPage({super.key, required this.groupId});
  final String groupId;
  @override
  ConsumerState<GroupPage> createState() => _GroupPageState();
}

class _GroupPageState extends ConsumerState<GroupPage> {
  bool busy = false;
  Future<void> run(
    Future<void> Function(GroupRepository) operation, {
    String? success,
  }) async {
    final repository = ref.read(groupRepositoryProvider);
    if (repository == null || busy) return;
    setState(() => busy = true);
    try {
      await operation(repository);
      if (mounted && repository.access.isCurrent) {
        refreshContacts(ref);
        if (success != null) contactNotice(context, success);
      }
    } on AppFailure catch (error) {
      if (mounted && repository.access.isCurrent) {
        contactNotice(context, error.message);
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> edit(
    String title,
    String value,
    Future<void> Function(GroupRepository, String) operation, {
    int maxLength = 128,
    int lines = 1,
    String? success,
  }) async {
    final repository = ref.read(groupRepositoryProvider);
    if (repository == null) return;
    final result = await contactInput(
      context,
      title,
      value: value,
      maxLength: maxLength,
      lines: lines,
    );
    if (result != null && mounted && repository.access.isCurrent) {
      await run((repo) => operation(repo, result), success: success);
    }
  }

  @override
  Widget build(BuildContext context) {
    final repository = ref.watch(groupRepositoryProvider);
    if (repository == null) return const SizedBox.shrink();
    final profile = ref.watch(groupProfileProvider(widget.groupId));
    return Scaffold(
      appBar: AppBar(title: const Text('群资料')),
      body: profile.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ContactProblem(
          error: e,
          retry: () => ref.invalidate(groupProfileProvider(widget.groupId)),
        ),
        data: (group) => _body(group, repository),
      ),
    );
  }

  Widget _body(GroupProfile group, GroupRepository repository) {
    final id = widget.groupId;
    final active = group.joined && group.summary.status != 2;
    final manage = active && group.canManage;
    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 760),
        child: ListView(
          padding: const EdgeInsets.all(20),
          children: [
            Card(
              elevation: 0,
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Column(
                  children: [
                    ContactAvatar(
                      uri: group.summary.avatar,
                      group: true,
                      radius: 38,
                    ),
                    const SizedBox(height: 16),
                    Text(
                      group.summary.name,
                      textAlign: TextAlign.center,
                      style: const TextStyle(
                        fontSize: 24,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    Text('${group.summary.members} 人'),
                    Wrap(
                      crossAxisAlignment: WrapCrossAlignment.center,
                      alignment: WrapAlignment.center,
                      children: [
                        SelectableText('群 ID：$id'),
                        IconButton(
                          tooltip: '复制群 ID',
                          onPressed: () async {
                            await Clipboard.setData(ClipboardData(text: id));
                            if (mounted) {
                              contactNotice(context, '群 ID 已复制');
                            }
                          },
                          icon: const Icon(Icons.copy_outlined, size: 18),
                        ),
                      ],
                    ),
                  ],
                ),
              ),
            ),
            const SizedBox(height: 16),
            if (group.summary.status == 2)
              const Padding(
                padding: EdgeInsets.all(20),
                child: Text('该群已解散', textAlign: TextAlign.center),
              )
            else if (!active)
              FilledButton(
                onPressed: busy
                    ? null
                    : () => edit(
                        '申请加入群聊',
                        '',
                        (repo, reason) => repo.join(id, reason),
                        maxLength: 256,
                        lines: 3,
                        success: '入群请求已提交',
                      ),
                child: const Text('申请加入群聊'),
              )
            else
              FilledButton.icon(
                onPressed: busy
                    ? null
                    : () => run((repo) async {
                        final conversation = await repo.conversation(id);
                        if (mounted && repo.access.isCurrent) {
                          context.push(
                            '/chat/${Uri.encodeComponent(conversation)}',
                          );
                        }
                      }),
                icon: const Icon(Icons.chat_bubble_outline),
                label: const Text('进入群聊'),
              ),
            const SizedBox(height: 16),
            Card(
              elevation: 0,
              child: Column(
                children: [
                  ListTile(
                    title: const Text('群名称'),
                    subtitle: Text(group.summary.name),
                    trailing: manage ? const Icon(Icons.edit_outlined) : null,
                    enabled: !busy,
                    onTap: manage
                        ? () => edit(
                            '群名称',
                            group.summary.name,
                            (repo, value) => repo.update(id, name: value),
                          )
                        : null,
                  ),
                  ListTile(
                    title: const Text('群公告'),
                    subtitle: Text(
                      group.announcement.isEmpty ? '暂无群公告' : group.announcement,
                    ),
                    isThreeLine: false,
                    trailing: manage ? const Icon(Icons.edit_outlined) : null,
                    enabled: !busy,
                    onTap: manage
                        ? () => edit(
                            '群公告',
                            group.announcement,
                            (repo, value) =>
                                repo.update(id, announcement: value),
                            maxLength: 10000,
                            lines: 6,
                          )
                        : null,
                  ),
                  if (group.introduction.isNotEmpty)
                    ListTile(
                      title: const Text('群介绍'),
                      subtitle: Text(group.introduction),
                    ),
                  if (active)
                    ListTile(
                      title: const Text('群成员'),
                      subtitle: Text('${group.summary.members} 人'),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: busy
                          ? null
                          : () => context.push('${groupPath(id)}/members'),
                    ),
                  if (active)
                    ListTile(
                      title: const Text('我在本群的昵称'),
                      subtitle: Text(group.me?.name ?? ''),
                      trailing: const Icon(Icons.edit_outlined),
                      onTap: busy
                          ? null
                          : () => edit(
                              '我在本群的昵称',
                              group.me?.name ?? '',
                              (repo, value) => repo.nickname(id, value),
                            ),
                    ),
                  if (active)
                    ListTile(
                      leading: const Icon(Icons.person_add_outlined),
                      title: const Text('邀请好友'),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: busy
                          ? null
                          : () async {
                              final chosen = await selectContacts(
                                context,
                                title: '邀请好友入群',
                                excluded: {
                                  repository.access.credentials.userId,
                                },
                              );
                              if (chosen != null &&
                                  chosen.isNotEmpty &&
                                  mounted &&
                                  repository.access.isCurrent) {
                                await run(
                                  (repo) => repo.invite(
                                    id,
                                    chosen.map((user) => user.id).toList(),
                                  ),
                                  success: '邀请已发送',
                                );
                              }
                            },
                    ),
                ],
              ),
            ),
            if (manage) ...[
              const Padding(
                padding: EdgeInsets.symmetric(horizontal: 8, vertical: 16),
                child: Text(
                  '群管理',
                  style: TextStyle(fontWeight: FontWeight.w600),
                ),
              ),
              Card(
                elevation: 0,
                child: Column(
                  children: [
                    SwitchListTile(
                      title: const Text('全员禁言'),
                      subtitle: const Text('群主和管理员仍可发言'),
                      value: group.summary.status == 3,
                      onChanged: busy
                          ? null
                          : (value) => run((repo) => repo.setMute(id, value)),
                    ),
                    SwitchListTile(
                      title: const Text('禁止查看成员资料'),
                      value: group.hideMemberProfiles,
                      onChanged: busy
                          ? null
                          : (value) =>
                                run((repo) => repo.setHideProfiles(id, value)),
                    ),
                    SwitchListTile(
                      title: const Text('禁止通过群聊添加好友'),
                      value: group.preventMemberFriends,
                      onChanged: busy
                          ? null
                          : (value) => run(
                              (repo) => repo.setPreventFriends(id, value),
                            ),
                    ),
                    ListTile(
                      title: const Text('入群验证'),
                      subtitle: Text(switch (group.verification) {
                        1 => '申请和普通成员邀请均需验证',
                        2 => '无需验证',
                        _ => '申请需验证，邀请直接入群',
                      }),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: busy
                          ? null
                          : () async {
                              final value = await showModalBottomSheet<int>(
                                context: context,
                                builder: (context) => SafeArea(
                                  child: Column(
                                    mainAxisSize: MainAxisSize.min,
                                    children: [
                                      for (final entry in {
                                        0: '申请需验证，邀请直接入群',
                                        1: '申请和普通成员邀请均需验证',
                                        2: '无需验证',
                                      }.entries)
                                        ListTile(
                                          title: Text(entry.value),
                                          trailing:
                                              group.verification == entry.key
                                              ? const Icon(Icons.check)
                                              : null,
                                          onTap: () =>
                                              Navigator.pop(context, entry.key),
                                        ),
                                    ],
                                  ),
                                ),
                              );
                              if (value != null &&
                                  mounted &&
                                  repository.access.isCurrent) {
                                await run(
                                  (repo) => repo.verification(id, value),
                                );
                              }
                            },
                    ),
                  ],
                ),
              ),
            ],
            ListTile(
              leading: const Icon(Icons.flag_outlined),
              title: const Text('投诉此群组'),
              trailing: const Icon(Icons.chevron_right),
              enabled: !busy,
              onTap: () => Navigator.push(
                context,
                MaterialPageRoute(
                  builder: (_) => ComplaintPage(
                    target: ComplaintTarget.group,
                    targetId: id,
                    targetName: group.summary.name,
                  ),
                ),
              ),
            ),
            if (active)
              Padding(
                padding: const EdgeInsets.only(top: 24),
                child: TextButton(
                  onPressed: busy
                      ? null
                      : () async {
                          final owner = group.me?.owner == true;
                          if (!await contactConfirm(
                            context,
                            owner ? '解散群聊？' : '退出群聊？',
                            owner
                                ? '解散后所有成员将无法继续使用该群，此操作无法撤销。若只想离开，请先在成员列表转让群主。'
                                : '退出后将不再接收本群新消息。',
                            action: owner ? '解散' : '退出',
                          )) {
                            return;
                          }
                          if (!mounted || !repository.access.isCurrent) return;
                          await run((repo) async {
                            if (owner) {
                              await repo.dismiss(id);
                            } else {
                              await repo.leave(id);
                            }
                            if (mounted && repo.access.isCurrent) {
                              context.go('/contacts');
                            }
                          });
                        },
                  child: Text(
                    group.me?.owner == true ? '解散群聊' : '退出群聊',
                    style: TextStyle(
                      color: Theme.of(context).colorScheme.error,
                    ),
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
  }
}
