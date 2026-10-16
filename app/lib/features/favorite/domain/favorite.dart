import 'dart:convert';

import '../../../core/error/app_failure.dart';
import '../../../core/privacy/message_privacy.dart';

enum FavoriteKind {
  message('消息'),
  file('文件'),
  image('图片'),
  video('视频'),
  audio('音频'),
  link('链接');

  const FavoriteKind(this.label);
  final String label;
  int get code => index + 1;
}

final class FavoriteItem {
  FavoriteItem({
    required this.id,
    required this.ownerId,
    required this.kind,
    required this.sourceId,
    required String content,
    this.conversationId = '',
    String title = '',
    String thumbnail = '',
    this.categoryId = 'default',
    Iterable<String> tags = const [],
    String notes = '',
    this.createdAt = 0,
    this.updatedAt = 0,
    String ex = '',
    this.retentionBlocked = false,
    this.sourcePrivacy,
  }) : content = retentionBlocked ? '' : content,
       title = retentionBlocked ? '私密收藏已隔离' : title,
       thumbnail = retentionBlocked ? '' : thumbnail,
       notes = retentionBlocked ? '' : notes,
       ex = retentionBlocked ? '' : ex,
       tags = List.unmodifiable(retentionBlocked ? const <String>[] : tags);
  final String id,
      ownerId,
      sourceId,
      conversationId,
      title,
      content,
      thumbnail,
      categoryId,
      notes,
      ex;
  final FavoriteKind kind;
  final List<String> tags;
  final int createdAt, updatedAt;
  final bool retentionBlocked;
  final MessagePrivacy? sourcePrivacy;
  bool get canUseContent =>
      !retentionBlocked && decoded.privacy.allowsRetention;
  String? get retentionNotice =>
      retentionBlocked ? '私密收藏已隔离，可删除此记录' : decoded.privacy.notice;
  String get displayTitle => !canUseContent
      ? '私密收藏已隔离'
      : title.trim().isNotEmpty
      ? title
      : decoded.summary.isNotEmpty
      ? decoded.summary
      : kind.label;
  late final FavoriteContent decoded = retentionBlocked
      ? const FavoriteContent(
          '私密收藏已隔离',
          privacy: MessagePrivacy(inspectionComplete: false),
        )
      : FavoriteContent.decode(content, kind, privacy: sourcePrivacy);

  factory FavoriteItem.fromJson(
    Object? value,
    String owner, {
    bool retentionDenied = false,
    MessagePrivacy? Function(
      String sourceId,
      String conversationId,
      String content,
    )?
    projectPrivacy,
  }) {
    final json = favoriteMap(value);
    final id = favoriteString(json, 'favoriteID');
    final userId = favoriteString(json, 'userID');
    final type = json['favoriteType'];
    final blocked = json['retentionBlocked'];
    if (blocked != null && blocked is! bool) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    if (id.isEmpty || userId != owner || type is! int || type < 1 || type > 6) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final tags = json['tags'];
    if (tags != null && (tags is! List || tags.any((tag) => tag is! String))) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return FavoriteItem(
      id: id,
      ownerId: userId,
      kind: FavoriteKind.values[type - 1],
      sourceId: favoriteString(json, 'sourceID'),
      content: favoriteString(json, 'content'),
      conversationId: favoriteString(json, 'conversationID'),
      title: favoriteString(json, 'title'),
      thumbnail: favoriteString(json, 'thumbnailURL'),
      categoryId: favoriteString(json, 'categoryID').isEmpty
          ? 'default'
          : favoriteString(json, 'categoryID'),
      tags: tags == null ? const [] : (tags as List).cast<String>(),
      notes: favoriteString(json, 'notes'),
      createdAt: favoriteTimestamp(json, 'createTime'),
      updatedAt: favoriteTimestamp(json, 'updateTime'),
      ex: favoriteString(json, 'ex'),
      retentionBlocked: retentionDenied || (blocked as bool? ?? false),
      sourcePrivacy: projectPrivacy?.call(
        favoriteString(json, 'sourceID'),
        favoriteString(json, 'conversationID'),
        favoriteString(json, 'content'),
      ),
    );
  }
  Map<String, Object?> toJson() => {
    'favoriteID': id,
    'userID': ownerId,
    'favoriteType': kind.code,
    'sourceID': sourceId,
    'conversationID': conversationId,
    'title': title,
    'content': content,
    'thumbnailURL': thumbnail,
    'categoryID': categoryId,
    'tags': tags,
    'notes': notes,
    'createTime': createdAt,
    'updateTime': updatedAt,
    'ex': ex,
    if (retentionBlocked) 'retentionBlocked': true,
  };
}

final class FavoriteCategory {
  const FavoriteCategory({
    required this.id,
    required this.name,
    this.color = '#2563EB',
    this.order = 0,
    this.count = 0,
    this.ex = '',
  });
  final String id, name, color, ex;
  final int order, count;
  bool get isDefault => id == 'default';
  factory FavoriteCategory.fromJson(Object? value, String owner) {
    final map = favoriteMap(value);
    final id = favoriteString(map, 'categoryID');
    final user = favoriteString(map, 'userID');
    if (id.isEmpty || (user.isNotEmpty && user != owner)) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return FavoriteCategory(
      id: id,
      name: favoriteString(map, 'categoryName'),
      color: favoriteString(map, 'categoryColor'),
      order: favoriteInt(map, 'sortOrder'),
      count: favoriteInt(map, 'favoriteCount'),
      ex: favoriteString(map, 'ex'),
    );
  }
  Map<String, Object?> toJson() => {
    'categoryID': id,
    'categoryName': name,
    'categoryColor': color,
    'sortOrder': order,
    'favoriteCount': count,
    'ex': ex,
  };
}

