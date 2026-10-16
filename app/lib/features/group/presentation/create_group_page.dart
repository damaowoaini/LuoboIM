import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/network/tenant_api.dart';
import '../../../core/storage/object_uploader.dart';
import '../../../core/storage/staged_image.dart';
import '../../../providers.dart';
import '../../contacts/contacts_providers.dart';
import '../../contacts/domain/contact.dart';
import '../../contacts/presentation/contact_widgets.dart';
import '../data/group_repository.dart';

// OS selection and HTTP transport are replaceable, while the real private
// staging/decoder and signed uploader remain in the actual creation path.
final groupCreateAvatarPickerProvider = Provider<Future<XFile?> Function(ImageSource)>(
  (_) => (source) => ImagePicker().pickImage(source: source, maxWidth: 2048,
    maxHeight: 2048, imageQuality: 90, requestFullMetadata: false),
);
final groupCreateAvatarUploaderProvider = Provider<ObjectUploader Function(TenantApi)>(
  (_) => (api) => ObjectUploader(api),
);

class CreateGroupPage extends ConsumerStatefulWidget {
  const CreateGroupPage({super.key, required this.members, required this.owner});
  final List<Contact> members;
  // The selector captures this before push; mounting after an owner change
  // must not turn the old selected people into a new account's draft.
  final GroupRepository owner;
  @override
  ConsumerState<CreateGroupPage> createState() => _CreateGroupPageState();
}

class _CreateGroupPageState extends ConsumerState<CreateGroupPage> {
  late final GroupRepository repository;
  late final TenantApi? api;
  late final List<Contact> members;
  late final TextEditingController name;
  StreamSubscription<int>? invalidation;
  final revision = ValueNotifier(0);
  final uploaders = <ObjectUploader>{};
  bool revisionDisposed = false;
  int view = 0, turn = 0, overlays = 0;
  bool choosing = false, uploading = false, creating = false, dispatched = false;
  double? progress;
  Uri? avatar;
  String? avatarFailure, failure;
  CreatedGroup? created;

