import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../booking_providers.dart';
import '../data/booking_contract.dart';
import '../domain/booking_view.dart';
import 'booking_controller.dart';
import 'booking_recovery_panel.dart';

class BookingEditorPage extends ConsumerStatefulWidget {
  const BookingEditorPage({super.key, this.original});
  final OwnerBookingView? original;
  @override
  ConsumerState<BookingEditorPage> createState() => _BookingEditorPageState();
}
class _BookingEditorPageState extends ConsumerState<BookingEditorPage> {
  final title = TextEditingController();
  DateTime? start;
  int duration = 3600;
  BookingController? owner;
  BookingIntent? frozen;
  BookingReceipt? terminal;
  bool busy = false;
  String? notice;
  int _epoch = 0;
  late final OwnerBookingView? original;
  @override
  void initState() { super.initState(); original = widget.original; }
  void bind(BookingController controller) {
    if (owner != null) return;
    owner = controller; controller.addListener(changed);
    title.text = original?.payload?.title ?? '';
    start = original?.payload?.start.toLocal() ?? controller.now().add(const Duration(minutes: 10));
    duration = original?.payload?.durationSeconds ?? 3600;
  }
  void changed() {
    if (!mounted) return;
    if (owner?.usable != true) { ++_epoch; title.clear(); start = null; frozen = null; terminal = null; }
    setState(() {});
  }
  bool get active => mounted && identical(widget.original, original) && owner?.usable == true &&
    identical(ref.read(bookingControllerProvider).asData?.value, owner) && ModalRoute.of(context)?.isCurrent == true;
  bool matches(BookingController controller, BookingIntent intent, int epoch) =>
    active && identical(owner, controller) && identical(frozen, intent) && epoch == _epoch;
  bool verifiedTerminal(BookingController controller, BookingIntent intent, int epoch, BookingReceipt receipt) {
    if (!matches(controller, intent, epoch) || receipt.id != intent.id || !receipt.found) return false;
    try { receipt.verifyIntent(intent.operation, intent.target); } catch (_) { return false; }
    return !receipt.applied || (receipt.record?.host == controller.repository.scope.owner && isBookingUUID(receipt.record?.id));
  }
  void recovered(BookingController controller, BookingIntent intent, int epoch, BookingReceipt receipt) {
    if (!verifiedTerminal(controller, intent, epoch, receipt)) return;
    setState(() {
      terminal = receipt;
      notice = receipt.definitiveNotApplied ? '原操作已确认未应用。请明确选择下一步，不会自动重发。'
        : '原操作已保存。请读取当前预约详情，原草稿不是最新详情。';
    });
  }
  bool matchesTerminal(BookingController controller, BookingIntent intent, int epoch, BookingReceipt receipt) =>
    matches(controller, intent, epoch) && identical(terminal, receipt) && receipt.id == intent.id &&
    receipt.found && !busy && !controller.isBusy(intent.id);
  void refill(BookingController controller, BookingIntent intent, int epoch, BookingReceipt receipt) {
    if (!matchesTerminal(controller, intent, epoch, receipt) || !receipt.definitiveNotApplied ||
        original != null || !controller.canCreate) {
      return;
    }
    setState(() {
      ++_epoch; frozen = null; terminal = null; notice = null; title.clear();
      start = controller.now().add(const Duration(minutes: 10)); duration = 3600;
    });
  }
  void leaveTerminal(BookingController controller, BookingIntent intent, int epoch,
      BookingReceipt receipt, {bool detail = false}) {
    if (!matchesTerminal(controller, intent, epoch, receipt)) return;
    if (detail) {
      if (!receipt.applied || receipt.record?.host != controller.repository.scope.owner) return;
      final id = receipt.record?.id;
      if (!isBookingUUID(id)) return;
      context.go('/meetings/bookings/$id');
    } else { context.go('/meetings/bookings'); }
  }
  @override
  void dispose() { ++_epoch; owner?.removeListener(changed); title.dispose(); super.dispose(); }
  Future<void> chooseTime() async {
    if (!active || busy || frozen != null) return;
    final epoch = _epoch, captured = owner!, now = owner!.now();
    final date = await showDatePicker(context: context, initialDate: start!.isBefore(now) ? now : start!,
      firstDate: DateTime(now.year, now.month, now.day), lastDate: now.add(const Duration(days: 365)));
    if (!mounted || date == null || !active || epoch != _epoch || owner != captured) return;
    final time = await showTimePicker(context: context, initialTime: TimeOfDay.fromDateTime(start!));
    if (time != null && active && epoch == _epoch && owner == captured) {
      setState(() => start = DateTime(date.year, date.month, date.day, time.hour, time.minute));
    }
  }
  Future<void> save() async {
    final controller = owner;
    if (!active || controller == null || busy || frozen != null || !controller.canWrite) return;
    if (original == null && !controller.canCreate) return;
    if (original != null && !controller.canEdit(original!)) return;
    final epoch = _epoch;
    try {
      final now = controller.now(), chosen = start!;
      if (original == null && chosen.isBefore(now.subtract(const Duration(minutes: 1)))) {
        throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
      }
      final seed = original?.payload ?? BookingPayload(title: '',
        startSeconds: chosen.millisecondsSinceEpoch ~/ 1000, durationSeconds: duration);
      final payload = seed.edited(title.text, chosen, duration, now);
      final id = bookingUUID();
      frozen = BookingIntent(id: id, operation: original == null ? BookingOperation.create : BookingOperation.update,
        frozenBody: {'clientRequestID': id, ...payload.toWire(), if (original != null) ...{
          'bookingID': original!.id.value, 'expectedVersion': original!.version,
        }});
    } catch (_) { if (active) setState(() => notice = '请检查主题、开始时间与时长。'); return; }
    setState(() { busy = true; notice = null; });
    final submitted = frozen!;
    try {
      final receipt = await controller.submit(submitted);
      if (!mounted || !matches(controller, submitted, epoch)) return;
      if (receipt != null && verifiedTerminal(controller, submitted, epoch, receipt)) {
        if (receipt.applied && submitted.operation == BookingOperation.create) {
          // A direct GoRoute need not have a page to pop. Never render the
          // receipt payload as a current detail or mutate a cached list.
          context.go('/meetings/bookings/${receipt.record!.id}');
        } else if (receipt.applied && Navigator.of(context).canPop()) {
          // Existing Detail -> MaterialPageRoute Editor awaits this pop, then
          // performs its own authoritative detail read.
          Navigator.of(context).pop();
        } else { recovered(controller, submitted, epoch, receipt); }
      } else { setState(() => notice = '结果尚未确认。原草稿已冻结，请查询原操作回执，勿重新创建。'); }
    } finally { if (mounted && epoch == _epoch) setState(() => busy = false); }
  }
  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: Text(original == null ? '预约会议（仅排期）' : '编辑预约')),
    body: ref.watch(bookingControllerProvider).when(
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (_, _) => const Center(child: Text('本地预约资料暂时无法打开，原记录会保留。')),
      data: (controller) {
        bind(controller);
        if (owner != controller || !controller.usable || !identical(widget.original, original)) {
          return const Center(child: Text('登录、目标或访问状态已变化，请重新打开编辑页。'));
        }
        final currentIntent = frozen, currentTerminal = terminal, renderEpoch = _epoch;
        final enabled = active && controller.canWrite && !busy && frozen == null;
        return Center(child: ConstrainedBox(constraints: const BoxConstraints(maxWidth: 680),
          child: ListView(padding: const EdgeInsets.all(20), children: [
            const Text('仅保存本人排期，邀请通知和音视频入会尚未开放。'),
            if (!controller.canWrite) const Text('当前服务或资料状态暂不支持提交预约。'),
            if (currentTerminal == null) ...[
            TextField(controller: title, enabled: enabled, maxLength: 256,
              decoration: const InputDecoration(labelText: '会议主题')),
            ListTile(contentPadding: EdgeInsets.zero, title: const Text('开始时间（本地时区）'),
              subtitle: Text(start == null ? '登录状态已变化' : bookingLocalTime(start!)), onTap: enabled ? chooseTime : null),
            DropdownButtonFormField<int>(key: ValueKey(_epoch), initialValue: duration,
              decoration: const InputDecoration(labelText: '持续时间'),
              items: {...[1800, 3600, 5400, 7200], duration}.map((seconds) => DropdownMenuItem(value: seconds,
                child: Text('$seconds 秒'))).toList(), onChanged: enabled ? (value) => setState(() => duration = value ?? duration) : null),
            FilledButton(onPressed: enabled ? save : null, child: Text(busy ? '正在提交…' : '保存预约')),
            ],
            if (notice != null) SelectableText(notice!),
            if (currentTerminal == null && controller.notice != null) Text(controller.notice!),
            if (currentIntent != null && currentTerminal != null) ...[
              if (currentTerminal.definitiveNotApplied && original == null)
                FilledButton(onPressed: active && controller.canCreate ?
                  () => refill(controller, currentIntent, renderEpoch, currentTerminal) : null,
                  child: const Text('重新填写')),
              if (currentTerminal.applied)
                FilledButton(onPressed: active ? () => leaveTerminal(controller, currentIntent, renderEpoch,
                  currentTerminal, detail: true) : null, child: const Text('查看详情')),
              TextButton(onPressed: active ? () => leaveTerminal(controller, currentIntent, renderEpoch,
                currentTerminal) : null, child: const Text('返回预约列表')),
            ],
            BookingRecoveryPanel(controller: controller, originalIntentID: currentTerminal == null ? currentIntent?.id : null,
              onTerminal: currentIntent == null ? null :
              (receipt) => recovered(controller, currentIntent, renderEpoch, receipt)),
          ])));
      },
    ),
  );
}
