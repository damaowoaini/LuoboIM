import 'dart:convert';

import '../../../core/error/app_failure.dart';

enum MomentVisibility {
  public(0, '公开'),
  private(1, '仅自己'),
  included(2, '部分可见'),
  excluded(3, '不给谁看');

  const MomentVisibility(this.code, this.label);
  final int code;
  final String label;
  static MomentVisibility parse(Object? value) {
    final code = momentInt(value);
    return values.where((item) => item.code == code).firstOrNull ??
        (throw const AppFailure(FailureKind.invalidResponse));
  }
}

Map<String, Object?> momentMap(Object? value) {
  if (value is! Map || value.keys.any((key) => key is! String)) {
    throw const AppFailure(FailureKind.invalidResponse);
  }
  return Map<String, Object?>.from(value);
}

String momentString(Object? value, {bool required = false}) {
  if (value == null && !required) return '';
  if (value is! String || (required && value.isEmpty)) {
    throw const AppFailure(FailureKind.invalidResponse);
  }
  return value;
}

int momentInt(Object? value, {int fallback = 0}) {
  if (value == null) return fallback;
  if (value is int) return value;
  // Protobuf gateways may encode int64 as a decimal string.
  if (value is String && RegExp(r'^-?\d+$').hasMatch(value)) {
    final parsed = int.tryParse(value);
    if (parsed != null) return parsed;
  }
  throw const AppFailure(FailureKind.invalidResponse);
}

List<T> momentList<T>(Object? value, T Function(Object?) parse) {
  if (value == null) return const [];
  if (value is! List) throw const AppFailure(FailureKind.invalidResponse);
  return List.unmodifiable(value.map(parse));
}

Uri? momentMediaUri(String value) {
  final uri = Uri.tryParse(value);
  return uri != null &&
          uri.scheme == 'https' &&
          uri.host.isNotEmpty &&
          uri.userInfo.isEmpty
      ? uri
      : null;
}

DateTime? momentTime(Object? value) {
  final timestamp = momentInt(value);
  if (timestamp == 0) return null;
  if (timestamp < 0 || timestamp > 8640000000000000) {
    throw const AppFailure(FailureKind.invalidResponse);
  }
  return DateTime.fromMillisecondsSinceEpoch(timestamp);
}

final class MomentUser {
  const MomentUser({
    required this.id,
    this.name = '',
    this.avatar,
    this.likedAt,
  });
  final String id, name;
  final Uri? avatar;
  final DateTime? likedAt;
  String get displayName => name.isEmpty ? id : name;
  factory MomentUser.fromJson(Object? value) {
    final json = momentMap(value);
    return MomentUser(
      id: momentString(json['userID'], required: true),
      name: momentString(json['nickname'] ?? json['userName']),
      avatar: momentMediaUri(momentString(json['faceURL'])),
      likedAt: momentTime(json['likeTime']),
    );
  }
}

final class MomentMedia {
  const MomentMedia({
    required this.original,
    this.thumbnail = '',
    this.width = 0,
    this.height = 0,
  });
  final String original, thumbnail;
  final int width, height;
  Uri? get uri => momentMediaUri(original);
  Uri? get thumbnailUri => momentMediaUri(thumbnail);
  factory MomentMedia.fromJson(Object? value) {
    final json = momentMap(value);
    final width = momentInt(json['width']), height = momentInt(json['height']);
    if (width < 0 || height < 0) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return MomentMedia(
      original: momentString(json['original'], required: true),
      thumbnail: momentString(json['thumb']),
      width: width,
      height: height,
    );
  }
  Map<String, Object?> toJson() => {
    'original': original,
    'thumb': thumbnail,
    'width': width,
    'height': height,
  };
}