  @override
  void initState() {
    super.initState();
    repository = widget.owner;
    api = ref.read(tenantApiProvider);
    members = List.unmodifiable(widget.members);
    name = TextEditingController(text: members.take(3).map((m) => m.displayName).join('、')
      .characters.take(GroupRepository.maximumCreationNameLength).toString());
    invalidation = repository.access.session.invalidations.listen((_) => retire());
  }
  bool get ownerCurrent => mounted && repository.access.isCurrent &&
    identical(repository.access.session.state.credentials, repository.access.credentials) &&
    identical(repository, ref.read(groupRepositoryProvider)) &&
    identical(api, ref.read(tenantApiProvider)) &&
    (api == null || identical(api!.access.credentials, repository.access.credentials));
  bool owns(int original) => ownerCurrent && original == view;
  bool active(int original) => owns(original) && ModalRoute.of(context)?.isCurrent == true;
  void retire() {
    ++view; ++turn;
    for (final uploader in uploaders.toList()) { uploader.close(); }
    choosing = uploading = false; progress = null;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) { revision.value++; setState(() {}); }
    });
    WidgetsBinding.instance.ensureVisualUpdate();
  }
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (overlays == 0 && ModalRoute.of(context)?.isCurrent == false) retire();
  }
  @override
  void dispose() {
    ++view; ++turn;
    unawaited(invalidation?.cancel());
    for (final uploader in uploaders.toList()) { uploader.close(); }
    name.dispose();
    // An owned overlay may outlive this route while a foreign route covers it.
    // Retire its sensitive content, and release its notifier only after its
    // final listener is removed. Never pop the covering route.
    if (overlays == 0) {
      revision.dispose(); revisionDisposed = true;
    } else {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!revisionDisposed) revision.value++;
      });
    }
    super.dispose();
  }

  Future<T?> ownDialog<T>(int original,
      Widget Function(BuildContext, bool Function()) builder, {bool sheet = false}) async {
    if (!active(original)) return null;
    overlays++;
    Widget content(BuildContext dialogContext) => Consumer(builder: (dialogContext, dialogRef, _) {
      dialogRef.watch(groupRepositoryProvider);
      dialogRef.watch(tenantApiProvider);
      return ValueListenableBuilder<int>(valueListenable: revision, builder: (_, _, _) {
        bool allowed() => owns(original) && ModalRoute.of(dialogContext)?.isCurrent == true;
        return _CreationOverlay(onCovered: retire, child: !owns(original)
          ? AlertDialog(title: const Text('本次选择已失效'),
              content: const Text('请关闭后重新核对。'), actions: [
                TextButton(onPressed: () {
                  if (ModalRoute.of(dialogContext)?.isCurrent == true) Navigator.pop(dialogContext);
                }, child: const Text('关闭')),
              ]) : builder(dialogContext, allowed));
      });
    });
    try {
      return sheet
        ? await showModalBottomSheet<T>(context: context, showDragHandle: true, builder: content)
        : await showDialog<T>(context: context, builder: content);
    } finally {
      overlays--;
      if (mounted) setState(() {});
      else if (overlays == 0 && !revisionDisposed) {
        revision.dispose(); revisionDisposed = true;
      }
    }
  }

  Future<void> chooseAvatar() async {
    final original = view;
    final uploadApi = api;
    if (!active(original) || dispatched || creating || choosing) return;
    if (uploadApi == null) {
      setState(() => avatarFailure = const AppFailure(FailureKind.configuration).message);
      return;
    }
    final selectedTurn = ++turn;
    final pick = ref.read(groupCreateAvatarPickerProvider);
    final makeUploader = ref.read(groupCreateAvatarUploaderProvider);
    bool current() => active(original) && selectedTurn == turn && !dispatched &&
      identical(pick, ref.read(groupCreateAvatarPickerProvider)) &&
      identical(makeUploader, ref.read(groupCreateAvatarUploaderProvider));
    setState(() { choosing = true; uploading = false; progress = null; avatarFailure = null; });
    StagedImage? staged;
    ObjectUploader? uploader;
    try {
      final source = await ownDialog<ImageSource>(original, (ctx, allowed) => SafeArea(
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          ListTile(leading: const Icon(Icons.photo_library_outlined), title: const Text('从相册选择'),
            onTap: () { if (allowed()) Navigator.pop(ctx, ImageSource.gallery); }),
          ListTile(leading: const Icon(Icons.camera_alt_outlined), title: const Text('拍照'),
            onTap: () { if (allowed()) Navigator.pop(ctx, ImageSource.camera); }),
        ])), sheet: true);
      if (source == null || !current()) return;
      final selected = await pick(source);
      if (selected == null || !current()) return;
      staged = await ImageStager(ref.read(imGatewayProvider), repository.access).prepare(selected.path);
      if (!mounted || !current()) return;
      final preview = staged;
      final approved = await ownDialog<bool>(original, (ctx, allowed) => AlertDialog(
        title: const Text('使用这张群头像？'),
        content: Image.file(preview.file, width: 220, height: 220, fit: BoxFit.contain,
          cacheWidth: 512, semanticLabel: '待创建群头像预览'),
        actions: [
          TextButton(onPressed: () {
            if (ModalRoute.of(ctx)?.isCurrent == true) Navigator.pop(ctx, false);
          }, child: const Text('取消')),
          FilledButton(onPressed: () { if (allowed()) Navigator.pop(ctx, true); }, child: const Text('使用')),
        ]));
      if (approved != true || !current()) return;
      uploader = makeUploader(uploadApi);
      uploaders.add(uploader);
      setState(() { choosing = false; uploading = true; progress = 0; });
      final url = await uploader.upload(preview.file, contentType: preview.contentType,
        extension: preview.extension, group: 'avatar',
        progress: (value) { if (current()) setState(() => progress = value); });
      if (current()) setState(() { avatar = url; avatarFailure = null; });
    } catch (error) {
      if (current()) {
        setState(() => avatarFailure = error is AppFailure
          ? error.message : '头像上传未完成，请重新选择');
      }
    } finally {
      uploader?.close(); uploaders.remove(uploader);
      await staged?.dispose();
      if (mounted && selectedTurn == turn) setState(() { choosing = uploading = false; progress = null; });
    }
  }

  Future<void> submit() async {
    final original = view;
    if (!active(original) || creating || dispatched || choosing || uploading) return;
    final title = name.text;
    final selected = List<String>.unmodifiable(members.map((m) => m.id));
    final face = avatar?.toString();
    setState(() { creating = true; failure = null; });
    try {
      final receipt = await repository.createRecord(title, selected, avatar: face,
        isCurrent: () => active(original), onDispatched: () { setState(() => dispatched = true); });
      if (!active(original)) return;
      setState(() => created = receipt);
      await openCreated(receipt, original);
    } catch (error) {
      if (active(original)) {
        setState(() => failure = dispatched
          ? '群创建结果未确认，请前往群聊列表核对。本页不会再次提交。'
          : error is AppFailure ? error.message : '请检查填写的信息后重试');
      }
    } finally {
      if (mounted) setState(() => creating = false);
    }
  }
  Future<void> openCreated(CreatedGroup receipt, int original) async {
    if (!active(original) || !identical(receipt, created)) return;
    try {
      final cid = await repository.conversation(receipt.id, isCurrent: () => active(original));
      if (!mounted || !active(original)) return;
      refreshContacts(ref);
      context.go('/chat/${Uri.encodeComponent(cid)}');
    } catch (_) {
      if (active(original)) setState(() => failure = '群已创建，暂时无法打开会话。可查看群资料或重试打开。');
    }
  }
  Future<void> retryOpen() async {
    final receipt = created, original = view;
    if (receipt == null || creating || !active(original)) return;
    setState(() => creating = true);
    try { await openCreated(receipt, original); }
    finally { if (mounted) setState(() => creating = false); }
  }

  @override
  Widget build(BuildContext context) {
    ref.watch(groupRepositoryProvider); ref.watch(tenantApiProvider);
    final valid = ownerCurrent;
    final frozen = dispatched || creating;
    return PopScope(canPop: !valid || (!creating && !dispatched), child: Scaffold(
      appBar: AppBar(title: const Text('创建群聊')),
      body: !valid ? const Center(child: Text('会话已变化，请返回后重新选择成员')) : SafeArea(
        child: Center(child: ConstrainedBox(constraints: const BoxConstraints(maxWidth: 720),
          child: ListView(padding: const EdgeInsets.all(16), children: [
            Center(child: ContactAvatar(uri: avatar, group: true, radius: 34)),
            const SizedBox(height: 8),
            Wrap(alignment: WrapAlignment.center, spacing: 8, runSpacing: 8, children: [
              OutlinedButton.icon(key: const ValueKey('create-group-avatar'),
                onPressed: frozen || choosing ? null : chooseAvatar,
                icon: const Icon(Icons.add_a_photo_outlined),
                label: Text(uploading ? '上传中可重新选择' : avatar == null ? '选择群头像' : '更换群头像')),
              if (avatar != null) TextButton(onPressed: frozen || choosing ? null : () {
                ++turn; setState(() { avatar = null; uploading = false; progress = null; });
              }, child: const Text('移除头像')),
            ]),
            if ((choosing || uploading) && overlays == 0) Padding(padding: const EdgeInsets.symmetric(vertical: 8),
              child: LinearProgressIndicator(value: progress)),
            if (avatarFailure != null) Text(avatarFailure!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
            const SizedBox(height: 16),
            TextField(controller: name, enabled: !frozen, maxLength: GroupRepository.maximumCreationNameLength,
              decoration: const InputDecoration(labelText: '群聊名称', hintText: '填写群聊名称')),
            const SizedBox(height: 12), Text('群成员（包含你共 ${members.length + 1} 人）',
              style: Theme.of(context).textTheme.titleMedium),
            for (final member in members) ListTile(contentPadding: EdgeInsets.zero,
              leading: ContactAvatar(uri: member.avatar), title: Text(member.displayName),
              subtitle: SelectableText('ID：${member.id}')),
            if (!frozen) const Padding(padding: EdgeInsets.symmetric(vertical: 8),
              child: Text('返回上一步可重新选择成员。群头像可不设置。')),
            if (failure != null) Padding(padding: const EdgeInsets.symmetric(vertical: 12),
              child: Text(failure!, style: TextStyle(color: Theme.of(context).colorScheme.error))),
            if (dispatched && created == null && !creating && failure == null)
              const Padding(padding: EdgeInsets.symmetric(vertical: 12),
                child: Text('群创建结果未确认，请前往群聊列表核对。本页不会再次提交。')),
            if (creating) const LinearProgressIndicator(),
            const SizedBox(height: 12),
            if (!dispatched) FilledButton(onPressed: creating || choosing || uploading ? null : submit,
              child: const Text('创建群聊')),
            if (created case final receipt?) Wrap(spacing: 8, runSpacing: 8, children: [
              FilledButton(onPressed: creating ? null : retryOpen, child: const Text('重试打开会话')),
              OutlinedButton(onPressed: creating ? null : () {
                if (active(view)) context.go(groupPath(receipt.id));
              }, child: const Text('查看群资料')),
            ]),
            if (dispatched) TextButton(onPressed: creating ? null : () {
              if (active(view)) context.go('/contacts/groups');
            }, child: const Text('返回群聊列表')),
          ]))))));
  }
}

class _CreationOverlay extends StatefulWidget {
  const _CreationOverlay({required this.child, required this.onCovered});
  final Widget child;
  final VoidCallback onCovered;
  @override
  State<_CreationOverlay> createState() => _CreationOverlayState();
}
class _CreationOverlayState extends State<_CreationOverlay> {
  bool covered = false;
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    if (!covered && route?.isActive == true && route?.isCurrent == false) {
      covered = true; widget.onCovered();
    }
  }
  @override
  Widget build(BuildContext context) => widget.child;
}
