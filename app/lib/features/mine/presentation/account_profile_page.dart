import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/storage/object_uploader.dart';
import '../../../core/storage/staged_image.dart';
import '../../../design/components/feedback.dart';
import '../../../providers.dart';
import '../account_providers.dart';
import '../data/account_repository.dart';
import 'mine_page.dart' show mineProfileProvider;

class AccountProfilePage extends ConsumerStatefulWidget {
  const AccountProfilePage({super.key});
  @override
  ConsumerState<AccountProfilePage> createState() => _AccountProfilePageState();
}

class _AccountProfilePageState extends ConsumerState<AccountProfilePage> {
  bool busy = false, committing = false, cancelRequested = false;
  double? progress;
  String? failure;
  String? notice;
  ObjectUploader? uploader;

  Future<void> perform(
    AccountRepository repository,
    Future<AccountSaveResult> Function() action,
  ) async {
    if (busy || !repository.isCurrent) return;
    setState(() {
      busy = true;
      failure = null;
      notice = null;
    });
    try {
      final result = await action();
      if (!mounted || !repository.isCurrent) return;
      ref.invalidate(accountProfileProvider);
      ref.invalidate(mineProfileProvider);
      if (result.imSyncPending) {
        setState(() => notice = '资料已保存，昵称和头像正在同步');
      } else {
        showNotice(context, '资料已更新');
      }
    } catch (error) {
      if (mounted && repository.isCurrent) {
        setState(
          () => failure =
              '${error is AppFailure ? error.message : '资料未能确认更新'}。可刷新核对当前资料。',
        );
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> nickname(AccountProfile profile) async {
    final repository = ref.read(accountRepositoryProvider);
    if (repository == null || busy) return;
    final result = await showDialog<String>(
      context: context,
      builder: (_) => _NicknameDialog(initial: profile.nickname),
    );
    if (result == null || !mounted || !repository.isCurrent) return;
    await perform(repository, () => repository.updateProfile(nickname: result));
  }

  Future<void> gender() async {
    final repository = ref.read(accountRepositoryProvider);
    if (repository == null || busy) return;
    final selected = await showModalBottomSheet<int>(
      context: context,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            for (final entry in const {0: '未设置', 1: '男', 2: '女'}.entries)
              ListTile(
                title: Text(entry.value),
                onTap: () => Navigator.pop(context, entry.key),
              ),
          ],
        ),
      ),
    );
    if (selected == null || !mounted || !repository.isCurrent) return;
    await perform(repository, () => repository.updateProfile(gender: selected));
  }

  Future<void> birthday(AccountProfile profile) async {
    final repository = ref.read(accountRepositoryProvider);
    if (repository == null || busy) return;
    final now = DateTime.now();
    final selected = await showDatePicker(
      context: context,
      firstDate: DateTime(1900),
      lastDate: now,
      initialDate:
          profile.birthday ?? DateTime(now.year - 18, now.month, now.day),
    );
    if (selected == null || !mounted || !repository.isCurrent) return;
    await perform(
      repository,
      () => repository.updateProfile(birthday: selected),
    );
  }

