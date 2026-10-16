import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/error/app_failure.dart';
import '../contacts_providers.dart';
import '../data/contacts_repository.dart';
import '../../complaint/data/complaint_repository.dart';
import '../../complaint/presentation/complaint_page.dart';
import 'contact_widgets.dart';

class UserProfilePage extends ConsumerStatefulWidget {
  const UserProfilePage({super.key, required this.userId, this.groupId});
  final String userId;
  final String? groupId;
  @override
  ConsumerState<UserProfilePage> createState() => _UserProfilePageState();
}

class _UserProfilePageState extends ConsumerState<UserProfilePage> {
  bool busy = false;
  int _conversationView = 0;
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (ModalRoute.of(context)?.isCurrent == false) ++_conversationView;
  }
  @override
  void didUpdateWidget(covariant UserProfilePage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.userId != widget.userId || oldWidget.groupId != widget.groupId) ++_conversationView;
  }
  @override
  void dispose() { ++_conversationView; super.dispose(); }
  Future<void> run(
    Future<void> Function(ContactsRepository) operation, {
    String? success,
    bool Function()? isCurrent,
  }) async {
    final repository = ref.read(contactsRepositoryProvider);
    if (repository == null || busy || isCurrent?.call() == false) return;
    setState(() => busy = true);
    try {
      await operation(repository);
      if (mounted && repository.access.isCurrent && isCurrent?.call() != false) {
        refreshContacts(ref);
        if (success != null) contactNotice(context, success);
      }
    } on AppFailure catch (error) {
      if (mounted && repository.access.isCurrent && isCurrent?.call() != false) {
        contactNotice(context, error.message);
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> openConversation(ContactsRepository repository, String id) async {
    final view = _conversationView;
    final owner = repository.access.credentials;
    final group = widget.groupId;
    bool current() => mounted && view == _conversationView && widget.userId == id && widget.groupId == group &&
      repository.access.isCurrent && identical(repository.access.session.state.credentials, owner) &&
      identical(ref.read(contactsRepositoryProvider), repository) && ModalRoute.of(context)?.isCurrent == true;
    if (!current()) return;
    await run((repo) async {
      final conversation = await repo.conversation(id);
      if (mounted && current()) context.push('/chat/${Uri.encodeComponent(conversation)}');
    }, isCurrent: current);
  }

  @override
  Widget build(BuildContext context) {
    final repository = ref.watch(contactsRepositoryProvider);
    final profile = ref.watch(contactProfileProvider(widget.userId));
    final group = widget.groupId == null
        ? null
        : ref.watch(groupProfileProvider(widget.groupId!));
    if (repository == null) return const SizedBox.shrink();
    return Scaffold(
      appBar: AppBar(title: const Text('个人资料')),
      body: profile.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ContactProblem(
          error: e,
          retry: () => ref.invalidate(contactProfileProvider(widget.userId)),
        ),
        data: (user) {
          final me = repository.access.credentials.userId == user.id;
          if (widget.groupId != null &&
              (group?.isLoading == true || group?.hasError == true)) {
            return group!.when(
              data: (_) => const SizedBox.shrink(),
              loading: () => const Center(child: CircularProgressIndicator()),
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
                          if (user.remark.isNotEmpty) Text('昵称：${user.name}'),
                          const SizedBox(height: 8),
                          Wrap(
                            crossAxisAlignment: WrapCrossAlignment.center,
                            alignment: WrapAlignment.center,
                            children: [
                              SelectableText('ID：${user.id}'),
                              IconButton(
                                tooltip: '复制用户 ID',
                                onPressed: () async {
                                  await Clipboard.setData(
                                    ClipboardData(text: user.id),
                                  );
                                  if (context.mounted) {
                                    contactNotice(context, 'ID 已复制');
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
                  Card(
                    elevation: 0,
                    child: ListTile(
                      leading: const Icon(Icons.photo_library_outlined),
                      title: Text(me ? '我的动态' : '个人动态'),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: () => context.push(
                        '/moments/user/${Uri.encodeComponent(user.id)}',
                      ),
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
                          final value = await contactInput(
                            context,
                            '好友备注',
                            value: user.remark,
                          );
                          if (value != null &&
                              mounted &&
                              repository.access.isCurrent) {
                            await run((repo) => repo.remark(user.id, value));
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
                                if (!await contactConfirm(
                                  context,
                                  value ? '加入黑名单？' : '移出黑名单？',
                                  value
                                      ? '将屏蔽该用户的消息，可随时在黑名单中恢复。'
                                      : '恢复接收该用户的消息。',
                                )) {
                                  return;
                                }
                                if (mounted && repository.access.isCurrent) {
                                  await run(
                                    (repo) => repo.block(user.id, value),
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
                      onTap: () => Navigator.push(
                        context,
                        MaterialPageRoute(
                          builder: (_) => ComplaintPage(
                            target: ComplaintTarget.user,
                            targetId: user.id,
                            targetName: user.displayName,
                          ),
                        ),
                      ),
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
                                final reason = await contactInput(
                                  context,
                                  '添加好友',
                                  hint: '填写验证信息',
                                  maxLength: 256,
                                  lines: 3,
                                );
                                if (reason != null &&
                                    mounted &&
                                    repository.access.isCurrent) {
                                  await run(
                                    (repo) =>
                                        repo.requestFriend(user.id, reason),
                                    success: '好友申请已发送',
                                  );
                                }
                              },
                        child: const Text('添加好友'),
                      ),
                    ),
                  if (!me && !user.friend && !user.canRequestFriend)
                    const Padding(
                      padding: EdgeInsets.all(16),
                      child: Text('对方暂不接受好友申请', textAlign: TextAlign.center),
                    ),
                  if (!me && user.friend)
                    TextButton(
                      onPressed: busy
                          ? null
                          : () async {
                              if (!await contactConfirm(
                                context,
                                '删除好友？',
                                '你将从通讯录中移除该好友。',
                                action: '删除',
                              )) {
                                return;
                              }
                              if (!mounted || !repository.access.isCurrent) {
                                return;
                              }
                              await run(
                                (repo) => repo.deleteFriend(user.id),
                                success: '好友已删除',
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
