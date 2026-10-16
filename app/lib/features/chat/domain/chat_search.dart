import 'chat_message.dart';

enum ChatHistoryFilter { all, text, media, file }

final class ChatSearchPage {
  ChatSearchPage({
    required Iterable<ChatMessage> messages,
    required this.hasMore,
  }) : messages = List.unmodifiable(messages);
  final List<ChatMessage> messages;
  final bool hasMore;
}
