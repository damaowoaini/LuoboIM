import 'dart:convert';

import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';

import '../domain/chat_message.dart';
import '../domain/chat_location.dart';
import '../domain/chat_mention.dart';
import '../domain/message_privacy.dart';

Uri? secureMediaUri(String? value) {
  final uri = value == null ? null : Uri.tryParse(value);
  if (uri == null ||
      uri.scheme != 'https' ||
      uri.host.isEmpty ||
      uri.userInfo.isNotEmpty) {
    return null;
  }
  return uri;
}

String messagePreview(Message? item, {MessagePrivacy? privacy}) {
  if (item == null) return '';
  final policy = privacy ?? inspectMessagePrivacy(item.toJson());
  if (policy.hidden) return '[消息内容已不可用]';
  if (policy.containsPrivateContent) return '[阅后即焚消息]';
  if (!policy.inspectionComplete) return '[消息隐私状态无法确认]';
  return _preview(item, _legacyCustom(item));
}

typedef _LegacyCustom = ({int type, Map data});

_LegacyCustom? _legacyCustom(Message item) {
  if (item.contentType != MessageType.custom) return null;
  final data = item.customElem?.data;
  if (data == null || data.length > 65536) return null;
  try {
    final value = jsonDecode(data);
    if (value is! Map || value['data'] is! Map) return null;
    final type = value['customType'];
    if (type is! int ||
        !const {901, 902, 903, 905, 910, 911, 912, 913}.contains(type)) {
      return null;
    }
    return (type: type, data: value['data'] as Map);
  } catch (_) {
    return null;
  }
}

/// The original protocol uses customType901 for call summaries. Reading their
/// visible row must not turn them into an ordinary group-message receipt.
bool isLegacyCallSummary(Message item) => _legacyCustom(item)?.type == 901;

String? _stickerSource(String? value) {
  if (value == null || value.length > 65536) return null;
  if (secureMediaUri(value) != null) return value;
  try {
    final decoded = jsonDecode(value);
    if (decoded is Map && decoded['url'] is String) {
      return decoded['url'] as String;
    }
  } catch (_) {
    /* Invalid custom data stays opaque; never display or open it as a URL. */
  }
  return null;
}

String _callPreview(Map data) {
  final type = switch (data['type']) {
    'video' => '视频通话',
    'voice' || 'audio' => '语音通话',
    _ => '通话',
  };
  final state = switch (data['state']) {
    'beHangup' || 'hangup' => () {
      final seconds = data['duration'];
      if (seconds is! int || seconds < 0) return '通话已结束';
      String two(int value) => value.toString().padLeft(2, '0');
      final duration = seconds >= 3600
          ? '${two(seconds ~/ 3600)}:${two(seconds ~/ 60 % 60)}:${two(seconds % 60)}'
          : '${two(seconds ~/ 60)}:${two(seconds % 60)}';
      return '通话时长 $duration';
    }(),
    'cancel' => '已取消',
    'beCanceled' => '对方已取消',
    'reject' => '已拒绝',
    'beRejected' => '对方已拒绝',
    'timeout' => '未接听',
    'networkError' => '网络异常，通话已结束',
    _ => '通话记录',
  };
  return '[$type] $state';
}

// This is a display projection of the existing more protocol. Custom data
// remains opaque to privacy inspection and is never rewritten into a native
// message element, permission, meeting admission, or trusted member identity.
String? _legacySummary(_LegacyCustom custom) {
  final data = custom.data;
  switch (custom.type) {
    case 903:
      final text = data['textElem'];
      if (text is Map && text['content'] is String) {
        return text['content'] as String;
      }
      final sound = data['soundElem'];
      if (sound is Map) {
        final duration = sound['duration'];
        final length = duration is int && duration >= 0 ? ' $duration 秒' : '';
        return '[标签语音]$length（当前版本暂不支持播放）';
      }
      return null;
    case 905:
      final subject = data['subject'];
      final id = data['id'];
      if (subject is! String || id is! String || id.trim().isEmpty) {
        return null;
      }
      final title = subject.trim().isEmpty ? '会议邀请' : subject;
      return '[会议邀请] $title\n会议号：$id\n当前版本未开放从邀请加入会议';
    case 910:
      return '消息已发出，但被对方拒收了';
    case 911:
      return '对方开启了好友验证，你还不是对方的好友';
    case 912:
      return '你已被移出群聊';
    case 913:
      return '群已经解散';
    default:
      return null;
  }
}

