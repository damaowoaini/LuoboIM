import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/meeting_repository.dart';
import '../meeting_providers.dart';
import 'meeting_controller.dart';
import 'meeting_detail_page.dart';
import 'meeting_editor_page.dart';
import 'meeting_format.dart';

/// Kept independent until the root router and shared RTC admission lease are
/// wired in a later cohort. No provider or SDK is touched before mounting.
class MeetingsPage extends ConsumerStatefulWidget {
  const MeetingsPage({super.key, this.repository});
  final MeetingRepository? repository;
  @override
  ConsumerState<MeetingsPage> createState() => _MeetingsPageState();
}

class _MeetingsPageState extends ConsumerState<MeetingsPage> {
  late final MeetingController controller;
  final query = TextEditingController();
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

  Future<void> _create() async {
    await Navigator.of(context).push<void>(
      MaterialPageRoute(
        builder: (_) => MeetingEditorPage(repository: controller.repository),
      ),
    );
    if (mounted && controller.usable) {
      await controller.refresh();
    }
  }

  @override
  Widget build(BuildContext context) {
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
            tooltip: '预约会议',
            onPressed: controller.usable ? _create : null,
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
                  child: Text('显示当前账号尚未结束的会议，可预约和管理会议；音视频入会与屏幕共享暂未开放。'),
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
