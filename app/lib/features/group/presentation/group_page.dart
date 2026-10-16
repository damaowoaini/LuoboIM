import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/network/tenant_api.dart';
import '../../../core/storage/object_uploader.dart';
import '../../../core/storage/staged_image.dart';
import '../../../providers.dart';
import '../../contacts/contacts_providers.dart';
import '../../contacts/presentation/contact_widgets.dart';
import '../../contacts/presentation/select_contacts_page.dart';
import '../data/group_repository.dart';
import '../domain/group.dart';
import '../../complaint/data/complaint_repository.dart';
import '../../complaint/presentation/complaint_page.dart';

// Keep the existing OS picker and signed upload adapters lazy. Overrides replace
// platform/HTTP transport in tests, while staging and metadata use real code.
final groupAvatarPickerProvider =
    Provider<Future<XFile?> Function(ImageSource)>(
      (_) =>
          (source) => ImagePicker().pickImage(
            source: source,
            maxWidth: 2048,
            maxHeight: 2048,
            imageQuality: 90,
            requestFullMetadata: false,
          ),
    );
final groupAvatarUploaderProvider =
    Provider<ObjectUploader Function(TenantApi)>(
      (_) =>
          (api) => ObjectUploader(api),
    );

class GroupPage extends ConsumerStatefulWidget {
  const GroupPage({super.key, required this.groupId});
  final String groupId;
  @override
  ConsumerState<GroupPage> createState() => _GroupPageState();
}

class _GroupPageState extends ConsumerState<GroupPage> {
  bool busy = false;
  int _avatarTurn = 0, _avatarView = 0;
  bool _avatarChoosing = false, _avatarPending = false;
  bool _avatarMetadataPending = false;
  int _avatarOverlays = 0;
  double? _avatarProgress;
  String? _avatarFailure;
  final _avatarUploaders = <ObjectUploader>{};

  void _leaveAvatarView() {
    _avatarView++;
    _avatarTurn++;
    for (final uploader in _avatarUploaders.toList()) {
      uploader.close();
    }
    _avatarChoosing = _avatarPending = _avatarMetadataPending = false;
    _avatarProgress = null;
    _avatarFailure = null;
  }