final class MomentComment {
  const MomentComment({
    required this.id,
    required this.author,
    required this.text,
    this.reply,
    this.createdAt,
  });
  final String id, text;
  final MomentUser author;
  final MomentUser? reply;
  final DateTime? createdAt;
  factory MomentComment.fromJson(Object? value) {
    final json = momentMap(value);
    final reply = momentString(json['replyUserID']);
    return MomentComment(
      id: momentString(json['commentID'], required: true),
      author: MomentUser.fromJson(json),
      text: momentString(json['content']),
      reply: reply.isEmpty
          ? null
          : MomentUser(
              id: reply,
              name: momentString(json['replyNickname']),
              avatar: momentMediaUri(momentString(json['replyFaceURL'])),
            ),
      createdAt: momentTime(json['createTime']),
    );
  }
}

final class Moment {
  const Moment({
    required this.id,
    required this.author,
    required this.text,
    required this.visibility,
    required this.mediaType,
    this.media = const [],
    this.likes = const [],
    this.comments = const [],
    this.audience = const [],
    this.mentions = const [],
    this.createdAt,
  });
  final String id, text;
  final MomentUser author;
  final MomentVisibility visibility;

  /// Legacy contract: 0 is pictures or text, 1 is video.
  final int mediaType;
  final List<MomentMedia> media;
  final List<MomentUser> likes, audience, mentions;
  final List<MomentComment> comments;
  final DateTime? createdAt;
  bool likedBy(String user) => likes.any((like) => like.id == user);
  bool canDeleteComment(String user, MomentComment comment) =>
      author.id == user ||
      comment.author.id == user ||
      comment.reply?.id == user;
  factory Moment.fromJson(Object? value) {
    final json = momentMap(value),
        content = momentMap(momentMap(value)['content']);
    return Moment(
      id: momentString(json['workMomentID'], required: true),
      author: MomentUser.fromJson(json),
      text: momentString(content['text']),
      visibility: MomentVisibility.parse(json['permission']),
      mediaType: momentInt(content['type']),
      media: momentList(content['metas'], MomentMedia.fromJson),
      likes: momentList(json['likeUsers'], MomentUser.fromJson),
      comments: momentList(json['comments'], MomentComment.fromJson),
      audience: momentList(json['permissionUsers'], MomentUser.fromJson),
      mentions: momentList(json['atUsers'], MomentUser.fromJson),
      createdAt: momentTime(json['createTime']),
    );
  }
}

final class MomentPage {
  const MomentPage(this.items, this.hasMore);
  final List<Moment> items;
  final bool hasMore;
}

final class MomentInteraction {
  const MomentInteraction(this.moment, this.type);
  final Moment moment;

  /// 1 like, 2 mention, 3 comment/reply; preserve unknown future values.
  final int type;
  MomentUser get actor => type == 1 && moment.likes.isNotEmpty
      ? moment.likes.first
      : type == 3 && moment.comments.isNotEmpty
      ? moment.comments.first.author
      : moment.author;
  DateTime? get createdAt => type == 1 && moment.likes.isNotEmpty
      ? moment.likes.first.likedAt
      : type == 3 && moment.comments.isNotEmpty
      ? moment.comments.first.createdAt
      : moment.createdAt;
  // One post can have independent interactions on the same page. The legacy
  // wire has no event ID; preserve its actor/time/comment identity.
  String get key => jsonEncode([
    moment.id,
    type,
    moment.likes
        .map((user) => [user.id, user.likedAt?.millisecondsSinceEpoch])
        .toList(),
    moment.comments.map((comment) => comment.id).toList(),
    moment.mentions.map((user) => user.id).toList(),
  ]);
  factory MomentInteraction.fromJson(Object? value) => MomentInteraction(
    Moment.fromJson(value),
    momentInt(momentMap(value)['type']),
  );
}

final class MomentDraft {
  MomentDraft({
    required this.text,
    this.mediaType = 0,
    this.visibility = MomentVisibility.public,
    List<MomentMedia> media = const [],
    List<String> audience = const [],
    List<String> groups = const [],
    List<String> mentions = const [],
  }) : media = List.unmodifiable(media),
       audience = List.unmodifiable(audience),
       groups = List.unmodifiable(groups),
       mentions = List.unmodifiable(mentions);
  final String text;
  final int mediaType;
  final MomentVisibility visibility;
  final List<MomentMedia> media;
  final List<String> audience, groups, mentions;
}
