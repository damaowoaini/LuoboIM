import '../../../core/privacy/message_privacy.dart';

final class Conversation {
  const Conversation({
    required this.id,
    required this.title,
    required this._preview,
    required this.updatedAt,
    required this.unread,
    required this.pinned,
    required this.muted,
    required this.isGroup,
    required this.canSend,
    this.avatar,
    this.userId,
    this.groupId,
    this.draft = '',
    this.mentions = false,
    this.privacy,
  });

  final String id;
  final String title;
  final String _preview;
  final MessagePrivacy? privacy;
  String get preview => privacy?.hidden == true
      ? '[消息内容已不可用]'
      : privacy?.containsPrivateContent == true
      ? '[阅后即焚消息]'
      : privacy?.inspectionComplete == false
      ? '[消息隐私状态无法确认]'
      : _preview;
  final DateTime updatedAt;
  final int unread;
  final bool pinned;
  final bool muted;
  final bool isGroup;
  final bool canSend;
  final Uri? avatar;
  final String? userId;
  final String? groupId;
  final String draft;
  final bool mentions;

  Conversation withoutMessagePreview() => Conversation(
    id: id,
    title: title,
    preview: '',
    updatedAt: updatedAt,
    unread: unread,
    pinned: pinned,
    muted: muted,
    isGroup: isGroup,
    canSend: canSend,
    avatar: avatar,
    userId: userId,
    groupId: groupId,
    draft: draft,
    mentions: mentions,
  );
}
