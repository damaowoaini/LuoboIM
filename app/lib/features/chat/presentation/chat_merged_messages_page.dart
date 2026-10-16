import 'package:flutter/material.dart';

import '../domain/chat_message.dart';
import 'message_privacy_content.dart';

/// Reuses the ordinary media/text child renderer. No body is copied into a
/// dialog title/summary, and the shared policy can withdraw an open route.
class ChatMergedMessagesPage extends StatefulWidget {
  const ChatMergedMessagesPage({
    super.key,
    required this.root,
    required this.messages,
    required this.canView,
    required this.childBuilder,
    this.now,
    this.onExit,
  });
  final ChatMessage root;
  final DateTime Function()? now;
  final List<ChatMessage> messages;
  final bool Function() canView;
  final Widget Function(ChatMessage, bool Function()) childBuilder;
  final VoidCallback? onExit;
  @override
  State<ChatMergedMessagesPage> createState() => _ChatMergedMessagesPageState();
}

class _ChatMergedMessagesPageState extends State<ChatMergedMessagesPage> {
  bool active = true;
  bool current() =>
      mounted && active && (ModalRoute.of(context)?.isCurrent ?? false);
  void exit() {
    if (active) {
      active = false;
      widget.onExit?.call();
    }
  }

  @override
  void dispose() {
    exit();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => PopScope<void>(
    onPopInvokedWithResult: (didPop, _) {
      if (didPop) exit();
    },
    child: Scaffold(
      appBar: AppBar(title: const Text('聊天记录')),
      body: MessagePrivacyContent(
        privacy: widget.root.privacy,
        now: widget.now,
        onExpired: exit,
        child: Builder(
          builder: (context) {
            if (!widget.canView()) return const Center(child: Text('消息内容已不可用'));
            return ListView.builder(
              itemCount: widget.messages.length,
              padding: const EdgeInsets.all(16),
              itemBuilder: (context, index) {
                final item = widget.messages[index];
                return Padding(
                  padding: const EdgeInsets.only(bottom: 16),
                  child: MessagePrivacyContent(
                    privacy: item.privacy,
                    now: widget.now,
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          item.senderName,
                          style: Theme.of(context).textTheme.labelLarge,
                        ),
                        const SizedBox(height: 6),
                        widget.childBuilder(item, current),
                      ],
                    ),
                  ),
                );
              },
            );
          },
        ),
      ),
    ),
  );
}