String _preview(Message item, _LegacyCustom? custom) {
  if (custom?.type == 901) return _callPreview(custom!.data);
  if (custom?.type == 902) return '[表情]';
  final summary = custom == null ? null : _legacySummary(custom);
  if (summary != null) return summary;
  return switch (item.contentType) {
    MessageType.text => item.textElem?.content ?? '',
    MessageType.atText => item.atTextElem?.text ?? '',
    MessageType.advancedText => item.advancedTextElem?.text ?? '',
    MessageType.quote => item.quoteElem?.text ?? '',
    MessageType.picture => '[图片]',
    MessageType.voice => '[语音]',
    MessageType.video => '[视频]',
    MessageType.file => '[文件] ${item.fileElem?.fileName ?? ''}',
    MessageType.card => '[名片] ${item.cardElem?.nickname ?? ''}',
    MessageType.location => '[位置]',
    MessageType.merger => '[聊天记录]',
    MessageType.customFace => '[表情]',
    MessageType.typing => '对方正在输入…',
    MessageType.custom ||
    MessageType.customMsgNotTriggerConversation ||
    MessageType.customMsgOnlineOnly =>
      (item.customElem?.description?.trim().isNotEmpty ?? false)
          ? item.customElem!.description!
          : '[自定义消息]',
    _ => item.notificationElem?.defaultTips ?? '[消息]',
  };
}

List<ReceivedMention> mapReceivedMentions(Message item) {
  final element = item.contentType == MessageType.atText
      ? item.atTextElem
      : null;
  if (element == null) return const [];
  final text = element.text ?? '';
  final ids =
      (element.atUserList ?? const <String>[])
          .take(maximumMentionRecipients)
          .where((id) => MentionIdentity(id, id).valid)
          .toSet()
          .toList()
        ..sort((a, b) => b.length.compareTo(a.length));
  final spans = <ReceivedMention>[];
  for (final id in ids) {
    final info = element.atUsersInfo
        ?.take(100)
        .where((v) => v.atUserID == id)
        .firstOrNull;
    final rawName = (info?.groupNickname ?? id)
        .replaceAll(RegExp(r'[\r\n\x00-\x1f\x7f]'), ' ')
        .trim();
    final name = String.fromCharCodes(rawName.runes.take(128));
    final identity = MentionIdentity(
      id,
      id == mentionAllId
          ? '所有人'
          : name.isEmpty
          ? id
          : name,
    );
    var from = 0;
    while (from < text.length) {
      final start = text.indexOf('@$id', from);
      if (start < 0) break;
      final end = start + id.length + 1;
      from = end;
      if (start > 0 &&
          !RegExp(r'[\s,.!?:;，。！？：；、(]').hasMatch(text[start - 1])) {
        continue;
      }
      if (end < text.length &&
          !RegExp(r'[\s,.!?:;，。！？：；、)]').hasMatch(text[end])) {
        continue;
      }
      if (spans.any((old) => start < old.end && end > old.start)) continue;
      spans.add(ReceivedMention(start, end, identity));
      if (spans.length >= 100) break;
    }
    if (spans.length >= 100) break;
  }
  spans.sort((a, b) => a.start.compareTo(b.start));
  return List.unmodifiable(spans);
}

final class _MergeProjectionBudget {
  int remaining = 100;
}

ChatMessage mapMessage(
  Message item,
  String currentUserId, {
  MessagePrivacy? privacy,
}) => _mapMessage(item, currentUserId, privacy, 0, _MergeProjectionBudget());

