import 'package:flutter/material.dart';

import '../data/booking_contract.dart';
import 'booking_controller.dart';

class BookingRecoveryPanel extends StatelessWidget {
  const BookingRecoveryPanel({super.key, required this.controller, this.onTerminal, this.originalIntentID});
  final BookingController controller;
  final ValueChanged<BookingReceipt>? onTerminal;
  final String? originalIntentID;
  bool displays(String id) => controller.unknownIDs.contains(id) ||
    (isBookingUUID(originalIntentID) && originalIntentID == id);
  bool current(BuildContext context, {String? intentID}) {
    if (!context.mounted) return false;
    final visible = context.widget;
    // Normal route/dialog updates can replace this stateless Widget while its
    // Element and captured owner remain valid. Never borrow a new controller
    // or dispatch an intent that is no longer displayed by the current panel.
    return visible is BookingRecoveryPanel && identical(visible.controller, controller) &&
      controller.usable && ModalRoute.of(context)?.isCurrent == true &&
      (intentID == null || visible.displays(intentID));
  }
  Future<void> recover(BuildContext context, String id, {bool retry = false, bool resolve = false}) async {
    if (!current(context, intentID: id) || (retry && !controller.canWrite)) return;
    final receipt = await controller.recover(id, retry: retry, resolve: resolve);
    // Busy/pending/notice changes can replace this stateless panel. The result
    // belongs to the captured controller; the only consumer must fence its
    // own mounted page, route, provider, intent object and epoch before UI work.
    if (!controller.usable ||
        receipt == null || receipt.id != id || !receipt.found) {
      return;
    }
    onTerminal?.call(receipt);
  }
  @override
  Widget build(BuildContext context) => !controller.usable ? const SizedBox.shrink() : Column(children: [
    for (final id in {...controller.unknownIDs, if (isBookingUUID(originalIntentID)) originalIntentID!})
      Card(child: Padding(padding: const EdgeInsets.all(16), child: Column(
        crossAxisAlignment: CrossAxisAlignment.start, children: [
        const Text('待确认的预约操作'), SelectableText('操作号：$id'),
        const Text('请查询原操作回执，勿重新创建。返回页面不会撤回已发送的请求。'),
        Wrap(spacing: 8, children: [
          TextButton(onPressed: controller.usable && !controller.isBusy(id) &&
            controller.availability != BookingAvailability.unsupported
            ? () => recover(context, id) : null, child: const Text('查询原回执')),
          TextButton(onPressed: controller.usable && !controller.isBusy(id) && controller.canWrite
            ? () => recover(context, id, retry: true) : null, child: const Text('重试原操作')),
          TextButton(onPressed: controller.usable && !controller.isBusy(id) &&
            controller.availability != BookingAvailability.unsupported ? () async {
            if (!current(context, intentID: id)) return;
            final navigator = Navigator.of(context);
            final confirmed = await showDialog<bool>(context: context, builder: (context) => AlertDialog(
              title: const Text('核实并关闭原未知意图？'),
              content: const Text('如果服务端已保存，将显示实际预约；这不会取消已保存的预约。若尚未应用，服务端会冻结原操作，防止迟到创建。'),
              actions: [TextButton(onPressed: () => navigator.pop(false), child: const Text('返回')),
                FilledButton(onPressed: () => navigator.pop(true), child: const Text('核实原意图'))],
            ));
            if (!context.mounted) return;
            if (confirmed == true && current(context, intentID: id)) {
              await recover(context, id, resolve: true);
            }
          } : null, child: const Text('核实并关闭原意图')),
        ]),
      ]))),
  ]);
}
