import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../data/meeting_repository.dart';
import '../domain/meeting.dart';
import 'meeting_controller.dart';
import 'meeting_editor_page.dart';
import 'meeting_format.dart';

class MeetingDetailPage extends StatefulWidget {
  const MeetingDetailPage({
    super.key,
    required this.repository,
    required this.meetingId,
  });
  final MeetingRepository repository;
  final String meetingId;
  @override
  State<MeetingDetailPage> createState() => _MeetingDetailPageState();
}

class _MeetingDetailPageState extends State<MeetingDetailPage> {
  late final MeetingController controller;
  bool outcomeUnconfirmed = false;
  @override
  void initState() {
    super.initState();
    controller = MeetingController(widget.repository)..addListener(_changed);
    unawaited(controller.refresh());
  }

  void _changed() {
    if (mounted) {
      setState(() {});
    }
  }

  @override
  void dispose() {
    controller.removeListener(_changed);
    controller.dispose();
    super.dispose();
  }

  Future<void> _write(Future<MeetingWriteResult> Function() action) async {
    if (outcomeUnconfirmed) {
      return;
    }
    final result = await controller.perform(action);
    if (result == null || !mounted || !controller.usable) {
      return;
    }
    if (!result.confirmed) {
      setState(() => outcomeUnconfirmed = true);
    } else {
      await controller.refresh();
    }
  }

  Future<void> _close(Meeting meeting) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('结束会议？'),
        content: const Text('已连接的参会者将离开会议，尚未开始的预约也会被取消。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('返回'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('结束会议'),
          ),
        ],
      ),
    );
    if (confirmed == true && mounted && controller.usable) {
      await _write(() => widget.repository.close(meeting.id));
    }
  }

  Future<void> _invite(Meeting meeting) async {
    final value = await showDialog<String>(
      context: context,
      builder: (context) => _InviteDialog(repository: widget.repository),
    );
    if (value == null ||
        value.trim().isEmpty ||
        !mounted ||
        !controller.usable) {
      return;
    }
    await _write(
      () => widget.repository.update(meeting.id, {
        'inviteeUserIDList': value.trim().split(RegExp(r'[,，\s]+')),
      }),
    );
  }

  @override
  Widget build(BuildContext context) {
    Meeting? meeting;
    for (final item in controller.items) {
      if (item.id == widget.meetingId) {
        meeting = item;
      }
    }
    final current = meeting;
    final active = controller.usable && !controller.busy && !outcomeUnconfirmed;
    return Scaffold(
      appBar: AppBar(
        title: const Text('会议详情'),
        actions: [
          IconButton(
            tooltip: '刷新资料',
            onPressed: controller.busy || !controller.usable
                ? null
                : () async {
                    await controller.refresh();
                    // Refresh only reviews state; an uncertain mutation is never replayed.
                  },
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 760),
            child: controller.loading && current == null
                ? const CircularProgressIndicator()
                : ListView(
                    padding: const EdgeInsets.all(20),
                    children: [
                      if (controller.failure case final failure?)
                        Text(failure.message),
                      if (controller.notice != null)
                        Padding(
                          padding: const EdgeInsets.only(bottom: 12),
                          child: Text(controller.notice!),
                        ),
                      if (outcomeUnconfirmed)
                        const Text('此页面不会重复提交。请返回会议列表核对后再进行新的操作。'),
                      if (current == null && !controller.loading)
                        const Text('会议已结束、你尚未获邀或当前登录已变化。'),
                      if (current != null) ...[
                        Text(
                          current.displayTitle,
                          style: Theme.of(context).textTheme.headlineSmall,
                        ),
                        const SizedBox(height: 16),
                        SelectableText('会议号：${current.id}'),
                        Text(
                          '开始：${meetingDate(current.start)}\n结束：${meetingDate(current.end)}\n主持人：${current.hostId}',
                        ),
                        TextButton.icon(
                          onPressed: controller.usable
                              ? () => Clipboard.setData(
                                  ClipboardData(text: current.id),
                                )
                              : null,
                          icon: const Icon(Icons.copy),
                          label: const Text('复制会议号'),
                        ),
                        const FilledButton(
                          onPressed: null,
                          child: Text('音视频入会暂未开放'),
                        ),
                        const Padding(
                          padding: EdgeInsets.symmetric(vertical: 12),
                          child: Text('会议通话暂不可用，当前可预约和管理会议。本版本尚未开放加入会议。'),
                        ),
                        if (current.hostId == widget.repository.selfId) ...[
                          OutlinedButton(
                            onPressed: active
                                ? () async {
                                    await Navigator.of(context).push<void>(
                                      MaterialPageRoute(
                                        builder: (_) => MeetingEditorPage(
                                          repository: widget.repository,
                                          meeting: current,
                                        ),
                                      ),
                                    );
                                    if (mounted && controller.usable) {
                                      await controller.refresh();
                                    }
                                  }
                                : null,
                            child: const Text('编辑主题与时间'),
                          ),
                          for (final entry in _settingLabels.entries)
                            SwitchListTile(
                              contentPadding: EdgeInsets.zero,
                              title: Text(entry.value),
                              subtitle: current.settings[entry.key] == null
                                  ? const Text('当前权限资料缺失')
                                  : null,
                              value: current.settings[entry.key] ?? false,
                              onChanged:
                                  !active || current.settings[entry.key] == null
                                  ? null
                                  : (value) => _write(
                                      () => widget.repository.update(
                                        current.id,
                                        {entry.key: value},
                                      ),
                                    ),
                            ),
                        ],
                        const SizedBox(height: 16),
                        Text(
                          '邀请名单（${current.inviteeIds.length}，不代表在线人数）',
                          style: Theme.of(context).textTheme.titleMedium,
                        ),
                        if (current.canInvite(widget.repository.selfId))
                          OutlinedButton.icon(
                            onPressed: active ? () => _invite(current) : null,
                            icon: const Icon(Icons.person_add_alt),
                            label: const Text('邀请参会者'),
                          ),
                        for (final user in current.inviteeIds)
                          ListTile(
                            contentPadding: EdgeInsets.zero,
                            title: Text(user),
                            subtitle: Text(
                              '${current.microphoneDisabledIds.contains(user) ? '麦克风已限制' : '麦克风未被单独限制'} · ${current.videoDisabledIds.contains(user) ? '视频已限制' : '视频未被单独限制'}',
                            ),
                            trailing: current.hostId != widget.repository.selfId
                                ? null
                                : PopupMenuButton<String>(
                                    enabled: active,
                                    tooltip: '参会媒体权限',
                                    onSelected: (value) => _write(
                                      () => widget.repository.moderate(
                                        current.id,
                                        user,
                                        value == 'audio'
                                            ? MeetingMediaKind.audio
                                            : MeetingMediaKind.video,
                                        mute:
                                            !(value == 'audio'
                                                    ? current
                                                          .microphoneDisabledIds
                                                    : current.videoDisabledIds)
                                                .contains(user),
                                      ),
                                    ),
                                    itemBuilder: (_) => [
                                      PopupMenuItem(
                                        value: 'audio',
                                        child: Text(
                                          current.microphoneDisabledIds
                                                  .contains(user)
                                              ? '解除麦克风限制'
                                              : '限制麦克风',
                                        ),
                                      ),
                                      PopupMenuItem(
                                        value: 'video',
                                        child: Text(
                                          current.videoDisabledIds.contains(
                                                user,
                                              )
                                              ? '解除视频限制'
                                              : '限制视频',
                                        ),
                                      ),
                                    ],
                                  ),
                          ),
                        if (current.hostId == widget.repository.selfId)
                          Padding(
                            padding: const EdgeInsets.only(top: 24),
                            child: OutlinedButton.icon(
                              onPressed: active ? () => _close(current) : null,
                              icon: const Icon(Icons.stop_circle_outlined),
                              label: const Text('结束或取消会议'),
                            ),
                          ),
                      ],
                    ],
                  ),
          ),
        ),
      ),
    );
  }
}