final class FavoritePageData {
  FavoritePageData(Iterable<FavoriteItem> items, this.total, this.hasMore)
    : items = List.unmodifiable(items);
  final List<FavoriteItem> items;
  final int total;
  final bool hasMore;
}

final class FavoriteQuery {
  const FavoriteQuery({
    this.keyword = '',
    this.type = 0,
    this.categoryId,
    this.tags = const [],
  });
  final String keyword;
  final int type;
  final String? categoryId;
  final List<String> tags;
  bool get filtered =>
      keyword.isNotEmpty || type != 0 || categoryId != null || tags.isNotEmpty;
  Map<String, Object?> toJson() => {
    'favoriteType': type,
    if (keyword.trim().isNotEmpty) 'keyword': keyword.trim(),
    if (categoryId != null) 'categoryID': categoryId,
    if (tags.isNotEmpty) 'tags': List<String>.of(tags),
  };
}

final class FavoriteStats {
  FavoriteStats(
    Map<String, Object?> values, {
    Iterable<FavoriteCategory> categories = const [],
  }) : values = Map.unmodifiable(values),
       categories = List.unmodifiable(categories);
  final Map<String, Object?> values;
  final List<FavoriteCategory> categories;
  int get total => favoriteInt(values, 'totalCount');
  int count(String key) => favoriteInt(values, key);
  int typeCount(FavoriteKind kind) => count('${kind.name}Count');
}

/// Never interprets exported message local paths or HTML as runnable content.
final class FavoriteContent {
  const FavoriteContent(
    this.summary, {
    this.media,
    this.name = '',
    this.message,
    this.privacy = const MessagePrivacy(),
  });
  final String summary, name;
  final Uri? media;
  final Map<String, Object?>? message;
  final MessagePrivacy privacy;
  bool get canForward =>
      message?['clientMsgID'] is String &&
      (message!['clientMsgID'] as String).isNotEmpty &&
      privacy.allowsRetention;
  static FavoriteContent decode(
    String raw,
    FavoriteKind kind, {
    MessagePrivacy? privacy,
  }) {
    Object? decoded;
    try {
      decoded = jsonDecode(raw);
    } catch (_) {
      return FavoriteContent(
        raw,
        media: kind == FavoriteKind.message ? null : favoriteMedia(raw),
        privacy: privacy ?? const MessagePrivacy(),
      );
    }
    if (decoded is! Map) {
      return FavoriteContent(raw, privacy: privacy ?? const MessagePrivacy());
    }
    final map = Map<String, Object?>.from(decoded);
    String field(Object? value, String key) =>
        value is Map && value[key] is String ? value[key] as String : '';
    final text = field(map['textElem'], 'content');
    final atText = field(map['atTextElem'], 'text');
    final quote = field(map['quoteElem'], 'text');
    final file = map['fileElem'];
    final name = field(file, 'fileName');
    final urls = [
      field(file, 'sourceUrl'),
      field(map['soundElem'], 'sourceUrl'),
      field(map['videoElem'], 'videoUrl'),
      if (map['pictureElem'] is Map)
        field((map['pictureElem'] as Map)['sourcePicture'], 'url'),
      if (map['url'] is String) map['url'] as String,
    ];
    final media = urls.map(favoriteMedia).whereType<Uri>().firstOrNull;
    final summary =
        [
          text,
          atText,
          quote,
          name,
          if (map['text'] is String) map['text'] as String,
          if (map['title'] is String) map['title'] as String,
        ].where((value) => value.isNotEmpty).firstOrNull ??
        kind.label;
    return FavoriteContent(
      summary,
      media: media,
      name: name,
      message: Map.unmodifiable(map),
      privacy: MessagePrivacy.combined([inspectMessagePrivacy(map), ?privacy]),
    );
  }
}

Uri? favoriteMedia(String? raw) {
  final uri = raw == null ? null : Uri.tryParse(raw);
  return uri != null &&
          uri.scheme == 'https' &&
          uri.host.isNotEmpty &&
          uri.userInfo.isEmpty
      ? uri
      : null;
}

Map<String, Object?> favoriteMap(Object? value) {
  if (value is! Map || value.keys.any((key) => key is! String)) {
    throw const AppFailure(FailureKind.invalidResponse);
  }
  return Map<String, Object?>.from(value);
}

String favoriteString(Map<String, Object?> map, String key) {
  final value = map[key];
  if (value != null && value is! String) {
    throw const AppFailure(FailureKind.invalidResponse);
  }
  return value as String? ?? '';
}

int favoriteTimestamp(Map<String, Object?> map, String key) {
  final value = favoriteInt(map, key);
  if (value < 0 || value > 8640000000000000) {
    throw const AppFailure(FailureKind.invalidResponse);
  }
  return value;
}

int favoriteInt(Map<String, Object?> map, String key) {
  final value = map[key];
  if (value != null && value is! int) {
    throw const AppFailure(FailureKind.invalidResponse);
  }
  return value as int? ?? 0;
}
