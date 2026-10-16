import 'dart:async';

import 'package:flutter/material.dart';

import '../../../design/tokens.dart';
import '../data/chat_repository.dart';
import '../domain/chat_message.dart';
import '../domain/chat_search.dart';
import 'chat_search_controller.dart';
import 'message_privacy_content.dart';

class ChatSearchPage extends StatefulWidget {
  const ChatSearchPage({super.key, required this.repository});
  final ChatRepository repository;
  @override
  State<ChatSearchPage> createState() => _ChatSearchPageState();
}

class _ChatSearchPageState extends State<ChatSearchPage> {
  late final ChatSearchController controller;
  final input = TextEditingController();
  Timer? _debounce;

  @override
  void initState() {
    super.initState();
    controller = ChatSearchController(widget.repository)..addListener(_changed);
  }

  void _changed() {
    if (!mounted) return;
    if (!controller.usable) {
      _debounce?.cancel();
      input.clear();
    }
    setState(() {});
  }

  void _search() {
    _debounce?.cancel();
    unawaited(controller.search(query: input.text));
  }

  @override
  void dispose() {
    _debounce?.cancel();
    controller.removeListener(_changed);
    controller.dispose();
    input.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('查找聊天记录')),
    body: SafeArea(
      child: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 900),
          child: Column(
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
                child: TextField(
                  controller: input,
                  enabled:
                      controller.usable &&
                      controller.filter != ChatHistoryFilter.media,
                  maxLength: 200,
                  textInputAction: TextInputAction.search,
                  decoration: InputDecoration(
                    labelText: controller.filter == ChatHistoryFilter.media
                        ? '图片和视频按时间浏览'
                        : '搜索文字或文件名',
                    counterText: '',
                    prefixIcon: const Icon(Icons.search),
                    suffixIcon: IconButton(
                      tooltip: '清除搜索',
                      onPressed: input.text.isEmpty
                          ? null
                          : () {
                              input.clear();
                              _search();
                            },
                      icon: const Icon(Icons.close),
                    ),
                  ),
                  onSubmitted: (_) => _search(),
                  onChanged: (_) {
                    _debounce?.cancel();
                    setState(() {});
                    _debounce = Timer(
                      const Duration(milliseconds: 350),
                      _search,
                    );
                  },
                ),
              ),
              Padding(
                padding: const EdgeInsets.symmetric(
                  horizontal: 16,
                  vertical: 8,
                ),
                child: Wrap(
                  spacing: 8,
                  runSpacing: 4,
                  children: ChatHistoryFilter.values
                      .map(
                        (filter) => ChoiceChip(
                          label: Text(switch (filter) {
                            ChatHistoryFilter.all => '全部',
                            ChatHistoryFilter.text => '文字',
                            ChatHistoryFilter.media => '图片和视频',
                            ChatHistoryFilter.file => '文件',
                          }),
                          selected: controller.filter == filter,
                          onSelected: controller.usable
                              ? (_) {
                                  _debounce?.cancel();
                                  if (filter == ChatHistoryFilter.media) {
                                    input.clear();
                                  }
                                  unawaited(
                                    controller.search(
                                      query: input.text,
                                      filter: filter,
                                    ),
                                  );
                                }
                              : null,
                        ),
                      )
                      .toList(),
                ),
              ),
              const Padding(
                padding: EdgeInsets.fromLTRB(16, 0, 16, 8),
                child: Text(
                  '仅查找已同步到本机的记录；点选结果可回到原消息。',
                  style: TextStyle(color: AppColors.secondary, fontSize: 12),
                ),
              ),
              if (controller.failure case final failure?)
                MaterialBanner(
                  content: Text(failure.message),
                  actions: [
                    TextButton(
                      onPressed: !controller.usable
                          ? null
                          : () => unawaited(
                              controller.messages.isNotEmpty &&
                                      controller.hasMore
                                  ? controller.loadMore()
                                  : controller.search(),
                            ),
                      child: const Text('重试'),
                    ),
                  ],
                ),
              Expanded(child: _results()),
            ],
          ),
        ),
      ),
    ),
  );

  Widget _results() {
    if (controller.loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (controller.messages.isEmpty) {
      return Center(
        child: Text(
          controller.failure != null
              ? '暂时无法查找记录'
              : controller.searched
              ? '没有找到匹配记录'
              : '输入关键词，或选择图片、视频和文件',
        ),
      );
    }
    return NotificationListener<ScrollNotification>(
      onNotification: (notification) {
        if (controller.failure == null &&
            notification.metrics.extentAfter < 180) {
          unawaited(controller.loadMore());
        }
        return false;
      },
      child: ListView.separated(
        itemCount: controller.messages.length + (controller.hasMore ? 1 : 0),
        separatorBuilder: (_, _) => const Divider(height: 1),
        itemBuilder: (context, index) {
          if (index == controller.messages.length) {
            return Padding(
              padding: const EdgeInsets.all(12),
              child: controller.loadingMore
                  ? const Center(child: CircularProgressIndicator())
                  : TextButton(
                      onPressed: controller.loadMore,
                      child: const Text('加载更多记录'),
                    ),
            );
          }
          final message = controller.messages[index];
          return MessagePrivacyContent(
            key: ValueKey(message.id),
            privacy: message.privacy,
            now: widget.repository.now,
            child: ListTile(
              leading: Icon(switch (message.kind) {
                ChatMessageKind.image => Icons.image_outlined,
                ChatMessageKind.video => Icons.videocam_outlined,
                ChatMessageKind.file => Icons.insert_drive_file_outlined,
                _ => Icons.chat_bubble_outline,
              }),
              title: Text(
                message.privacy.containsPrivateContent
                    ? '[阅后即焚消息]'
                    : !message.privacy.inspectionComplete
                    ? '[消息隐私状态无法确认]'
                    : message.fileName ?? message.text,
                maxLines: 3,
                overflow: TextOverflow.ellipsis,
              ),
              subtitle: Text('${message.senderName} · ${_date(message)}'),
              trailing: const Icon(Icons.chevron_right),
              onTap: controller.usable
                  ? () {
                      if (widget.repository.canView(message)) {
                        Navigator.pop(context, message.id);
                      }
                    }
                  : null,
            ),
          );
        },
      ),
    );
  }

  String _date(ChatMessage message) {
    final date = message.sentAt.toLocal();
    String two(int value) => value.toString().padLeft(2, '0');
    return '${date.year}-${two(date.month)}-${two(date.day)} ${two(date.hour)}:${two(date.minute)}';
  }
}
