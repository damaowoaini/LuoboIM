import 'chat_message.dart';

/// Lossless protocol JSON for favorites; UI never receives the SDK model.
final class ChatFavoriteSnapshot {
  const ChatFavoriteSnapshot({
    required this.messageId,
    required this.conversationId,
    required this.content,
    required this.kind,
    required this.title,
    required this.thumbnail,
  });
  final String messageId, conversationId, content, title, thumbnail;
  final ChatMessageKind kind;
  @override
  String toString() => 'ChatFavoriteSnapshot';
}

final class ChatCallCandidate {
  const ChatCallCandidate(this.id, this.name);
  final String id, name;
}
