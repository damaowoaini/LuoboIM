import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../data/meeting_repository.dart';
import '../meeting_providers.dart';
import 'meeting_controller.dart';
import 'meeting_detail_page.dart';
import 'meeting_format.dart';

/// Lists existing rooms. New schedules use the separate booking route;
/// room membership is never used as a substitute for media admission.
class MeetingsPage extends ConsumerStatefulWidget {
  const MeetingsPage({super.key, this.repository});
  final MeetingRepository? repository;
  @override
  ConsumerState<MeetingsPage> createState() => _MeetingsPageState();
}

class _MeetingsPageState extends ConsumerState<MeetingsPage> {
  late final MeetingController controller;
  final query = TextEditingController();
  int _viewEpoch = 0;
  @override
  void initState() {
    super.initState();
    controller = MeetingController(
      widget.repository ?? ref.read(meetingRepositoryProvider),
    )..addListener(_changed);
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
    query.dispose();
    super.dispose();
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (ModalRoute.of(context)?.isCurrent == false) {
      ++_viewEpoch;
    }
  }

  void _openBooking(String path, int view) {
    if (!mounted || !controller.usable || view != _viewEpoch ||
        ModalRoute.of(context)?.isCurrent != true) {
      return;
    }
    // Scheduling has its own durable intent and never issues a media grant.
    // Existing room edits continue through MeetingDetailPage.
    context.push(path);
  }

  @override
  Widget build(BuildContext context) {
    final view = _viewEpoch;
    final keyword = query.text.trim().toLowerCase();
    final values = controller.items
        .where(
          (meeting) =>
              keyword.isEmpty ||
              meeting.displayTitle.toLowerCase().contains(keyword) ||
              meeting.id.toLowerCase().contains(keyword),
        )
        .toList();
    return Scaffold(
      appBar: AppBar(
        title: const Text('会议'),
        actions: [
          IconButton(
            tooltip: '我的预约（仅排期）',
            icon: const Icon(Icons.event_note),
            onPressed: controller.usable
                ? () => _openBooking('/meetings/bookings', view)
                : null,
          ),
          IconButton(
            tooltip: '预约会议',
            onPressed: controller.usable
                ? () => _openBooking('/meetings/bookings/new', view)
                : null,
            icon: const Icon(Icons.add),
          ),
        ],
      ),
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 900),
            child: Column(
              children: [
                Padding(
                  padding: const EdgeInsets.all(16),
                  child: TextField(
                    controller: query,
                    enabled: controller.usable,
                    decoration: const InputDecoration(
                      labelText: '查找已加载的会议名称或会议号',
                      prefixIcon: Icon(Icons.search),
                    ),
                    onChanged: (_) => setState(() {}),
                  ),
                ),
                const Padding(
                  padding: EdgeInsets.fromLTRB(16, 0, 16, 12),
                  child: Text('管理当前账号已有的会议；预约只保存排期，不会加入通话。音视频入会与屏幕共享暂未开放。'),
                ),
                if (controller.failure case final failure?)
                  MaterialBanner(
                    content: Text(failure.message),
                    actions: [
                      TextButton(
                        onPressed: controller.usable
                            ? controller.refresh
                            : null,
                        child: const Text('刷新'),
                      ),
                    ],
                  ),
                Expanded(
                  child: controller.loading && controller.items.isEmpty
                      ? const Center(child: CircularProgressIndicator())
                      : RefreshIndicator(
                          onRefresh: controller.refresh,
                          child: ListView.builder(
                            physics: const AlwaysScrollableScrollPhysics(),
                            itemCount: values.isEmpty ? 1 : values.length,
                            itemBuilder: (context, index) {
                              if (values.isEmpty) {
                                return const Padding(
                                  padding: EdgeInsets.all(32),
                                  child: Text('暂无匹配的会议，可下拉刷新或预约会议。'),
                                );
                              }
                              final meeting = values[index];
                              return ListTile(
                                leading: const Icon(Icons.video_call_outlined),
                                title: Text(meeting.displayTitle),
                                subtitle: Text(
                                  '${meetingDate(meeting.start)} — ${meetingDate(meeting.end)}\n会议号：${meeting.id}\n${meeting.hostId == controller.repository.selfId ? '由你主持' : '已邀请你参加'}',
                                ),
                                isThreeLine: true,
                                onTap: controller.usable
                                    ? () async {
                                        await Navigator.of(context).push<void>(
                                          MaterialPageRoute(
                                            builder: (_) => MeetingDetailPage(
                                              repository: controller.repository,
                                              meetingId: meeting.id,
                                            ),
                                          ),
                                        );
                                        if (mounted && controller.usable) {
                                          await controller.refresh();
                                        }
                                      }
                                    : null,
                              );
                            },
                          ),
                        ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
