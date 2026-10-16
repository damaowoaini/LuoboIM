import 'dart:convert';

import 'package:crypto/crypto.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/storage/remote_attachment_store.dart';

/// Gallery uploads own a final signed-upload receipt. Chat references never
/// gain independent retention permission merely by being added to this list.
final class SavedEmojiSource {
  const SavedEmojiSource(this.conversationId, this.messageId);
  final String conversationId, messageId;
  Map<String, Object> toJson() => {'cid': conversationId, 'id': messageId};
}

final class SavedEmoji {
  SavedEmoji({
    required this.url,
    required this.width,
    required this.height,
    this.source,
  }) {
    if (!isSafeAttachmentUri(url) ||
        url.toString().length > 8192 ||
        width < 1 ||
        height < 1 ||
        width * height > 100000000 ||
        source != null &&
            (source!.conversationId.isEmpty ||
                source!.conversationId.length > 1024 ||
                source!.messageId.isEmpty ||
                source!.messageId.length > 1024)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
  }
  final Uri url;
  final int width, height;
  final SavedEmojiSource? source;
  String get id => sha256.convert(utf8.encode(jsonEncode(toJson()))).toString();
  Map<String, Object> get faceData => {
    'url': url.toString(),
    'width': width,
    'height': height,
  };
  Map<String, Object> toJson() => {
    ...faceData,
    'origin': source == null ? 'upload_ack' : 'chat_reference',
    if (source != null) 'source': source!.toJson(),
  };
  factory SavedEmoji.fromJson(Object? value) {
    if (value is! Map ||
        value.keys.any(
          (k) =>
              !const {'url', 'width', 'height', 'origin', 'source'}.contains(k),
        ) ||
        value['url'] is! String ||
        value['width'] is! int ||
        value['height'] is! int ||
        !const ['upload_ack', 'chat_reference'].contains(value['origin'])) {
      throw const AppFailure(FailureKind.localStorage);
    }
    SavedEmojiSource? source;
    if (value['origin'] == 'chat_reference') {
      final raw = value['source'];
      if (raw is! Map ||
          raw.keys.any((k) => !const {'cid', 'id'}.contains(k)) ||
          raw['cid'] is! String ||
          raw['id'] is! String) {
        throw const AppFailure(FailureKind.localStorage);
      }
      source = SavedEmojiSource(raw['cid'] as String, raw['id'] as String);
    } else if (value.containsKey('source')) {
      throw const AppFailure(FailureKind.localStorage);
    }
    return SavedEmoji(
      url: Uri.parse(value['url'] as String),
      width: value['width'] as int,
      height: value['height'] as int,
      source: source,
    );
  }
  @override
  String toString() => 'SavedEmoji';
}

enum SavedEmojiSendOutcome { sent, notDispatched, unconfirmed }
