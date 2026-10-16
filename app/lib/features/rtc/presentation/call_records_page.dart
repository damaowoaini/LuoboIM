import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/error/app_failure.dart';
import '../domain/call_history_recorder.dart';
import '../domain/call_record.dart';
import 'call_records_providers.dart';
import 'rtc_providers.dart';

class CallRecordsPage extends ConsumerStatefulWidget {
  const CallRecordsPage({super.key});
  @override
  ConsumerState<CallRecordsPage> createState() => _CallRecordsPageState();
}

class _CallRecordsPageState extends ConsumerState<CallRecordsPage> {
  late final CallHistoryRecorder history;
  CallHistoryAccess? access;
  final List<CallRecord> _rows = [];
  CallRecordsFilter filter = CallRecordsFilter.all;
  CallRecordsCursor? _cursor;
  bool _loading = false, _ready = false, _ownedOverlay = false, _dirty = false;
  int _view = 0, _request = 0, _recordVersion = 0;
  Object? _operation;
  AppFailure? _failure;

  @override
  void initState() {
    super.initState();
    history = ref.read(callHistoryRecorderProvider);
    history.addListener(_changed);
    try { access = history.capture(); } on AppFailure catch (error) { _failure = error; }
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) unawaited(_load(reset: true));
    });
  }

  void _changed() {
    if (mounted) setState(() {
      ++_recordVersion;
      if (_ready) _dirty = true;
    });
  }
  bool get _ownerCurrent => mounted && (access?.current ?? false);
  bool _current(int view) => _ownerCurrent && view == _view &&
    ModalRoute.of(context)?.isCurrent == true;

  void _retire() {
    ++_view; ++_request;
    _loading = false;
    WidgetsBinding.instance.addPostFrameCallback((_) { if (mounted) setState(() {}); });
    WidgetsBinding.instance.ensureVisualUpdate();
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (!_ownedOverlay && ModalRoute.of(context)?.isCurrent == false) _retire();
  }

  Future<void> _load({required bool reset}) async {
    final owner = access;
    final view = _view;
    if (owner == null || !_current(view) || _loading || (!reset && _cursor == null)) return;
    final request = ++_request;
    final recordVersion = _recordVersion;
    final selected = filter;
    setState(() { _loading = true; _failure = null; });
    bool current() => _current(view) && request == _request && filter == selected;
    try {
      final result = await owner.repository.list(filter: selected, current: current,
        cursor: reset ? null : _cursor);
      if (!current()) return;
      setState(() {
        if (reset) _rows.clear();
        _rows.addAll(result.items); _cursor = result.next; _ready = true;
        _dirty = recordVersion != _recordVersion;
      });
    } on AppFailure catch (error) {
      if (current()) setState(() { _failure = error; });
    } finally {
      if (mounted && request == _request) setState(() { _loading = false; });
    }
  }

  Future<String?> _choice(CallRecord row, int view, {required bool deleting}) async {
    _ownedOverlay = true;
    try {
      return await showModalBottomSheet<String>(context: context, isScrollControlled: true,
        builder: (context) {
          void finish([String? result]) {
            if (ModalRoute.of(context)?.isCurrent != true ||
                (result != null && (!_ownerCurrent || view != _view))) return;
            Navigator.pop(context, result);
          }
          return _CallHistoryOverlay(
          history: history, current: () => _ownerCurrent && view == _view,
          covered: _retire,
          child: SafeArea(child: SingleChildScrollView(child: Padding(
            padding: const EdgeInsets.all(20), child: Column(mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch, children: [
                Text(deleting ? '删除通话记录？' : '回拨', style: Theme.of(context).textTheme.titleLarge),
                const SizedBox(height: 12), Text(row.name), SelectableText('ID：${row.peerId}'),
                if (deleting) ...[
                  const SizedBox(height: 12), const Text('仅删除此账号的这条通话记录，不会结束通话或删除聊天消息。'),
                  const SizedBox(height: 16), FilledButton(onPressed: () => finish('delete'), child: const Text('删除记录')),
                ] else ...[
                  const SizedBox(height: 16), FilledButton.icon(onPressed: () => finish('audio'), icon: const Icon(Icons.call), label: const Text('语音通话')),
                  const SizedBox(height: 8), OutlinedButton.icon(onPressed: () => finish('video'), icon: const Icon(Icons.videocam), label: const Text('视频通话')),
                ],
                TextButton(onPressed: finish, child: const Text('取消')),
              ])))));
        });
    } finally { _ownedOverlay = false; }
  }

  Future<void> _act(CallRecord row, {required int view, bool deleting = false}) async {
    final owner = access;
    if (owner == null || !_current(view) || _operation != null) return;
    final operation = Object();
    setState(() { _operation = operation; });
    try {
      final choice = await _choice(row, view, deleting: deleting);
      if (choice == null || !_current(view)) return;
      final actual = await owner.repository.exact(row.id, current: () => _current(view));
      if (!_current(view)) return;
      if (actual == null || actual.claim != row.claim || actual.sequence != row.sequence ||
          actual.peerId != row.peerId) throw const AppFailure(FailureKind.dataChanged);
      if (deleting && choice == 'delete') {
        await owner.repository.delete(actual, current: () => _current(view));
        if (_current(view)) {
          ++_request; _loading = false;
          await _load(reset: true);
        }
      } else if (!deleting && (choice == 'audio' || choice == 'video')) {
        final rtc = ref.read(rtcCoordinatorProvider);
        if (rtc.busy) {
          _notice('正在通话，请结束后再回拨'); return;
        }
        if (!rtc.canDial || !rtc.session.interactiveAllowed) {
          throw const AppFailure(FailureKind.sessionExpired);
        }
        // Dial accepts the original target synchronously before its first
        // await. Navigation/permission failure never causes automatic redial.
        final dialing = rtc.dial(inviteeIds: [actual.peerId], video: choice == 'video', title: actual.name);
        if (mounted && _current(view) && rtc.busy &&
            rtc.call?.inviteeIds.singleOrNull == actual.peerId) context.push('/rtc');
        await dialing;
      }
    } on AppFailure catch (error) {
      if (_current(view)) _notice(error.message);
    } catch (_) {
      if (_current(view)) _notice('操作未完成，请稍后重试');
    } finally {
      // Internal busy cleanup is not a late navigation/toast or a new intent.
      if (mounted && identical(_operation, operation)) setState(() { _operation = null; });
    }
  }

  void _notice(String text) => ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(text)));

  Future<void> _retryPersistence(int view) async {
    if (!_current(view) || _operation != null) return;
    final operation = Object();
    setState(() { _operation = operation; });
    try {
      await history.retryPersistence();
      if (_current(view)) await _load(reset: true);
    } on AppFailure catch (error) {
      if (_current(view)) _notice(error.message);
    } finally {
      if (mounted && identical(_operation, operation)) setState(() { _operation = null; });
    }
  }

  @override
  void dispose() { history.removeListener(_changed); super.dispose(); }

  @override
  Widget build(BuildContext context) {
    final renderedView = _view;
    return Scaffold(
    appBar: AppBar(title: const Text('通话记录'), actions: [
      IconButton(tooltip: '刷新通话记录', onPressed: !_ownerCurrent || _loading ? null : () => _load(reset: true), icon: const Icon(Icons.refresh)),
    ]),
    body: SafeArea(child: !_ownerCurrent ? const Center(child: Text('会话已变化，请返回后重新打开')) : Column(children: [
      Padding(padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        child: SegmentedButton<CallRecordsFilter>(segments: const [
          ButtonSegment(value: CallRecordsFilter.all, label: Text('全部')),
          ButtonSegment(value: CallRecordsFilter.missed, label: Text('未接')),
        ], selected: {filter}, onSelectionChanged: (value) {
          if (!_ownerCurrent || value.single == filter) return;
          ++_request; _loading = false;
          setState(() { filter = value.single; _rows.clear(); _cursor = null; _ready = false; });
          unawaited(_load(reset: true));
        })),
      if (history.failure != null) MaterialBanner(content: const Text('部分通话记录未能保存，请重试保存并刷新核对'), actions: [
        TextButton(onPressed: _operation == null ? () => _retryPersistence(renderedView) : null, child: const Text('重试保存')),
      ]),
      if (_failure != null) MaterialBanner(content: Text(_failure!.message), actions: [
        TextButton(onPressed: _loading ? null : () => _load(reset: true), child: const Text('重新读取')),
      ]),
      if (_dirty) MaterialBanner(content: const Text('通话记录已更新，刷新后查看'), actions: [
        TextButton(onPressed: _loading ? null : () => _load(reset: true), child: const Text('刷新记录')),
      ]),
      if (_loading) const LinearProgressIndicator(),
      Expanded(child: !_ready && _loading ? const Center(child: Text('正在读取通话记录')) :
        _ready && _rows.isEmpty && _failure == null ? Center(child: Text(_dirty ? '请刷新查看最新通话记录' : filter == CallRecordsFilter.missed ? '暂无未接来电' : '暂无通话记录')) :
        RefreshIndicator(onRefresh: () => _load(reset: true), child: ListView.builder(
          physics: const AlwaysScrollableScrollPhysics(),
          itemCount: _rows.length + (_cursor == null ? 0 : 1), itemBuilder: (context, index) {
            if (index == _rows.length) return TextButton(onPressed: _loading ? null : () => _load(reset: false), child: const Text('加载更多'));
            final row = _rows[index];
            final time = DateTime.fromMillisecondsSinceEpoch(row.admittedAt).toLocal();
            final date = '${time.year}-${time.month.toString().padLeft(2, '0')}-${time.day.toString().padLeft(2, '0')} ${time.hour.toString().padLeft(2, '0')}:${time.minute.toString().padLeft(2, '0')}';
            return ListTile(key: ValueKey(row.id), isThreeLine: true,
              leading: Icon(row.direction == CallDirection.incoming ? Icons.call_received : Icons.call_made),
              title: Text(row.name), subtitle: Text('ID：${row.peerId}\n${row.video ? '视频' : '语音'} · ${row.label(live: history.live(row.id))}${row.connected ? ' · ${row.durationMs ~/ 1000}秒' : ''}\n$date'),
              onTap: _operation == null ? () => _act(row, view: renderedView) : null,
              trailing: IconButton(tooltip: '删除 ${row.name} 的通话记录', onPressed: _operation == null ? () => _act(row, view: renderedView, deleting: true) : null, icon: const Icon(Icons.delete_outline)));
          }))),
    ])),
  );
  }
}

class _CallHistoryOverlay extends StatefulWidget {
  const _CallHistoryOverlay({required this.history, required this.current,
    required this.covered, required this.child});
  final CallHistoryRecorder history;
  final bool Function() current;
  final VoidCallback covered;
  final Widget child;
  @override
  State<_CallHistoryOverlay> createState() => _CallHistoryOverlayState();
}
class _CallHistoryOverlayState extends State<_CallHistoryOverlay> {
  bool retired = false;
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    if (!retired && route?.isActive == true && route?.isCurrent == false) {
      retired = true; widget.covered();
    }
  }
  @override
  Widget build(BuildContext context) => AnimatedBuilder(animation: widget.history,
    builder: (context, _) => widget.current() ? widget.child : SafeArea(child: Padding(
      padding: const EdgeInsets.all(20), child: Column(mainAxisSize: MainAxisSize.min, children: [
        const Text('会话或页面已变化，请关闭后重新操作'),
        TextButton(onPressed: () { if (ModalRoute.of(context)?.isCurrent == true) Navigator.pop(context); }, child: const Text('关闭')),
      ]))));
}
