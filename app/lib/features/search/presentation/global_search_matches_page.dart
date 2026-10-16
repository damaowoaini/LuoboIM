import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../../chat/presentation/message_privacy_content.dart';
import '../domain/global_search.dart';
import 'global_search_controller.dart';

Future<void> openGlobalSource(BuildContext context, GlobalSourceSelection selection) async {
  if (ModalRoute.of(context)?.isCurrent != true || !selection.isCurrent()) return;
  await context.push(Uri(pathSegments: ['', 'chat', selection.conversationId],
      queryParameters: {'message': selection.messageId, 'source': 'global'}).toString(), extra: selection);
}

class GlobalSearchMatchesPage extends StatefulWidget {
  const GlobalSearchMatchesPage({super.key, required this.controller,
      required this.conversationId, required this.queryEpoch});
  final GlobalSearchController controller;
  final String conversationId;
  final int queryEpoch;
  @override
  State<GlobalSearchMatchesPage> createState() => _GlobalSearchMatchesPageState();
}

class _GlobalSearchMatchesPageState extends State<GlobalSearchMatchesPage> {
  late final int epoch = widget.queryEpoch;
  int shown = 30;
  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.controller,
    builder: (context, _) {
      final controller = widget.controller;
      final group = controller.usable && controller.epoch == epoch
          ? controller.threads.where((g) => g.conversationId == widget.conversationId).firstOrNull
          : null;
      return Scaffold(
        appBar: AppBar(title: const Text('相关聊天记录')),
        body: group == null || group.hits.isEmpty
            ? const Center(child: Text('结果已不可用，请返回重新搜索'))
            : ListView(children: [
                ListTile(title: Text(group.title), subtitle: Text(group.conversationId)),
                for (final hit in group.hits.take(shown))
                  MessagePrivacyContent(
                    key: ValueKey(hit.key), privacy: hit.message.privacy,
                    now: controller.repository.now,
                    child: ListTile(
                      title: Text(hit.summary, maxLines: 4, overflow: TextOverflow.ellipsis),
                      subtitle: Text('${hit.message.senderName} · ${hit.message.sentAt.toLocal()}'),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: () {
                        if (controller.epoch == epoch && hit.visibleAt(controller.repository.now())) {
                          openGlobalSource(context, controller.repository.selection(hit));
                        }
                      },
                    ),
                  ),
                if (shown < group.hits.length)
                  TextButton(onPressed: () => setState(() => shown += 30),
                      child: const Text('显示更多匹配记录')),
              ]),
      );
    },
  );
}
