import 'dart:async';

import 'package:flutter/material.dart';

import '../../../core/error/app_failure.dart';
import '../data/meeting_repository.dart';
import '../domain/meeting.dart';
import 'meeting_format.dart';

class MeetingEditorPage extends StatefulWidget {
  const MeetingEditorPage({super.key, required this.repository, this.meeting});
  final MeetingRepository repository;
  final Meeting? meeting;
  @override
  State<MeetingEditorPage> createState() => _MeetingEditorPageState();
}

class _MeetingEditorPageState extends State<MeetingEditorPage> {
  late final TextEditingController title;
  late DateTime start;
  late Duration duration;
  final invitees = TextEditingController();
  bool busy = false, unconfirmed = false;
  String? notice;
  late final StreamSubscription<int> _invalidations;
  @override
  void initState() {
    super.initState();
    title = TextEditingController(text: widget.meeting?.title ?? '');
    start =
        widget.meeting?.start.toLocal() ??
        widget.repository.now().add(const Duration(minutes: 10));
    duration = widget.meeting == null
        ? const Duration(hours: 1)
        : widget.meeting!.end.difference(widget.meeting!.start);
    _invalidations = widget.repository.session.invalidations.listen((_) {
      title.clear();
      invitees.clear();
      if (mounted) {
        setState(() {
          busy = false;
          notice = '登录已变化，请关闭此页面后重新进入。';
        });
      }
    });
  }

  @override
  void dispose() {
    unawaited(_invalidations.cancel());
    title.dispose();
    invitees.dispose();
    super.dispose();
  }

  Future<void> _time() async {
    final now = widget.repository.now().toLocal();
    final date = await showDatePicker(
      context: context,
      initialDate: start.isBefore(now) ? now : start,
      firstDate: DateTime(now.year, now.month, now.day),
      lastDate: now.add(const Duration(days: 365)),
    );
    if (date == null || !mounted) {
      return;
    }
    final time = await showTimePicker(
      context: context,
      initialTime: TimeOfDay.fromDateTime(start),
    );
    if (time != null && mounted) {
      setState(
        () => start = DateTime(
          date.year,
          date.month,
          date.day,
          time.hour,
          time.minute,
        ),
      );
    }
  }

  Future<void> _save() async {
    if (busy || unconfirmed || !widget.repository.current) {
      return;
    }
    setState(() {
      busy = true;
      notice = null;
    });
    try {
      final ids = invitees.text.trim().isEmpty
          ? <String>[]
          : invitees.text.trim().split(RegExp(r'[,，\s]+'));
      final draft = MeetingDraft(
        title: title.text,
        start: start,
        duration: duration,
        inviteeIds: ids,
      );
      draft.validate(
        widget.repository.now(),
        originalStart: widget.meeting?.start,
      );
      final result = widget.meeting == null
          ? await widget.repository.create(draft)
          : await widget.repository.update(widget.meeting!.id, {
              'meetingName': title.text.trim(),
              'startTime': start.millisecondsSinceEpoch ~/ 1000,
              'endTime': start.add(duration).millisecondsSinceEpoch ~/ 1000,
            });
      if (!mounted || !widget.repository.current) {
        return;
      }
      if (result.confirmed) {
        Navigator.of(context).pop();
      } else {
        setState(() {
          unconfirmed = true;
          notice =
              '${result.message}${result.roomId == null ? '' : '\n会议号：${result.roomId}'}';
        });
      }
    } catch (error) {
      if (mounted && widget.repository.current) {
        setState(
          () => notice = error is AppFailure
              ? error.message
              : '暂时无法提交，请保留草稿并返回核对。',
        );
      }
    } finally {
      if (mounted) {
        setState(() => busy = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) => PopScope(
    canPop: !busy,
    child: Scaffold(
      appBar: AppBar(title: Text(widget.meeting == null ? '预约会议' : '编辑会议')),
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 680),
            child: ListView(
              padding: const EdgeInsets.all(20),
              children: [
                TextField(
                  controller: title,
                  enabled: !busy && !unconfirmed && widget.repository.current,
                  maxLength: 256,
                  decoration: const InputDecoration(labelText: '会议主题'),
                ),
                ListTile(
                  contentPadding: EdgeInsets.zero,
                  title: const Text('开始时间（本地时区）'),
                  subtitle: Text(meetingDate(start)),
                  trailing: const Icon(Icons.calendar_today_outlined),
                  onTap: busy || unconfirmed || !widget.repository.current
                      ? null
                      : _time,
                ),
                DropdownButtonFormField<int>(
                  initialValue: duration.inSeconds,
                  decoration: const InputDecoration(labelText: '持续时间'),
                  items:
                      {
                            ...[1800, 3600, 5400, 7200],
                            duration.inSeconds,
                          }
                          .map(
                            (value) => DropdownMenuItem(
                              value: value,
                              child: Text(
                                value % 60 == 0
                                    ? '${value ~/ 60} 分钟'
                                    : '${value ~/ 60} 分 ${value % 60} 秒',
                              ),
                            ),
                          )
                          .toList(),
                  onChanged: busy || unconfirmed || !widget.repository.current
                      ? null
                      : (value) {
                          if (value != null) {
                            setState(() => duration = Duration(seconds: value));
                          }
                        },
                ),
                if (widget.meeting == null)
                  TextField(
                    controller: invitees,
                    enabled: !busy && !unconfirmed && widget.repository.current,
                    maxLines: 3,
                    decoration: const InputDecoration(
                      labelText: '邀请的用户 ID（可选）',
                      helperText: '最多 100 人，用逗号分隔；服务端会验证邀请权限。',
                    ),
                  ),
                if (notice != null)
                  Padding(
                    padding: const EdgeInsets.symmetric(vertical: 16),
                    child: SelectableText(notice!),
                  ),
                const SizedBox(height: 24),
                FilledButton(
                  onPressed: busy || unconfirmed || !widget.repository.current
                      ? null
                      : _save,
                  child: busy
                      ? const SizedBox.square(
                          dimension: 20,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Text('保存会议'),
                ),
                if (unconfirmed)
                  TextButton(
                    onPressed: () => Navigator.of(context).pop(),
                    child: const Text('返回会议列表核对'),
                  ),
              ],
            ),
          ),
        ),
      ),
    ),
  );
}
