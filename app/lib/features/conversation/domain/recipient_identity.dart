import 'conversation.dart';

/// The identity shown for a destination must also be the one used for sending.
/// Conversation storage keys are not user-facing account or group identifiers.
extension ConversationRecipientIdentity on Conversation {
  String get recipientStableId => (isGroup ? groupId : userId) ?? '';
  bool get hasRecipientIdentity => recipientStableId.trim().isNotEmpty;
  String get recipientIdentityLabel => hasRecipientIdentity
      ? '${isGroup ? '群 ID' : '账号 ID'}：$recipientStableId'
      : '身份信息不可用';

  bool matchesRecipient(String query) {
    final needle = query.trim().toLowerCase();
    return title.toLowerCase().contains(needle) ||
        recipientStableId.toLowerCase().contains(needle);
  }
}
