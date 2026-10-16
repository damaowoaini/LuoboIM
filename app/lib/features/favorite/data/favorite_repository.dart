import 'dart:convert';

import '../../../core/error/app_failure.dart';
import '../../../core/network/tenant_api.dart';
import '../../../core/privacy/message_privacy.dart';
import '../domain/favorite.dart';

final class FavoriteRepository {
  FavoriteRepository(this.api, {MessagePrivacyLedger? privacyLedger})
    : _privacyLedger =
          privacyLedger?.isBoundTo(
                api.access.credentials.accountKey,
                api.access.generation,
              ) ==
              true
          ? privacyLedger
          : null;
  final MessagePrivacyLedger? _privacyLedger;
  Stream<void>? get privacyChanges => _privacyLedger?.changes;
  final Map<String, MessagePrivacy> _retention = {};
  MessagePrivacy? _sourcePrivacy(
    String sourceId,
    String conversationId,
    String content,
  ) {
    final ledger = _privacyLedger;
    if (ledger == null) return null;
    final policies = <MessagePrivacy>[
      if (sourceId.isNotEmpty) ledger.retentionReference(sourceId),
      if (sourceId.isNotEmpty) ledger.favoriteRetentionReference('', sourceId),
    ];
    var nodes = 0, hasMessageIdentity = false;
    void inspect(Object? value, int depth) {
      if (value is! Map || depth > 32 || ++nodes > 4096) return;
      final id = value['clientMsgID'];
      if (id is String && id.isNotEmpty && id.length <= 512) {
        hasMessageIdentity = true;
        policies.add(ledger.retentionReference(id));
        policies.add(ledger.favoriteRetentionReference('', id));
      }
      for (final child in actualMessageChildren(value)) {
        inspect(child, depth + 1);
      }
    }

    if (content.length <= 128 * 1024) {
      try {
        final decoded = jsonDecode(content);
        inspect(decoded, 0);
        // Learn actual protocol descendants from a legacy snapshot as well as
        // live chat, retaining only IDs/flags/expiry in the shared ledger.
        if (hasMessageIdentity) {
          ledger.observe(conversationId, decoded, now: DateTime.now());
        }
      } catch (_) {}
    }
    return MessagePrivacy.combined(policies);
  }

  void checkContent(FavoriteItem item) {
    check();
    if (!item.canUseContent) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
  }

  bool _denied(String id) =>
      _blocked.contains(id) ||
      _retention[id]?.allowsRetention == false ||
      _privacyLedger?.favoriteRetentionReference(id, '').allowsRetention ==
          false;
  final TenantApi api;
  final _blocked = <String>{};
  FavoriteItem _item(Object? value) {
    final parsed = FavoriteItem.fromJson(value, owner);
    if (!parsed.canUseContent) {
      _blocked.add(parsed.id);
      _privacyLedger?.denyFavoriteRetention(parsed.id, parsed.sourceId);
    }
    final id = parsed.id, sourceId = parsed.sourceId;
    final item = FavoriteItem.fromJson(
      value,
      owner,
      retentionDenied: _blocked.contains(id),
      projectPrivacy: (source, conversation, content) =>
          MessagePrivacy.combined([
            ?_sourcePrivacy(source, conversation, content),
            ?_privacyLedger?.favoriteRetentionReference(id, sourceId),
          ]),
    );
    _retention[item.id] = item.decoded.privacy;
    return item;
  }

  String get owner => api.access.credentials.userId;
  bool get isCurrent => api.access.isCurrent;
  void check() => api.access.check();
  Future<Map<String, Object?>> _post(
    String operation,
    Map<String, Object?> data,
  ) async {
    check();
    const limits = {
      'sourceID': 512,
      'conversationID': 256,
      'title': 1024,
      'content': 128 * 1024,
      'notes': 8192,
      'ex': 8192,
      'keyword': 256,
      'categoryName': 128,
      'categoryID': 128,
      'favoriteID': 128,
      'targetCategoryID': 128,
    };
    for (final entry in limits.entries) {
      final value = data[entry.key];
      if (value is String && utf8.encode(value).length > entry.value) {
        throw const AppFailure(FailureKind.invalidInput);
      }
    }
    for (final key in ['tags', 'targetTags']) {
      final values = data[key];
      if (values is List &&
          (values.length > 20 ||
              values.any(
                (tag) =>
                    tag is! String ||
                    tag.trim().isEmpty ||
                    utf8.encode(tag).length > 96,
              ))) {
        throw const AppFailure(FailureKind.invalidInput);
      }
    }
    if (data.containsKey('categoryName') &&
        (data['categoryName'] as String).trim().isEmpty) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final result = await api.im.post('favorite/$operation', {
      ...data,
      'userID': owner,
    });
    check();
    final value = favoriteMap(result);
    if (['remove', 'batch_operate', 'category/delete'].contains(operation) &&
        value['success'] != true) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return value;
  }

  Future<FavoritePageData> list({
    FavoriteQuery query = const FavoriteQuery(),
    int page = 1,
    int count = 30,
  }) async {
    if (page < 1 ||
        page > 100000 ||
        count < 1 ||
        count > 100 ||
        query.type < 0 ||
        query.type > 6) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final data = await _post(query.keyword.trim().isEmpty ? 'list' : 'search', {
      ...query.toJson(),
      'pagination': {'pageNumber': page, 'showNumber': count},
    });
    if (data['favorites'] is! List ||
        data['total'] is! int ||
        data['hasMore'] is! bool) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final items = (data['favorites'] as List).map(_item).toList();
    final total = data['total'] as int;
    if (total < 0 ||
        items.length > count ||
        (data['hasMore'] == true && items.isEmpty) ||
        items.map((item) => item.id).toSet().length != items.length) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return FavoritePageData(items, total, data['hasMore'] as bool);
  }