ChatMessage _mapMessage(
  Message item,
  String currentUserId,
  MessagePrivacy? privacy,
  int depth,
  _MergeProjectionBudget budget,
) {
  if (depth > 4 || --budget.remaining < 0) {
    return ChatMessage(
      id: item.clientMsgID ?? '',
      senderId: '',
      senderName: '消息',
      kind: ChatMessageKind.unsupported,
      text: '内容过多，暂不可用',
      sentAt: DateTime.fromMillisecondsSinceEpoch(0),
      mine: false,
      delivery: MessageDelivery.sent,
      mergedContentComplete: false,
      privacy: MessagePrivacy.combined([
        ?privacy,
        const MessagePrivacy(inspectionComplete: false),
      ]),
    );
  }
  final custom = _legacyCustom(item);
  final kind = custom?.type == 902
      ? ChatMessageKind.sticker
      : switch (item.contentType) {
          MessageType.text || MessageType.advancedText => ChatMessageKind.text,
          MessageType.atText =>
            item.atTextElem?.quoteMessage == null
                ? ChatMessageKind.text
                : ChatMessageKind.quote,
          MessageType.picture => ChatMessageKind.image,
          MessageType.voice => ChatMessageKind.voice,
          MessageType.video => ChatMessageKind.video,
          MessageType.file => ChatMessageKind.file,
          MessageType.location => ChatMessageKind.location,
          MessageType.card => ChatMessageKind.card,
          MessageType.quote => ChatMessageKind.quote,
          MessageType.merger => ChatMessageKind.forwarded,
          MessageType.customFace => ChatMessageKind.sticker,
          MessageType.typing => ChatMessageKind.typing,
          MessageType.custom ||
          MessageType.customMsgNotTriggerConversation ||
          MessageType.customMsgOnlineOnly => ChatMessageKind.custom,
          int value when value >= MessageType.notificationBegin =>
            ChatMessageKind.system,
          _ => ChatMessageKind.unsupported,
        };
  final picture = item.pictureElem;
  final media = switch (kind) {
    ChatMessageKind.image =>
      picture?.sourcePicture?.url ?? picture?.bigPicture?.url,
    ChatMessageKind.voice => item.soundElem?.sourceUrl,
    ChatMessageKind.video => item.videoElem?.videoUrl,
    ChatMessageKind.file => item.fileElem?.sourceUrl,
    ChatMessageKind.sticker =>
      custom?.type == 902
          ? (custom!.data['url'] is String
                ? custom.data['url'] as String
                : null)
          : _stickerSource(item.faceElem?.data),
    _ => null,
  };
  final timestamp = item.sendTime ?? item.createTime ?? 0;
  final policy = privacy ?? inspectMessagePrivacy(item.toJson());
  final merged = item.contentType == MessageType.merger
      ? item.mergeElem?.multiMessage
      : null;
  final originalAt =
      merged?.length == 1 && merged!.single.contentType == MessageType.atText
      ? merged.single
      : null;
  final projected = <ChatMessage>[
    for (final child in merged?.take(20) ?? <Message>[])
      _mapMessage(
        child,
        currentUserId,
        MessagePrivacy.combined([
          policy,
          inspectMessagePrivacy(child.toJson()),
        ]),
        depth + 1,
        budget,
      ),
  ];
  final complete =
      (merged?.length ?? 0) <= 20 &&
      projected.every((child) => child.mergedContentComplete);
  return ChatMessage(
    id: item.clientMsgID ?? '',
    mergedMessages: List.unmodifiable(projected),
    mergedContentComplete: complete,
    mentions: mapReceivedMentions(item),
    sourceGroupId: item.groupID?.isNotEmpty == true ? item.groupID : null,
    forwardedMessage: originalAt == null ? null : projected.single,
    senderId: item.sendID ?? '',
    senderName: item.senderNickname ?? item.sendID ?? '未知用户',
    kind: kind,
    text: _preview(item, custom),
    sentAt: DateTime.fromMillisecondsSinceEpoch(timestamp),
    mine: item.sendID == currentUserId,
    delivery: switch (item.status) {
      MessageStatus.sending => MessageDelivery.sending,
      MessageStatus.failed => MessageDelivery.failed,
      _ => MessageDelivery.sent,
    },
    avatar: secureMediaUri(item.senderFaceUrl),
    location: kind == ChatMessageKind.location
        ? ChatLocation.parse(
            latitude: item.locationElem?.latitude,
            longitude: item.locationElem?.longitude,
            description: item.locationElem?.description,
          )
        : null,
    media: secureMediaUri(media),
    thumbnail: secureMediaUri(
      item.videoElem?.snapshotUrl ?? picture?.snapshotPicture?.url,
    ),
    fileName: item.fileElem?.fileName,
    fileBytes: item.fileElem?.fileSize,
    durationSeconds: item.soundElem?.duration ?? item.videoElem?.duration,
    quoteId: (item.quoteElem?.quoteMessage ?? item.atTextElem?.quoteMessage)
        ?.clientMsgID,
    quotePreview:
        (item.quoteElem?.quoteMessage ?? item.atTextElem?.quoteMessage) == null
        ? null
        : messagePreview(
            item.quoteElem?.quoteMessage ?? item.atTextElem?.quoteMessage,
          ),
    read: item.isRead ?? false,
    privacy: policy,
    groupReadCount: item.sessionType == 3 || (item.groupID?.isNotEmpty ?? false)
        ? item.attachedInfoElem?.groupHasReadInfo?.hasReadCount ?? 0
        : null,
  );
}
