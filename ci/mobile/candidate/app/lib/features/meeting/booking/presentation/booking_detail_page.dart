import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../booking_providers.dart';
import '../data/booking_contract.dart';
import '../domain/booking_view.dart';
import 'booking_controller.dart';
import 'booking_editor_page.dart';
import 'booking_recovery_panel.dart';

class BookingDetailPage extends ConsumerStatefulWidget {
  const BookingDetailPage({super.key, required this.bookingID});
  final String bookingID;
  @override
  ConsumerState<BookingDetailPage> createState() => _BookingDetailPageState();
}
class _BookingDetailPageState extends ConsumerState<BookingDetailPage> {
  BookingController? owner;
  OwnerBookingView? row;
  int request = 0;
  bool loading = false;
  bool get active => mounted && owner?.usable == true && ModalRoute.of(context)?.isCurrent == true;
  void backToList(BookingController controller, int epoch, String target) {
    if (!active || !identical(owner, controller) || epoch != request || target != widget.bookingID ||
        !identical(ref.read(bookingControllerProvider).asData?.value, controller)) return;
    context.go('/meetings/bookings');
  }
  void bind(BookingController controller) {
    if (owner != null) return;
    owner = controller; controller.addListener(changed);
    WidgetsBinding.instance.addPostFrameCallback((_) { if (active) load(); });
  }
  void changed() { if (mounted) { if (owner?.usable != true) { ++request; row = null; } setState(() {}); } }
  Future<void> load() async {
    if (!active || loading) return;
    final captured = owner!, epoch = ++request; setState(() => loading = true);
    try {
      final result = await captured.detail(widget.bookingID);
      if (active && epoch == request && owner == captured) setState(() => row = result);
    } finally {
      if (mounted && epoch == request && owner == captured && captured.usable) {
        setState(() => loading = false);
      }
    }
  }
  Future<void> cancel(OwnerBookingView saved) async {
    if (!active || saved.id.value != widget.bookingID || !identical(row, saved) || owner?.canEdit(saved) != true) return;
    final captured = owner!, epoch = request;
    final confirm = await showDialog<bool>(context: context, builder: (context) => AlertDialog(
      title: const Text('取消此预约？'), content: const Text('只取消你的排期，不会关闭任何音视频房间。'),
      actions: [TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('返回')),
        FilledButton(onPressed: () => Navigator.pop(context, true), child: const Text('取消预约'))],
    ));
    if (confirm != true || !active || owner != captured || epoch != request ||
        saved.id.value != widget.bookingID || !identical(row, saved) || !captured.canEdit(saved)) return;
    final intentID = bookingUUID();
    final receipt = await captured.submit(BookingIntent(id: intentID, operation: BookingOperation.cancel,
      frozenBody: {'clientRequestID': intentID, 'bookingID': saved.id.value, 'expectedVersion': saved.version}));
    if (active && owner == captured && receipt?.applied == true) await load();
  }
  @override
  void didUpdateWidget(covariant BookingDetailPage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.bookingID != widget.bookingID) {
      ++request; row = null; loading = false;
      WidgetsBinding.instance.addPostFrameCallback((_) { if (active) load(); });
    }
  }
  @override
  void dispose() { ++request; owner?.removeListener(changed); super.dispose(); }
  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('预约详情')),
    body: ref.watch(bookingControllerProvider).when(
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (_, _) => const Center(child: Text('预约资料暂时无法打开。')),
      data: (controller) {
        bind(controller);
        if (owner != controller || !controller.usable) return const Center(child: Text('登录或访问状态已变化，请重新进入。'));
        final current = row;
        final renderRequest = request, target = widget.bookingID;
        return Center(child: ConstrainedBox(constraints: const BoxConstraints(maxWidth: 760),
          child: ListView(padding: const EdgeInsets.all(20), children: [
            const Text('本人排期；邀请通知和音视频入会尚未开放。'),
            if (controller.availability == BookingAvailability.unsupported) const Text('当前服务暂不支持预约。'),
            if (controller.notice != null) Text(controller.notice!),
            if (loading) const LinearProgressIndicator(),
            if (!loading && current == null) const Text('详情尚未核对，请刷新；待确认结果会保留。'),
            if (current != null && controller.usable) ...[
              Text(current.title, style: Theme.of(context).textTheme.titleLarge),
              SelectableText('预约号：${current.id.value}'), Text('版本：${current.version} · ${current.state}'),
              if (current.payload case final payload?) Text('${bookingLocalTime(payload.start)} — ${bookingLocalTime(payload.end)}'),
              if (current.payload?.invitees.isNotEmpty == true) const Text('此记录含已有参会 ID；本版本不会发送或修改邀请。'),
              OutlinedButton(onPressed: controller.canEdit(current) ? () async {
                if (!active || owner != controller || !identical(row, current) || current.id.value != widget.bookingID) return;
                final captured = controller;
                await Navigator.of(context).push<void>(MaterialPageRoute(builder: (_) => BookingEditorPage(original: current)));
                if (active && owner == captured) await load();
              } : null, child: const Text('编辑主题与时间')),
              OutlinedButton(onPressed: controller.canEdit(current) ? () => cancel(current) : null, child: const Text('取消预约')),
            ],
            TextButton(onPressed: active && !loading ? load : null, child: const Text('刷新详情')),
            TextButton(onPressed: active ? () => backToList(controller, renderRequest, target) : null,
              child: const Text('返回预约列表')),
            BookingRecoveryPanel(controller: controller),
          ])));
      },
    ),
  );
}