  Future<FavoriteItem> info(String id) async {
    final item = _item(
      (await _post('info', {'favoriteID': id}))['favoriteInfo'],
    );
    if (item.id != id) throw const AppFailure(FailureKind.invalidResponse);
    return item;
  }

  Future<FavoriteItem?> findSource(
    String sourceId,
    String conversationId,
    FavoriteKind kind,
  ) async {
    final result = await _post('list', {
      'sourceID': sourceId,
      'conversationID': conversationId,
      'favoriteType': kind.code,
      'pagination': {'pageNumber': 1, 'showNumber': 1},
    });
    if (result['favorites'] is! List) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final values = result['favorites'] as List;
    if (values.isEmpty) return null;
    if (values.length != 1) throw const AppFailure(FailureKind.invalidResponse);
    final item = _item(values.single);
    if (item.sourceId != sourceId ||
        item.conversationId != conversationId ||
        item.kind != kind) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return item;
  }

  Future<FavoriteItem> add({
    required FavoriteKind kind,
    required String sourceId,
    required String content,
    String conversationId = '',
    String title = '',
    String thumbnail = '',
    String categoryId = 'default',
    List<String> tags = const [],
    String notes = '',
    String ex = '',
  }) async {
    if (sourceId.isEmpty || utf8.encode(content).length > 128 * 1024) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    if (!FavoriteContent.decode(
      content,
      kind,
      privacy: _sourcePrivacy(sourceId, conversationId, content),
    ).privacy.allowsRetention) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    return _item(
      (await _post('add', {
        'favoriteType': kind.code,
        'sourceID': sourceId,
        'content': content,
        'conversationID': conversationId,
        'title': title,
        'thumbnailURL': thumbnail,
        'categoryID': categoryId,
        'tags': tags,
        'notes': notes,
        'ex': ex,
      }))['favoriteInfo'],
    );
  }

  Future<FavoriteItem> update(
    String id, {
    String? title,
    String? categoryId,
    List<String>? tags,
    String? notes,
  }) async {
    if (_denied(id)) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    return _item(
      (await _post('update', {
        'favoriteID': id,
        'title': ?title,
        'categoryID': ?categoryId,
        'tags': ?tags,
        'notes': ?notes,
      }))['favoriteInfo'],
    );
  }

  List<String> _ids(Iterable<String> ids) {
    final result = ids.toSet().toList();
    if (result.isEmpty ||
        result.length > 100 ||
        result.any((id) => id.isEmpty)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    return result;
  }

  Future<void> remove(Iterable<String> ids) async {
    await _post('remove', {'favoriteIDs': _ids(ids)});
  }

  Future<void> batch(
    Iterable<String> ids, {
    required int operation,
    String? categoryId,
    List<String> tags = const [],
  }) async {
    if (operation < 1 || operation > 4) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    if (operation != 4 && ids.any(_denied)) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    await _post('batch_operate', {
      'favoriteIDs': _ids(ids),
      'operation': operation,
      'targetCategoryID': ?categoryId,
      'targetTags': tags,
    });
  }

  Future<List<FavoriteCategory>> categories() async {
    final data = await _post('category/list', {});
    if (data['categories'] is! List) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return (data['categories'] as List)
        .map((value) => FavoriteCategory.fromJson(value, owner))
        .toList();
  }

  Future<FavoriteCategory> createCategory(
    String name, {
    String color = '#2563EB',
    int? order,
  }) async => FavoriteCategory.fromJson(
    (await _post('category/create', {
      'categoryName': name.trim(),
      'categoryColor': color,
      'sortOrder': ?order,
    }))['categoryInfo'],
    owner,
  );
  Future<FavoriteCategory> updateCategory(
    String id, {
    String? name,
    String? color,
    int? order,
  }) async {
    if (id == 'default') throw const AppFailure(FailureKind.invalidInput);
    return FavoriteCategory.fromJson(
      (await _post('category/update', {
        'categoryID': id,
        if (name != null) 'categoryName': name.trim(),
        'categoryColor': ?color,
        'sortOrder': ?order,
      }))['categoryInfo'],
      owner,
    );
  }

  Future<void> deleteCategory(String id, {required bool moveToDefault}) async {
    if (id == 'default') throw const AppFailure(FailureKind.invalidInput);
    await _post('category/delete', {
      'categoryID': id,
      'moveToDefault': moveToDefault,
    });
  }

  Future<FavoriteStats> stats() async {
    final values = await _post('stats', {});
    for (final key in [
      'totalCount',
      'messageCount',
      'fileCount',
      'imageCount',
      'videoCount',
      'audioCount',
      'linkCount',
      'categoryCount',
      'todayAddedCount',
      'weekAddedCount',
      'monthAddedCount',
      'lastFavoriteTime',
    ]) {
      if (favoriteInt(values, key) < 0) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
    }
    favoriteTimestamp(values, 'lastFavoriteTime');
    final categoryValues = values['categoriesWithCount'];
    if (categoryValues != null && categoryValues is! List) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final categories = (categoryValues as List? ?? const [])
        .map((value) => FavoriteCategory.fromJson(value, owner))
        .toList();
    if (categories.any((category) => category.count < 0) ||
        categories.map((category) => category.id).toSet().length !=
            categories.length) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return FavoriteStats(values, categories: categories);
  }
}
