import '../../chat/domain/chat_message.dart';

enum GlobalSearchKind { contacts, groups, text, files }
enum GlobalSearchPhase { idle, loading, ready, error }

/// Ephemeral navigation intent. It never carries credentials or message bodies.
final class GlobalSourceSelection {
  const GlobalSourceSelection({
    required this.conversationId,
    required this.messageId,
    required this.isCurrent,
  });
  final String conversationId;
  final String messageId;
  final bool Function() isCurrent;
  bool matches(String conversation, String? message) =>
      conversation == conversationId && message == messageId;
}

bool Function()? globalSourceGuard(String cid, String? id, Object? extra,
    {required bool fromGlobal}) {
  final selection = extra is GlobalSourceSelection ? extra : null;
  if (!fromGlobal && selection == null) return null;
  // A restored global-source URL without its ephemeral ticket cannot silently
  // become a new ordinary deep-link intent after process/session retirement.
  return () => selection != null && selection.matches(cid, id) && selection.isCurrent();
}

final class GlobalMessageHit {
  const GlobalMessageHit({
    required this.conversationId,
    required this.title,
    required this.message,
    required this.isCurrent,
  });
  final String conversationId;
  final String title;
  final ChatMessage message;
  final bool Function() isCurrent;
  // Result identity is composite; shared privacy facts deliberately remain
  // clientID-keyed and may conservatively hide both colliding identities.
  String get key => '$conversationId\u0000${message.id}';
  bool visibleAt(DateTime now) =>
      isCurrent() && message.privacy.canViewAt(now);
  String get summary => message.privacy.containsPrivateContent
      ? '[阅后即焚消息]'
      : !message.privacy.inspectionComplete
      ? '[消息隐私状态无法确认]'
      : message.fileName ?? message.text;
}

final class GlobalMessageGroup {
  GlobalMessageGroup(this.conversationId, this.title, Iterable<GlobalMessageHit> hits)
      : hits = List.unmodifiable(hits);
  final String conversationId;
  final String title;
  final List<GlobalMessageHit> hits;
}