  @override
  void didUpdateWidget(covariant GroupPage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.groupId != widget.groupId) _leaveAvatarView();
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    // Our source/preview dialogs deliberately cover this route. Other route
    // changes invalidate the old view even if the user later comes back.
    if (_avatarPending &&
        _avatarOverlays == 0 &&
        ModalRoute.of(context)?.isCurrent == false) {
      _leaveAvatarView();
    }
  }

  @override
  void dispose() {
    _leaveAvatarView();
    super.dispose();
  }

  Future<void> changeAvatar(
    GroupRepository repository,
    GroupProfile renderedGroup,
  ) async {
    if (!mounted || busy || _avatarChoosing || _avatarMetadataPending) return;
    final api = ref.read(tenantApiProvider);
    final access = repository.access;
    final owner = access.credentials;
    final id = widget.groupId;
    final route = ModalRoute.of(context);
    final view = _avatarView;
    if (api == null ||
        !identical(api.access.credentials, owner) ||
        api.access.generation != access.generation ||
        !access.isCurrent ||
        !renderedGroup.canManage ||
        renderedGroup.summary.status == 2 ||
        renderedGroup.summary.id != id ||
        route?.isCurrent != true) {
      return;
    }
    final turn = ++_avatarTurn;
    final pick = ref.read(groupAvatarPickerProvider);
    final makeUploader = ref.read(groupAvatarUploaderProvider);
    bool owns() =>
        mounted &&
        _avatarView == view &&
        _avatarTurn == turn &&
        widget.groupId == id &&
        identical(ref.read(groupRepositoryProvider), repository) &&
        identical(ref.read(tenantApiProvider), api) &&
        access.isCurrent &&
        identical(access.session.state.credentials, owner);
    bool current() {
      if (!owns() || route?.isCurrent != true) return false;
      final latest = ref.read(groupProfileProvider(id)).asData?.value;
      return latest != null &&
          latest.summary.id == id &&
          latest.canManage &&
          latest.summary.status != 2;
    }

    setState(() {
      _avatarChoosing = _avatarPending = true;
      _avatarFailure = null;
      _avatarProgress = null;
    });
    StagedImage? staged;
    ObjectUploader? uploader;
    try {
      _avatarOverlays++;
      ImageSource? source;
      var sourceFinished = false;
      try {
        source = await showModalBottomSheet<ImageSource>(
          context: context,
          showDragHandle: true,
          builder: (context) {
            final overlay = ModalRoute.of(context);
            if (!sourceFinished &&
                overlay?.isActive == true &&
                overlay?.isCurrent == false) {
              _leaveAvatarView();
            }
            return SafeArea(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  ListTile(
                    leading: const Icon(Icons.photo_library_outlined),
                    title: const Text('从相册选择'),
                    onTap: () {
                      sourceFinished = true;
                      Navigator.pop(context, ImageSource.gallery);
                    },
                  ),
                  ListTile(
                    leading: const Icon(Icons.camera_alt_outlined),
                    title: const Text('拍照'),
                    onTap: () {
                      sourceFinished = true;
                      Navigator.pop(context, ImageSource.camera);
                    },
                  ),
                ],
              ),
            );
          },
        );
      } finally {
        _avatarOverlays--;
      }
      if (source == null || !current()) return;
      final selected = await pick(source);
      if (selected == null || !current()) return;
      staged = await ImageStager(
        ref.read(imGatewayProvider),
        access,
      ).prepare(selected.path);
      if (!mounted || !current()) return;
      _avatarOverlays++;
      bool? approved;
      var previewFinished = false;
      try {
        approved = await showDialog<bool>(
          context: context,
          builder: (context) {
            final overlay = ModalRoute.of(context);
            if (!previewFinished &&
                overlay?.isActive == true &&
                overlay?.isCurrent == false) {
              _leaveAvatarView();
            }
            return AlertDialog(
              title: const Text('使用这张群头像？'),
              content: Image.file(
                staged!.file,
                cacheWidth: 512,
                width: 220,
                height: 220,
                fit: BoxFit.contain,
                semanticLabel: '所选群头像预览',
              ),
              actions: [
                TextButton(
                  onPressed: () {
                    previewFinished = true;
                    Navigator.pop(context, false);
                  },
                  child: const Text('取消'),
                ),
                FilledButton(
                  onPressed: () {
                    previewFinished = true;
                    Navigator.pop(context, true);
                  },
                  child: const Text('使用'),
                ),
              ],
            );
          },
        );
      } finally {
        _avatarOverlays--;
      }
      if (approved != true || !current()) return;
      // Read current management permission before starting provider I/O. The
      // repository repeats this read before the eventual metadata dispatch.
      final latest = await repository.profile(id);
      if (!current()) return;
      if (!latest.canManage || latest.summary.status == 2) {
        throw const AppFailure(FailureKind.permissionDenied);
      }
      uploader = makeUploader(api);
      _avatarUploaders.add(uploader);
      setState(() {
        _avatarChoosing = false;
        _avatarProgress = 0;
      });
      final uri = await uploader.upload(
        staged.file,
        contentType: staged.contentType,
        extension: staged.extension,
        group: 'avatar',
        progress: (value) {
          if (current()) setState(() => _avatarProgress = value);
        },
      );
      if (!current()) return;
      // Synchronous admission: never begin another upload while this exact
      // metadata intent is awaiting an ACK. Unknown results are not replayed.
      setState(() => _avatarMetadataPending = true);
      await repository.update(id, avatar: uri.toString(), isCurrent: current);
      if (!mounted || !current()) return;
      ref.invalidate(groupProfileProvider(id));
      contactNotice(context, '群头像已更新');
    } catch (error) {
      if (current()) {
        setState(() {
          _avatarFailure =
              '${error is AppFailure ? error.message : '群头像更新未能确认'}。请刷新核对后重新选择。';
        });
      }
    } finally {
      uploader?.close();
      _avatarUploaders.remove(uploader);
      await staged?.dispose();
      // Internal loading cleanup does not require a foreground route; a dialog
      // or another page covering us must not leave a permanent spinner.
      if (owns()) {
        setState(() {
          _avatarChoosing = _avatarPending = _avatarMetadataPending = false;
          _avatarProgress = null;
        });
      }
    }
  }

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
    ref.listen(groupRepositoryProvider, (previous, next) {
      if (!identical(previous, next)) {
        setState(_leaveAvatarView);
      }
    });
    // Depend on route visibility so leaving and returning cannot revive an old
    // picker/upload continuation. Own dialogs are handled above.
    ModalRoute.of(context);
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
                    if (manage) ...[
                      const SizedBox(height: 8),
                      OutlinedButton.icon(
                        key: const ValueKey('group-avatar-edit'),
                        onPressed:
                            busy || _avatarChoosing || _avatarMetadataPending
                            ? null
                            : () => changeAvatar(repository, group),
                        icon: const Icon(Icons.add_a_photo_outlined),
                        label: Text(
                          _avatarMetadataPending
                              ? '正在确认群头像'
                              : _avatarPending
                              ? '重新选择群头像'
                              : '更换群头像',
                        ),
                      ),
                      if (_avatarPending) ...[
                        const SizedBox(height: 8),
                        LinearProgressIndicator(value: _avatarProgress),
                        const SizedBox(height: 8),
                        Text(
                          _avatarMetadataPending
                              ? '正在保存，请稍候'
                              : _avatarChoosing
                              ? '请选择并确认头像'
                              : '头像上传中，可重新选择',
                          textAlign: TextAlign.center,
                          semanticsLabel: '群头像更新进度',
                        ),
                      ],
                      if (_avatarFailure != null) ...[
                        const SizedBox(height: 8),
                        Semantics(
                          liveRegion: true,
                          child: Text(
                            _avatarFailure!,
                            textAlign: TextAlign.center,
                          ),
                        ),
                        TextButton.icon(
                          onPressed: () =>
                              ref.invalidate(groupProfileProvider(id)),
                          icon: const Icon(Icons.refresh),
                          label: const Text('刷新核对群头像'),
                        ),
                      ],
                    ],
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
