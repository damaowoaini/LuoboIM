import 'message_privacy.dart';
import 'chat_location.dart';
import 'chat_card.dart';
import 'chat_mention.dart';

enum ChatMessageKind {
  text,
  image,
  voice,
  video,
  file,
  location,
  card,
  quote,
  forwarded,
  custom,
  sticker,
  system,
  typing,
  unsupported,
}

enum MessageDelivery { sending, sent, failed }

enum AttachmentKind { image, voice, video, file }

// A received 903 audio view, never a native voice/file or retention capability.
typedef TagAudioProjection = ({Uri source, int? durationSeconds});

// Display fields from received905; none grants meeting admission or identity.
typedef TagMeetingProjection = ({
  String inviterUserId,
  String? inviterNickname,
  String? inviterFaceUrl,
  String subject,
  String id,
  int startSeconds,
  int durationSeconds,
});

final class ChatAttachment {
  const ChatAttachment({
    required this.kind,
    required this.path,
    required this.name,
    this.durationSeconds,
  });
  final AttachmentKind kind;
  final String path;
  final String name;
  final int? durationSeconds;
}

final class ChatMessage {
  const ChatMessage({
    required this.id,
    required this.senderId,
    required this.senderName,
    required this.kind,
    required this.text,
    required this.sentAt,
    required this.mine,
    required this.delivery,
    this.avatar,
    this.mentions = const [],
    this.forwardedMessage,
    this.mergedMessages = const [],
    this.mergedContentComplete = true,
    this.sourceGroupId,
    this.location,
    this.card,
    this.media,
    this.tagAudio,
    this.tagTextForwardable = false,
    this.tagMeeting,
    this.legacyCustomType,
    this.tagText,
    this.thumbnail,
    this.fileName,
    this.fileBytes,
    this.durationSeconds,
    this.quoteId,
    this.quotePreview,
    this.read = false,
    this.groupReadCount,
    this.localMedia,
    this.localThumbnail,
    this.privacy = const MessagePrivacy(),
  });

  final String id;
  final List<ReceivedMention> mentions;
  final ChatMessage? forwardedMessage;
  final List<ChatMessage> mergedMessages;
  final bool mergedContentComplete;
  final String? sourceGroupId;
  final String senderId;
  final String senderName;
  final ChatMessageKind kind;
  final String text;
  final DateTime sentAt;
  final bool mine;
  final MessageDelivery delivery;
  final Uri? avatar;
  final ChatLocation? location;
  final ChatCard? card;
  final Uri? media;
  final TagAudioProjection? tagAudio;
  // Set only by the legacy text903 projection; custom identity stays intact.
  final bool tagTextForwardable;
  final TagMeetingProjection? tagMeeting;
  // Decoded protocol classification, never sender identity or permission.
  final int? legacyCustomType;
  final String? tagText;
  final Uri? thumbnail;
  final String? fileName;
  final int? fileBytes;
  final int? durationSeconds;
  final String? quoteId;
  final String? quotePreview;
  final bool read;
  final int? groupReadCount;
  final MessagePrivacy privacy;
  // Native and other custom menus keep their existing behavior. For903/905,
  // text copy/reply follows the text projection, not forwarding permission.
  bool get canUseTextActions =>
      kind != ChatMessageKind.custom ||
      !const {903, 905}.contains(legacyCustomType) ||
      tagText != null;
  bool get canForward =>
      mergedContentComplete &&
      delivery == MessageDelivery.sent &&
      privacy.allowsRetention &&
      (kind != ChatMessageKind.custom ||
          tagTextForwardable ||
          tagMeeting != null) &&
      !const {
        ChatMessageKind.system,
        ChatMessageKind.typing,
        ChatMessageKind.unsupported,
      }.contains(kind);
  // These paths are assigned only after account-scoped staging by the data
  // layer. Paths embedded in received protocol messages are never trusted.
  final String? localMedia;
  final String? localThumbnail;
  Uri? get playableMedia =>
      media ?? (localMedia == null ? null : Uri.file(localMedia!));
  Uri? get playableAudio =>
      kind == ChatMessageKind.custom ? tagAudio?.source : playableMedia;

  ChatMessage withDelivery(MessageDelivery value) => ChatMessage(
    id: id,
    senderId: senderId,
    mentions: mentions,
    forwardedMessage: forwardedMessage,
    mergedMessages: mergedMessages,
    mergedContentComplete: mergedContentComplete,
    sourceGroupId: sourceGroupId,
    senderName: senderName,
    kind: kind,
    text: text,
    sentAt: sentAt,
    mine: mine,
    delivery: value,
    avatar: avatar,
    location: location,
    card: card,
    media: media,
    tagAudio: tagAudio,
    tagTextForwardable: tagTextForwardable,
    tagMeeting: tagMeeting,
    legacyCustomType: legacyCustomType,
    tagText: tagText,
    thumbnail: thumbnail,
    fileName: fileName,
    fileBytes: fileBytes,
    durationSeconds: durationSeconds,
    quoteId: quoteId,
    quotePreview: quotePreview,
    read: read,
    groupReadCount: groupReadCount,
    localMedia: localMedia,
    localThumbnail: localThumbnail,
    privacy: privacy,
  );

  ChatMessage withLocalMedia(String path, {String? snapshot}) => ChatMessage(
    id: id,
    senderId: senderId,
    mentions: mentions,
    forwardedMessage: forwardedMessage,
    mergedMessages: mergedMessages,
    mergedContentComplete: mergedContentComplete,
    sourceGroupId: sourceGroupId,
    senderName: senderName,
    kind: kind,
    text: text,
    sentAt: sentAt,
    mine: mine,
    delivery: delivery,
    avatar: avatar,
    location: location,
    card: card,
    media: media,
    tagAudio: tagAudio,
    tagTextForwardable: tagTextForwardable,
    tagMeeting: tagMeeting,
    legacyCustomType: legacyCustomType,
    tagText: tagText,
    thumbnail: thumbnail,
    fileName: fileName,
    fileBytes: fileBytes,
    durationSeconds: durationSeconds,
    quoteId: quoteId,
    quotePreview: quotePreview,
    read: read,
    groupReadCount: groupReadCount,
    localMedia: path,
    localThumbnail: snapshot,
    privacy: privacy,
  );
}

final class MessagePage {
  MessagePage({
    required Iterable<ChatMessage> messages,
    required this.isEnd,
    required this.lastMinSeq,
  }) : messages = List.unmodifiable(messages);
  final List<ChatMessage> messages;
  final bool isEnd;
  final int lastMinSeq;
}

final class MessageReader {
  const MessageReader({required this.userId, required this.name, this.avatar});
  final String userId;
  final String name;
  final Uri? avatar;
}

final class MessageReadersPage {
  MessageReadersPage({
    required Iterable<MessageReader> readers,
    required this.nextOffset,
    required this.hasMore,
  }) : readers = List.unmodifiable(readers);
  final List<MessageReader> readers;
  final int nextOffset;
  final bool hasMore;
}
