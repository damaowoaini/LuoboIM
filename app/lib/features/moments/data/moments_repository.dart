import 'dart:convert';

import '../../../core/error/app_failure.dart';
import '../../../core/network/tenant_api.dart';
import '../domain/moment.dart';

final class MomentsRepository {
  MomentsRepository(this.api);
  final TenantApi api;
  String get owner => api.access.credentials.userId;
  bool get isCurrent => api.access.isCurrent;
  void check() => api.access.check();

  Future<Map<String, Object?>> _post(
    String operation,
    Map<String, Object?> body,
  ) async {
    check();
    final result = await api.chat.post('office/work_moment/$operation', body);
    check();
    return result == null ? const {} : momentMap(result);
  }

  Map<String, Object?> _pagination(int page, int count) {
    if (page < 1 || page > 100000 || count < 1 || count > 100) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    return {'pageNumber': page, 'showNumber': count};
  }

  void _id(String value) {
    if (value.isEmpty || utf8.encode(value).length > 256) {
      throw const AppFailure(FailureKind.invalidInput);
    }
  }

  Future<MomentPage> list({
    String? author,
    int page = 1,
    int count = 20,
  }) async {
    if (author != null) _id(author);
    final body = await _post(author == null ? 'find/recv' : 'find/send', {
      'userID': author ?? owner,
      'pagination': _pagination(page, count),
      // Friend membership is resolved by the server, never supplied by a client.
    });
    final items = momentList(body['workMoments'], Moment.fromJson);
    if (items.length > count ||
        items.map((item) => item.id).toSet().length != items.length ||
        (author != null && items.any((item) => item.author.id != author))) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return MomentPage(items, items.length == count);
  }

  Future<Moment> detail(String id) async {
    _id(id);
    final item = Moment.fromJson(
      (await _post('get', {'workMomentID': id}))['workMoment'],
    );
    if (item.id != id) throw const AppFailure(FailureKind.invalidResponse);
    return item;
  }

  Future<Moment> create(MomentDraft draft) async {
    check();
    if ((draft.text.trim().isEmpty && draft.media.isEmpty) ||
        utf8.encode(draft.text).length > 65536 ||
        ![0, 1].contains(draft.mediaType) ||
        draft.media.length > (draft.mediaType == 1 ? 1 : 9) ||
        draft.media.any(
          (item) =>
              item.uri == null ||
              (item.thumbnail.isNotEmpty && item.thumbnailUri == null),
        ) ||
        ((draft.visibility == MomentVisibility.included ||
                draft.visibility == MomentVisibility.excluded) &&
            draft.audience.isEmpty &&
            draft.groups.isEmpty)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    for (final ids in [draft.audience, draft.groups, draft.mentions]) {
      if (ids.length > 1000) throw const AppFailure(FailureKind.invalidInput);
      for (final id in ids) {
        _id(id);
      }
    }
    final result = await _post('add', {
      'userID': owner,
      'content': {
        'text': draft.text,
        'type': draft.mediaType,
        'metas': draft.media.map((item) => item.toJson()).toList(),
      },
      'permission': draft.visibility.code,
      'permissionUserIDs': draft.audience.toSet().toList(),
      'permissionGroupIDs': draft.groups.toSet().toList(),
      'atUserIDs': draft.mentions.toSet().toList(),
    });
    final moment = Moment.fromJson(result['workMoment']);
    if (moment.author.id != owner) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return moment;
  }

  Future<void> delete(Moment moment) async {
    check();
    if (moment.author.id != owner) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    _id(moment.id);
    await _post('del', {'workMomentID': moment.id, 'userID': owner});
  }

  Future<Moment> like(String id, bool enabled) async {
    _id(id);
    await _post('like', {'workMomentID': id, 'userID': owner, 'like': enabled});
    // Refresh authoritative state also for idempotent legacy responses.
    return detail(id);
  }

  Future<Moment> comment(String id, String text, {String replyTo = ''}) async {
    _id(id);
    if (text.trim().isEmpty || utf8.encode(text).length > 8192) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    if (replyTo.isNotEmpty) _id(replyTo);
    await _post('comment/add', {
      'workMomentID': id,
      'userID': owner,
      'replyUserID': replyTo,
      'content': text,
    });
    return detail(id);
  }

  Future<Moment> deleteComment(Moment moment, MomentComment comment) async {
    check();
    if (!moment.canDeleteComment(owner, comment) ||
        !moment.comments.any((item) => item.id == comment.id)) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    _id(moment.id);
    _id(comment.id);
    await _post('comment/del', {
      'workMomentID': moment.id,
      'commentID': comment.id,
    });
    return detail(moment.id);
  }

  Future<List<MomentInteraction>> interactions({
    int page = 1,
    int count = 20,
  }) async {
    final result = await _post('logs', {
      'userID': owner,
      'pagination': _pagination(page, count),
    });
    final values = momentList(
      result['workMoments'],
      MomentInteraction.fromJson,
    );
    if (values.length > count) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return values;
  }

  Future<int> unreadCount() async {
    final result = await _post('unread/count', {'userID': owner});
    final count = momentInt(result['total']);
    if (count < 0) throw const AppFailure(FailureKind.invalidResponse);
    return count;
  }

  /// Legacy protocol: 1=count, 2=interaction list, 3=both.
  Future<void> clearUnread({int type = 1}) async {
    if (![1, 2, 3].contains(type)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    await _post('unread/clear', {'userID': owner, 'type': type});
  }
}