class _InviteDialog extends StatefulWidget {
  const _InviteDialog({required this.repository});
  final MeetingRepository repository;

  @override
  State<_InviteDialog> createState() => _InviteDialogState();
}

class _InviteDialogState extends State<_InviteDialog> {
  final text = TextEditingController();
  late final StreamSubscription<int> _invalidations;

  @override
  void initState() {
    super.initState();
    _invalidations = widget.repository.session.invalidations.listen((_) {
      text.clear();
      if (mounted) {
        setState(() {});
      }
    });
  }

  @override
  void dispose() {
    unawaited(_invalidations.cancel());
    text.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('邀请参会者'),
    content: TextField(
      controller: text,
      enabled: widget.repository.current,
      maxLines: 3,
      decoration: const InputDecoration(
        labelText: '用户 ID，以逗号分隔',
        helperText: '服务端验证好友关系及邀请权限',
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('返回'),
      ),
      FilledButton(
        onPressed: widget.repository.current
            ? () => Navigator.pop(context, text.text)
            : null,
        child: const Text('提交邀请'),
      ),
    ],
  );
}

const _settingLabels = {
  'participantCanUnmuteSelf': '允许成员自行打开麦克风',
  'participantCanEnableVideo': '允许成员自行打开视频',
  'onlyHostInviteUser': '仅主持人可以邀请',
  'onlyHostShareScreen': '仅主持人可以共享屏幕',
  'joinDisableMicrophone': '加入时默认关闭麦克风',
  'joinDisableVideo': '加入时默认关闭视频',
  'isMuteAllMicrophone': '限制全体成员麦克风',
  'isMuteAllVideo': '限制全体成员视频',
};