  Future<void> avatar(AccountProfile profile) async {
    final repository = ref.read(accountRepositoryProvider);
    if (repository == null || busy) return;
    final source = await showModalBottomSheet<Object>(
      context: context,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.photo_library_outlined),
              title: const Text('从相册选择'),
              onTap: () => Navigator.pop(context, ImageSource.gallery),
            ),
            ListTile(
              leading: const Icon(Icons.camera_alt_outlined),
              title: const Text('拍照'),
              onTap: () => Navigator.pop(context, ImageSource.camera),
            ),
            if (profile.hasStoredAvatar)
              ListTile(
                leading: const Icon(Icons.person_remove_outlined),
                title: const Text('移除头像'),
                onTap: () => Navigator.pop(context, 'clear'),
              ),
          ],
        ),
      ),
    );
    if (source == null || !mounted || !repository.isCurrent) return;
    if (source == 'clear') {
      if (!await confirmAction(
            context,
            '移除当前头像？',
            '移除后使用默认头像。',
            action: '移除',
          ) ||
          !mounted ||
          !repository.isCurrent) {
        return;
      }
      await perform(
        repository,
        () => repository.updateProfile(clearAvatar: true),
      );
      return;
    }
    if (source is! ImageSource) return;
    setState(() {
      busy = true;
      cancelRequested = false;
      failure = null;
    });
    StagedImage? staged;
    try {
      final selected = await ImagePicker().pickImage(
        source: source,
        maxWidth: 2048,
        maxHeight: 2048,
        imageQuality: 90,
        requestFullMetadata: false,
      );
      if (selected == null || !mounted || !repository.isCurrent) return;
      staged = await ImageStager(
        ref.read(imGatewayProvider),
        repository.api.access,
      ).prepare(selected.path);
      if (!mounted || !repository.isCurrent) return;
      final approved = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('使用这张头像？'),
          content: Image.file(
            staged!.file,
            cacheWidth: 512,
            width: 220,
            height: 220,
            fit: BoxFit.contain,
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('使用'),
            ),
          ],
        ),
      );
      if (approved != true || !mounted || !repository.isCurrent) return;
      uploader = ObjectUploader(repository.api);
      setState(() => progress = 0);
      final uri = await uploader!.upload(
        staged.file,
        contentType: staged.contentType,
        extension: staged.extension,
        group: 'avatar',
        progress: (value) {
          if (mounted && repository.isCurrent) setState(() => progress = value);
        },
      );
      if (cancelRequested || !mounted || !repository.isCurrent) return;
      setState(() => committing = true);
      final result = await repository.updateProfile(avatar: uri);
      if (!mounted || !repository.isCurrent) return;
      ref.invalidate(accountProfileProvider);
      ref.invalidate(mineProfileProvider);
      if (result.imSyncPending) {
        setState(() => notice = '头像已保存，正在同步');
      } else {
        showNotice(context, '头像已更新');
      }
    } catch (error) {
      if (mounted && repository.isCurrent && !cancelRequested) {
        setState(
          () => failure =
              '${error is AppFailure ? error.message : '头像更新未能确认'}。可刷新核对当前头像。',
        );
      }
    } finally {
      uploader?.close();
      uploader = null;
      await staged?.dispose();
      if (mounted) {
        setState(() {
          busy = false;
          committing = false;
          progress = null;
        });
      }
    }
  }

  @override
  void dispose() {
    uploader?.close();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final profile = ref.watch(accountProfileProvider);
    return PopScope(
      canPop: !busy,
      child: Scaffold(
        appBar: AppBar(
          title: const Text('个人资料'),
          actions: [
            IconButton(
              tooltip: '刷新资料',
              onPressed: busy
                  ? null
                  : () => ref.invalidate(accountProfileProvider),
              icon: const Icon(Icons.refresh),
            ),
          ],
        ),
        body: profile.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (error, _) => Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(error is AppFailure ? error.message : '暂时无法读取资料'),
                TextButton(
                  onPressed: () => ref.invalidate(accountProfileProvider),
                  child: const Text('重试'),
                ),
              ],
            ),
          ),
          data: (user) => ListView(
            padding: const EdgeInsets.all(16),
            children: [
              ListTile(
                title: const Text('头像'),
                onTap: busy ? null : () => avatar(user),
                leading: CircleAvatar(
                  backgroundImage: user.avatar == null
                      ? null
                      : NetworkImage(user.avatar.toString()),
                  onBackgroundImageError: user.avatar == null
                      ? null
                      : (_, _) {},
                  child: user.avatar == null
                      ? const Icon(Icons.person_outline)
                      : null,
                ),
                trailing: const Icon(Icons.chevron_right),
              ),
              ListTile(
                title: const Text('昵称'),
                subtitle: Text(user.nickname),
                onTap: busy ? null : () => nickname(user),
                trailing: const Icon(Icons.chevron_right),
              ),
              ListTile(
                title: const Text('我的 ID'),
                subtitle: SelectableText(user.userId),
                trailing: IconButton(
                  tooltip: '复制我的 ID',
                  onPressed: () async {
                    await Clipboard.setData(ClipboardData(text: user.userId));
                    if (context.mounted) showNotice(context, 'ID 已复制');
                  },
                  icon: const Icon(Icons.copy_outlined),
                ),
              ),
              ListTile(
                title: const Text('性别'),
                subtitle: Text(switch (user.gender) {
                  1 => '男',
                  2 => '女',
                  _ => '未设置',
                }),
                onTap: busy ? null : gender,
                trailing: const Icon(Icons.chevron_right),
              ),
              ListTile(
                title: const Text('生日'),
                subtitle: Text(
                  user.birthday == null
                      ? '未设置'
                      : '${user.birthday!.year}年${user.birthday!.month}月${user.birthday!.day}日',
                ),
                onTap: busy ? null : () => birthday(user),
                trailing: const Icon(Icons.chevron_right),
              ),
              const Divider(),
              ListTile(
                title: const Text('绑定邮箱'),
                subtitle: Text(user.email.isEmpty ? '未绑定' : user.email),
              ),
              ListTile(
                title: const Text('绑定手机'),
                subtitle: Text(
                  user.phone.isEmpty ? '未绑定' : '${user.areaCode} ${user.phone}',
                ),
              ),
              if (progress != null) ...[
                const SizedBox(height: 12),
                LinearProgressIndicator(value: progress),
                TextButton(
                  onPressed: committing
                      ? null
                      : () {
                          cancelRequested = true;
                          uploader?.close();
                        },
                  child: Text(committing ? '正在保存头像' : '取消上传'),
                ),
              ] else if (busy)
                const LinearProgressIndicator(),
              if (notice != null)
                Padding(
                  padding: const EdgeInsets.all(12),
                  child: Text(notice!),
                ),
              if (failure != null)
                Padding(
                  padding: const EdgeInsets.all(12),
                  child: Text(
                    failure!,
                    style: TextStyle(
                      color: Theme.of(context).colorScheme.error,
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

class _NicknameDialog extends StatefulWidget {
  const _NicknameDialog({required this.initial});
  final String initial;
  @override
  State<_NicknameDialog> createState() => _NicknameDialogState();
}

class _NicknameDialogState extends State<_NicknameDialog> {
  late final controller = TextEditingController(text: widget.initial);
  @override
  void dispose() {
    controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('修改昵称'),
    content: TextField(
      controller: controller,
      autofocus: true,
      maxLength: 64,
      textInputAction: TextInputAction.done,
      decoration: const InputDecoration(labelText: '昵称'),
      onSubmitted: (_) => Navigator.pop(context, controller.text),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('取消'),
      ),
      FilledButton(
        onPressed: () => Navigator.pop(context, controller.text),
        child: const Text('保存'),
      ),
    ],
  );
}
