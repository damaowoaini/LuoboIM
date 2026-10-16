import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:qr_flutter/qr_flutter.dart';

import '../data/qr_platform.dart';
import '../data/qr_repository.dart';
import '../domain/qr_invite.dart';

/// Keeps the discovery input observable without relying on private plugin
/// fields. This component still renders the real qr_flutter QR widget.
class QrCodeImage extends StatelessWidget {
  const QrCodeImage({super.key, required this.data});
  final String data;
  @override
  Widget build(BuildContext context) => QrImageView(
    data: data, version: QrVersions.auto, size: 240,
  );
}

class QrCodePage extends ConsumerStatefulWidget {
  const QrCodePage({super.key, this.groupId});
  final String? groupId;
  @override
  ConsumerState<QrCodePage> createState() => _QrCodePageState();
}
class _QrCodePageState extends ConsumerState<QrCodePage> with WidgetsBindingObserver {
  QrRepository? owner;
  int epoch = 0, operation = 0;
  bool busy = false, ownedOS = false;
  String? failure;
  @override
  void initState() {
    super.initState(); WidgetsBinding.instance.addObserver(this);
    owner = ref.read(qrRepositoryProvider);
  }
  @override
  void didUpdateWidget(covariant QrCodePage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.groupId != widget.groupId) { ++epoch; ++operation; busy = false; failure = null; }
  }
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (ModalRoute.of(context)?.isCurrent == false) ++epoch;
  }
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state != AppLifecycleState.resumed && !ownedOS) ++epoch;
  }
  @override
  void dispose() { ++epoch; WidgetsBinding.instance.removeObserver(this); super.dispose(); }
  Future<void> exportImage(QrSubject subject, bool save) async {
    final repository = owner;
    if (busy || repository == null || !repository.current || ModalRoute.of(context)?.isCurrent != true) return;
    final id = widget.groupId, token = epoch;
    final turn = ++operation;
    final platform = ref.read(qrPlatformProvider);
    bool current() => mounted && token == epoch && widget.groupId == id && repository.current &&
      identical(repository, ref.read(qrRepositoryProvider)) &&
      !ref.read(qrSubjectProvider(id)).isLoading &&
      identical(subject, ref.read(qrSubjectProvider(id)).asData?.value) &&
      identical(platform, ref.read(qrPlatformProvider)) && ModalRoute.of(context)?.isCurrent == true;
    if (!current()) return;
    setState(() { busy = true; failure = null; });
    try {
      final data = QrInvite.encode(kind: subject.kind, subjectId: subject.id, credentials: repository.access.credentials);
      final bytes = await platform.renderPng(data);
      if (!mounted || !current()) return;
      final box = context.findRenderObject() as RenderBox?;
      final origin = box == null ? const Rect.fromLTWH(0, 0, 1, 1) : box.localToGlobal(Offset.zero) & box.size;
      ownedOS = true;
      final result = save ? await platform.savePng(bytes, current) : await platform.sharePng(bytes, origin);
      ownedOS = false;
      if (!mounted || !current()) return;
      if (result == QrExportResult.completed) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(save ? '二维码已保存' : '二维码已分享')));
      } else if (result == QrExportResult.unavailable) {
        setState(() => failure = save ? '当前设备不支持保存图片' : '当前设备无法分享图片，请重试');
      }
    } catch (_) {
      if (mounted && current()) setState(() => failure = '二维码图片处理失败，请重试');
    } finally {
      ownedOS = false;
      if (mounted && turn == operation) setState(() => busy = false);
    }
  }
  @override
  Widget build(BuildContext context) {
    final repository = ref.watch(qrRepositoryProvider);
    final subject = ref.watch(qrSubjectProvider(widget.groupId));
    final valid = owner != null && identical(owner, repository) && owner!.current;
    return Scaffold(appBar: AppBar(title: Text(widget.groupId == null ? '我的二维码' : '群二维码')),
      body: SafeArea(child: Center(child: ConstrainedBox(constraints: const BoxConstraints(maxWidth: 600),
        child: !valid ? const Text('会话已变化，请返回后重新打开') : subject.when(
          skipLoadingOnRefresh: false, skipLoadingOnReload: false,
          loading: () => const CircularProgressIndicator(),
          error: (_, _) => Padding(padding: const EdgeInsets.all(24), child: Column(mainAxisSize: MainAxisSize.min, children: [
            Text(widget.groupId == null ? '无法读取个人资料' : '无法核验群资料或当前成员身份'),
            const SizedBox(height: 12), FilledButton(onPressed: () => ref.invalidate(qrSubjectProvider(widget.groupId)), child: const Text('重试')),
          ])),
          data: (value) => ListView(shrinkWrap: true, padding: const EdgeInsets.all(24), children: [
            Text(value.name, textAlign: TextAlign.center, style: Theme.of(context).textTheme.titleLarge),
            const SizedBox(height: 8), SelectableText('ID：${value.id}', textAlign: TextAlign.center),
            const SizedBox(height: 24), Center(child: Semantics(label: value.kind == QrInviteKind.user ? '个人二维码' : '群二维码',
              image: true, child: ColoredBox(color: Colors.white, child: Padding(padding: const EdgeInsets.all(20),
                child: SizedBox(width: 240, height: 240, child: QrCodeImage(
                  data: QrInvite.encode(kind: value.kind, subjectId: value.id, credentials: owner!.access.credentials),
                )))))),
            const SizedBox(height: 16), const Text('请对方在相同登录服务中扫描，并核对资料', textAlign: TextAlign.center),
            if (failure != null) Padding(padding: const EdgeInsets.symmetric(vertical: 12),
              child: Text(failure!, textAlign: TextAlign.center, style: TextStyle(color: Theme.of(context).colorScheme.error))),
            if (busy) const LinearProgressIndicator(),
            const SizedBox(height: 16), Wrap(spacing: 12, runSpacing: 8, alignment: WrapAlignment.center, children: [
              FilledButton.icon(onPressed: busy ? null : () => exportImage(value, false), icon: const Icon(Icons.share_outlined), label: const Text('分享二维码')),
              if (ref.watch(qrPlatformProvider).supportsSave) OutlinedButton.icon(
                onPressed: busy ? null : () => exportImage(value, true), icon: const Icon(Icons.save_alt), label: const Text('保存图片')),
            ]),
          ]),
        )))));
  }
}
