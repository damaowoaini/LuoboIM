import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../data/qr_platform.dart';
import '../data/qr_repository.dart';
import '../domain/qr_invite.dart';
import 'qr_route_watch.dart';

class QrScanPage extends ConsumerStatefulWidget {
  const QrScanPage({super.key});
  @override
  ConsumerState<QrScanPage> createState() => _QrScanPageState();
}

class _QrScanPageState extends ConsumerState<QrScanPage> with WidgetsBindingObserver {
  QrRepository? repository;
  late final QrPlatform platform;
  late final QrCamera camera;
  StreamSubscription<int>? invalidation;
  int epoch = 0, operation = 0;
  bool busy = false, running = false, foreground = true, ownedOS = false;
  bool ownedDialog = false, settings = false;
  String? failure;
  ValueNotifier<bool>? confirmationActive;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    platform = ref.read(qrPlatformProvider);
    camera = platform.createCamera();
    repository = ref.read(qrRepositoryProvider);
    invalidation = repository?.access.session.invalidations.listen((_) => retire());
    WidgetsBinding.instance.addPostFrameCallback((_) { if (mounted) unawaited(start()); });
  }
  bool owns(int token) => mounted && token == epoch && repository?.current == true &&
    identical(repository, ref.read(qrRepositoryProvider)) &&
    identical(platform, ref.read(qrPlatformProvider));
  bool current(int token) => owns(token) && foreground && ModalRoute.of(context)?.isCurrent == true;
  void redraw() {
    WidgetsBinding.instance.addPostFrameCallback((_) { if (mounted) setState(() {}); });
    // A platform/session notification may arrive while no frame is scheduled.
    WidgetsBinding.instance.ensureVisualUpdate();
  }
  void retire() {
    ++epoch; running = false;
    failure = '扫描已暂停，请重新开始';
    // Hide only this page's confirmation. Never pop a foreign route above it.
    final confirmation = confirmationActive;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted && identical(confirmationActive, confirmation)) confirmation?.value = false;
    });
    unawaited(camera.stop().catchError((Object _) {}));
    redraw();
  }
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (!ownedDialog && ModalRoute.of(context)?.isCurrent == false) retire();
  }
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    foreground = state == AppLifecycleState.resumed;
    if (!foreground) {
      unawaited(camera.stop().catchError((Object _) {}));
      running = false;
      // Permission/gallery/settings are owned OS awaits. They still retain
      // owner+route fencing, but normal OS lifecycle must not discard a pick.
      if (!ownedOS) retire();
    }
    if (mounted) setState(() {});
  }
  @override
  void dispose() {
    ++epoch; WidgetsBinding.instance.removeObserver(this);
    unawaited(invalidation?.cancel());
    unawaited(camera.dispose().catchError((Object _) {}));
    super.dispose();
  }
  Future<void> start() async {
    if (busy || repository?.current != true || ModalRoute.of(context)?.isCurrent != true) return;
    final token = ++epoch;
    final turn = ++operation;
    List<String>? firstCapture;
    var startingCapture = true;
    setState(() { busy = true; running = false; failure = null; settings = false; });
    try {
      await camera.stop();
      if (!current(token)) return;
      ownedOS = true;
      final permission = await platform.cameraPermission();
      ownedOS = false;
      if (!current(token)) return;
      if (permission != QrCameraPermission.granted) {
        setState(() {
          settings = permission == QrCameraPermission.permanentlyDenied;
          failure = settings ? '相机权限已关闭，请在系统设置中允许' : '需要相机权限，也可以从图片识别';
        });
        return;
      }
      await camera.start((codes) {
        if (!current(token)) return;
        if (startingCapture) {
          firstCapture = codes;
        } else if (!busy) {
          unawaited(detected(codes, token));
        }
      });
      if (!current(token)) { await camera.stop(); return; }
      setState(() => running = true);
    } catch (_) {
      if (current(token)) setState(() => failure = '相机暂不可用，请重试或从图片识别');
    } finally {
      ownedOS = false;
      startingCapture = false;
      if (mounted && turn == operation) setState(() => busy = false);
      if (current(token) && running && firstCapture != null && !busy) {
        unawaited(detected(firstCapture!, token));
      }
    }
  }
  Future<void> gallery() async {
    if (busy || repository?.current != true || ModalRoute.of(context)?.isCurrent != true) return;
    final token = ++epoch;
    final turn = ++operation;
    setState(() { busy = true; running = false; failure = null; });
    try {
      await camera.stop();
      if (!current(token)) return;
      ownedOS = true;
      final image = await platform.pickImage();
      ownedOS = false;
      if (!current(token) || image == null) return;
      final codes = await camera.analyzeImage(image.path);
      if (!current(token)) return;
      await inspect(codes, token);
    } catch (_) {
      if (current(token)) setState(() => failure = '图片识别失败，请选择清晰的二维码重试');
    } finally {
      ownedOS = false;
      if (mounted && turn == operation) setState(() => busy = false);
    }
  }
  Future<void> detected(List<String> codes, int token) async {
    if (busy || !current(token)) return;
    final turn = ++operation;
    setState(() { busy = true; running = false; failure = null; });
    try {
      await camera.stop();
      if (current(token)) await inspect(codes, token);
    } catch (_) {
      if (current(token)) setState(() => failure = '暂时无法核验二维码，请重试');
    } finally { if (mounted && turn == operation) setState(() => busy = false); }
  }
  Future<void> inspect(List<String> raw, int token) async {
    if (!mounted || !current(token)) return;
    final values = raw.where((s) => s.isNotEmpty).toSet();
    if (values.length != 1) {
      setState(() => failure = values.isEmpty ? '没有识别到二维码' : '识别到多个二维码，请只保留一个');
      return;
    }
    final QrInvite invite;
    try { invite = QrInvite.decode(values.single, repository!.access.credentials); }
    on QrInviteException catch (error) {
      setState(() => failure = switch (error.reason) {
        QrInviteRejection.otherService => '二维码来自其他服务，请确认当前登录服务',
        QrInviteRejection.unsupported => '这不是萝卜对话的个人或群二维码',
        QrInviteRejection.invalid => '二维码内容无效',
      });
      return;
    }
    if (invite.legacy && !await confirmLegacy(invite, token)) return;
    if (!mounted || !current(token)) return;
    // No code payload becomes profile data or permission. Resolve exact IDs
    // through existing SDK/business-policy readers before asking the user.
    final target = await repository!.resolve(invite);
    if (!mounted || !current(token)) return;
    final confirm = await confirmation(token, (dialogContext, dialogCurrent) => AlertDialog(
          title: Text(target.kind == QrInviteKind.user ? '确认个人资料' : '确认群资料'),
          content: SingleChildScrollView(child: Column(mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start, children: [
              Text(target.name), const SizedBox(height: 8), SelectableText('ID：${target.id}'),
              if (invite.legacy) const Padding(padding: EdgeInsets.only(top: 12),
                child: Text('这是旧版二维码，请确认要在当前登录服务中查看此对象。')),
              const SizedBox(height: 12), const Text('查看资料不会自动添加好友或加入群聊。'),
            ])),
          actions: [
            TextButton(onPressed: () { if (ModalRoute.of(dialogContext)?.isCurrent == true) Navigator.pop(dialogContext, false); }, child: const Text('取消')),
            FilledButton(onPressed: () { if (dialogCurrent()) Navigator.pop(dialogContext, true); }, child: const Text('查看资料')),
          ],
        ));
    if (confirm != true || !mounted || !current(token)) return;
    final id = Uri.encodeComponent(target.id);
    // Only the exact ID is forwarded. The existing pages hydrate again and
    // retain their own friend/member permission checks and explicit actions.
    await context.push(target.kind == QrInviteKind.user
      ? '/contacts/user/$id' : '/contacts/groups/$id?joinSource=4');
  }
  Future<bool> confirmLegacy(QrInvite invite, int token) async {
    if (!current(token)) return false;
    final result = await confirmation(token, (dialogContext, allowed) => AlertDialog(
      title: const Text('确认登录服务'),
      content: SingleChildScrollView(child: Column(mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start, children: [
          const Text('这是旧版二维码，未包含登录服务信息。'),
          const SizedBox(height: 8),
          Text('当前服务：${repository!.access.credentials.tenantName}'),
          SelectableText('ID：${invite.subjectId}'),
          const SizedBox(height: 8), const Text('请确认要在当前服务中查找此对象。'),
        ])),
      actions: [
        TextButton(onPressed: () { if (ModalRoute.of(dialogContext)?.isCurrent == true) Navigator.pop(dialogContext, false); }, child: const Text('取消')),
        FilledButton(onPressed: () { if (allowed()) Navigator.pop(dialogContext, true); }, child: const Text('在当前服务查找')),
      ],
    ));
    return result == true && owns(token);
  }
  Future<bool?> confirmation(int token,
      Widget Function(BuildContext, bool Function()) content) async {
    if (!current(token)) return false;
    final visible = ValueNotifier(true);
    confirmationActive = visible;
    setState(() => ownedDialog = true);
    try {
      return await showDialog<bool>(context: context, builder: (dialogContext) => Consumer(
        builder: (dialogContext, dialogRef, _) {
          final actualRepository = dialogRef.watch(qrRepositoryProvider);
          final actualPlatform = dialogRef.watch(qrPlatformProvider);
          return ValueListenableBuilder<bool>(valueListenable: visible,
            builder: (dialogContext, shown, _) {
              bool allowed() => shown && owns(token) &&
                identical(repository, actualRepository) && identical(platform, actualPlatform) &&
                ModalRoute.of(dialogContext)?.isCurrent == true;
              final valid = shown && owns(token) && identical(repository, actualRepository) &&
                identical(platform, actualPlatform);
              return QrRouteWatch(onCovered: retire, child: valid ? content(dialogContext, allowed) : AlertDialog(
                title: const Text('本次扫描已失效'),
                content: const Text('请关闭后重新扫描并核对资料。'),
                actions: [TextButton(onPressed: () {
                  if (ModalRoute.of(dialogContext)?.isCurrent == true) Navigator.pop(dialogContext, false);
                }, child: const Text('取消'))],
              ));
            });
        }));
    } finally {
      if (identical(confirmationActive, visible)) confirmationActive = null;
      if (mounted) { setState(() => ownedDialog = false); }
      else { ownedDialog = false; }
      visible.dispose();
    }
  }

  Future<void> torch() async {
    final token = epoch;
    if (!running || busy || !current(token)) return;
    try { await camera.toggleTorch(); }
    catch (_) { if (current(token)) setState(() => failure = '此设备暂时无法使用手电筒'); }
  }
  Future<void> openSettings() async {
    final token = epoch;
    if (busy || !current(token)) return;
    final turn = ++operation;
    setState(() => busy = true);
    ownedOS = true;
    try {
      final opened = await platform.openCameraSettings();
      if (current(token) && !opened) setState(() => failure = '无法打开系统设置，请手动检查相机权限');
    } catch (_) {
      if (current(token)) setState(() => failure = '无法打开系统设置，请手动检查相机权限');
    } finally {
      ownedOS = false;
      if (mounted && turn == operation) setState(() => busy = false);
    }
  }
  @override
  Widget build(BuildContext context) {
    final currentRepository = ref.watch(qrRepositoryProvider);
    final previewToken = epoch;
    final valid = repository != null && identical(currentRepository, repository) && repository!.current;
    return Scaffold(appBar: AppBar(title: const Text('扫一扫')),
      body: SafeArea(child: Center(child: ConstrainedBox(constraints: const BoxConstraints(maxWidth: 720),
        child: Column(children: [
          Expanded(child: Semantics(label: '二维码相机预览', child: valid
            ? ClipRect(child: camera.preview(() {
                if (owns(previewToken) && running && !busy) {
                  retire();
                  failure = '相机暂不可用，请重新扫描或从图片识别';
                }
              })) : const Center(child: Text('会话已变化，请返回后重新打开')))),
          Padding(padding: const EdgeInsets.all(16), child: Column(mainAxisSize: MainAxisSize.min, children: [
            const Text('扫描个人或群二维码；识别后请核对资料'),
            if (failure != null) Padding(padding: const EdgeInsets.symmetric(vertical: 8),
              child: Text(failure!, textAlign: TextAlign.center, style: TextStyle(color: Theme.of(context).colorScheme.error))),
            // Waiting for an explicit confirmation is not an active read.
            // Keep busy/ownedDialog admission guards, without animating behind it.
            if (busy && !ownedDialog) const LinearProgressIndicator(),
            Wrap(spacing: 12, runSpacing: 8, alignment: WrapAlignment.center, children: [
              OutlinedButton.icon(onPressed: valid && !busy ? gallery : null,
                icon: const Icon(Icons.photo_library_outlined), label: const Text('从图片识别')),
              OutlinedButton.icon(onPressed: valid && running && !busy ? torch : null,
                icon: const Icon(Icons.flashlight_on_outlined), label: const Text('手电筒')),
              FilledButton(onPressed: valid && !busy ? start : null, child: Text(running ? '重新扫描' : '开始扫描')),
              if (settings) TextButton(onPressed: valid && !busy ? openSettings : null, child: const Text('打开系统设置')),
            ]),
          ])),
        ])))));
  }
}
