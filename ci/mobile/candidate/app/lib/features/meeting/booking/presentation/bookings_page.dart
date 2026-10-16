import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../booking_providers.dart';
import '../domain/booking_view.dart';
import 'booking_controller.dart';
import 'booking_recovery_panel.dart';

class BookingsPage extends ConsumerStatefulWidget {
  const BookingsPage({super.key});
  @override
  ConsumerState<BookingsPage> createState() => _BookingsPageState();
}
class _BookingsPageState extends ConsumerState<BookingsPage> {
  final query = TextEditingController();
  BookingController? owner;
  bool entering = true;
  bool owns(BookingController controller) => mounted && owner == controller && controller.usable &&
    identical(ref.read(bookingControllerProvider).asData?.value, controller);
  bool active(BookingController controller) => owns(controller) && ModalRoute.of(context)?.isCurrent == true;
  void bind(BookingController controller) {
    if (owner != null) return;
    owner = controller;
    WidgetsBinding.instance.addPostFrameCallback((_) async {
      if (!active(controller)) return;
      try { await controller.refresh(); }
      finally { if (owns(controller)) setState(() => entering = false); }
    });
  }
  @override
  void dispose() { query.dispose(); super.dispose(); }
  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('我的预约（仅排期）')),
    body: ref.watch(bookingControllerProvider).when(
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (_, _) => const Center(child: Text('本地预约资料暂时无法打开。原待确认记录会保留。')),
      data: (controller) => ListenableBuilder(listenable: controller, builder: (context, _) {
        bind(controller);
        if (owner != controller) return const Center(child: Text('登录或访问状态已变化，请重新进入预约页面。'));
        final keyword = query.text.trim().toLowerCase();
        final records = controller.records.where((row) => keyword.isEmpty ||
          row.title.toLowerCase().contains(keyword) || row.id.value.contains(keyword)).toList();
        return Center(child: ConstrainedBox(constraints: const BoxConstraints(maxWidth: 900),
          child: ListView(padding: const EdgeInsets.all(20), children: [
            if (!controller.usable) const Text('登录或本机访问状态已变化，请重新进入。')
            else ...[
              const Text('这里只记录你创建的排期，邀请通知和音视频入会尚未开放。'),
              if (controller.availability == BookingAvailability.unsupported)
                const Text('当前服务暂不支持预约。已有待确认操作会保留。'),
              if (controller.notice != null) Text(controller.notice!),
              BookingRecoveryPanel(controller: controller),
              TextField(controller: query, decoration: const InputDecoration(labelText: '搜索已加载的预约主题或预约号'),
                onChanged: (_) { if (active(controller)) setState(() {}); }),
              CheckboxListTile(contentPadding: EdgeInsets.zero, title: const Text('包含已取消的预约'),
                value: controller.includeCancelled,
                onChanged: controller.loading || controller.availability == BookingAvailability.unsupported
                  ? null : (value) { if (active(controller)) controller.refresh(cancelled: value ?? false); }),
              Wrap(spacing: 12, children: [
                FilledButton(onPressed: !entering && controller.canCreate ? () {
                  if (active(controller) && !entering && controller.canCreate) context.push('/meetings/bookings/new');
                } : null,
                  child: const Text('预约会议')),
                OutlinedButton(onPressed: controller.loading || controller.availability == BookingAvailability.unsupported
                  ? null : () { if (active(controller)) controller.refresh(); }, child: const Text('刷新预约')),
              ]),
              if (entering || controller.loading) const LinearProgressIndicator(),
              if (!entering && records.isEmpty && controller.availability == BookingAvailability.ready && !controller.loading)
                const Padding(padding: EdgeInsets.all(24), child: Text('暂无匹配的已加载预约。')),
              if (!entering) for (final row in records)
                ListTile(title: Text(row.title), subtitle: Text('预约号：${row.id.value}\n${row.state == 'cancelled' ? '已取消' : '${bookingLocalTime(row.payload!.start)} — ${bookingLocalTime(row.payload!.end)}'}'),
                  isThreeLine: true, onTap: () {
                    if (active(controller)) context.push('/meetings/bookings/${row.id.value}');
                  }),
              if (controller.hasMore) TextButton(onPressed: controller.loading ? null : () {
                if (active(controller)) controller.loadMore();
              },
                child: const Text('加载更多预约')),
            ],
          ])));
      }),
    ),
  );
}
