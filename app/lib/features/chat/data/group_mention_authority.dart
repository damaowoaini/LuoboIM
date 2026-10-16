import 'dart:async';

import '../../../core/error/app_failure.dart';
import '../../../core/network/tenant_api.dart';
import '../domain/chat_mention.dart';

/// These existing IM endpoints read current server membership, unlike the
/// SDK's local group tables. This supplements, never replaces, server send ACL.
final class GroupMentionAuthority {
  const GroupMentionAuthority(this.api);
  final TenantApi api;
  Future<List<MentionIdentity>> verify({
    required String groupId,
    required List<String> ids,
    required void Function() check,
    required DateTime Function() now,
  }) async {
    final watch = Stopwatch()..start();
    void current() {
      check();
      api.access.check();
      if (watch.elapsed >= const Duration(seconds: 8)) {
        throw const AppFailure(FailureKind.timeout);
      }
    }

    Future<Map> post(String path, Map<String, Object?> data) async {
      current();
      try {
        final value = await api.im
            .post(path, data)
            .timeout(const Duration(seconds: 4));
        current();
        if (value is! Map) throw const AppFailure(FailureKind.invalidResponse);
        return value;
      } on TimeoutException {
        throw const AppFailure(FailureKind.timeout);
      }
    }

    final groups = await post('group/get_groups_info', {
      'groupIDs': [groupId],
    });
    final list = groups['groupInfos'];
    if (list is! List || list.length != 1 || list.single is! Map) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final group = list.single as Map;
    if (group['groupID'] != groupId ||
        group['status'] is! int ||
        !const [0, 1, 2, 3].contains(group['status'])) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    if (group['status'] == 1 || group['status'] == 2) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    final actor = api.access.credentials.userId;
    final requiredIds = {actor, ...ids.where((id) => id != mentionAllId)};
    final data = await post('group/get_group_members_info', {
      'groupID': groupId,
      'userIDs': requiredIds.toList(),
    });
    final rawMembers = data['members'];
    if (rawMembers is! List) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final members = <String, Map>{};
    for (final raw in rawMembers) {
      if (raw is! Map ||
          raw['groupID'] != groupId ||
          raw['userID'] is! String ||
          !requiredIds.contains(raw['userID']) ||
          members.containsKey(raw['userID']) ||
          raw['roleLevel'] is! int ||
          !const [20, 60, 100].contains(raw['roleLevel'])) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      members[raw['userID'] as String] = raw;
    }
    if (members.length != requiredIds.length) {
      throw const AppFailure(FailureKind.dataChanged);
    }
    final self = members[actor]!;
    final mute = self['muteEndTime'];
    final muteTime = mute is int
        ? mute
        : mute is String && RegExp(r'^\d+$').hasMatch(mute)
        ? int.tryParse(mute)
        : null;
    if (muteTime == null || muteTime < 0) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final owner = self['roleLevel'] == 100 && group['ownerUserID'] == actor;
    final elevated = self['roleLevel'] == 60 || owner;
    // GroupMemberFullInfo.muteEndTime is UnixMilli. The server exempts the
    // current owner from a personal mute, but not administrators.
    if (!owner && muteTime >= now().millisecondsSinceEpoch ||
        group['status'] == 3 && !elevated ||
        ids.contains(mentionAllId) && !elevated) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    current();
    return ids.map((id) {
      if (id == mentionAllId) return const MentionIdentity(mentionAllId, '所有人');
      final value = members[id]!['nickname'];
      if (value != null && value is! String) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      final name = (value as String? ?? id)
          .replaceAll(RegExp(r'[\r\n\x00-\x1f\x7f]'), ' ')
          .trim();
      return MentionIdentity(
        id,
        name.isEmpty ? id : String.fromCharCodes(name.runes.take(128)),
      );
    }).toList();
  }
}
