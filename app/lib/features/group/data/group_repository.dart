import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../core/tenant/session_access.dart';
import '../../chat/data/message_mapper.dart' show secureMediaUri;
import '../../contacts/data/contacts_repository.dart' show mapGroup;
import '../domain/group.dart';

enum GroupRemovalState { acknowledged, rejected, unconfirmed, notAttempted }

final class GroupRemovalItem {
  const GroupRemovalItem(this.userId, this.state, [this.failure]);
  final String userId;
  final GroupRemovalState state;
  final AppFailure? failure;
}

final class GroupRemovalReport {
  GroupRemovalReport(List<GroupRemovalItem> items)
    : items = List.unmodifiable(items);
  final List<GroupRemovalItem> items;
  bool get hasUnconfirmed =>
      items.any((item) => item.state == GroupRemovalState.unconfirmed);
}

final class GroupRepository {
  GroupRepository(this.gateway, this.access);
  final OpenImGateway gateway;
  final SessionAccess access;
  Future<T> _sdk<T>(Future<T> Function(IMManager manager) operation) async {
    access.check();
    final value = await gateway.runForSession(access.generation, operation);
    access.check();
    return value;
  }

  Future<GroupProfile> profile(String id) => _sdk((manager) async {
    final groups = await manager.groupManager.getGroupsInfo(groupIDList: [id]);
    access.check();
    final group = groups.where((group) => group.groupID == id).firstOrNull;
    if (group == null) throw const AppFailure(FailureKind.invalidResponse);
    final joined = await manager.groupManager.isJoinedGroup(groupID: id);
    access.check();
    GroupMember? me;
    if (joined) {
      final users = await manager.groupManager.getGroupMembersInfo(
        groupID: id,
        userIDList: [access.credentials.userId],
      );
      access.check();
      final user = users
          .where((user) => user.userID == access.credentials.userId)
          .firstOrNull;
      if (user != null) me = mapGroupMember(user);
    }
    return GroupProfile(
      summary: mapGroup(group),
      joined: joined,
      me: me,
      announcement: group.notification ?? '',
      introduction: group.introduction ?? '',
      verification: group.needVerification ?? 0,
      hideMemberProfiles: group.lookMemberInfo == 1,
      preventMemberFriends: group.applyMemberFriend == 1,
    );
  });

  Future<List<GroupMember>> members(
    String id, {
    String query = '',
    int offset = 0,
    int count = 40,
  }) => _sdk((manager) async {
    if (offset < 0 || count < 1 || count > 100) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final list = query.trim().isEmpty
        ? await manager.groupManager.getGroupMemberList(
            groupID: id,
            offset: offset,
            count: count,
          )
        : await manager.groupManager.searchGroupMembers(
            groupID: id,
            keywordList: [query.trim()],
            isSearchMemberNickname: true,
            isSearchUserID: true,
            offset: offset,
            count: count,
          );
    return list
        .where((member) => (member.userID ?? '').isNotEmpty)
        .map(mapGroupMember)
        .toList();
  });

  Future<GroupProfile> _manager(String id, {bool ownerOnly = false}) async {
    final current = await profile(id);
    if (!current.canManage || (ownerOnly && current.me?.owner != true)) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    return current;
  }

  Future<void> update(
    String id, {
    String? name,
    String? announcement,
    String? avatar,
    bool Function()? isCurrent,
  }) async {
    void checkCurrent() {
      access.check();
      if (isCurrent?.call() == false) {
        throw const AppFailure(FailureKind.cancelled);
      }
    }

    checkCurrent();
    final profile = await _manager(id);
    checkCurrent();
    if (avatar != null && profile.summary.status == 2) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    if (name != null && (name.trim().isEmpty || name.length > 128)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    if (announcement != null && announcement.length > 10000) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    if (avatar != null && secureMediaUri(avatar) == null) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    await _sdk((manager) async {
      // The role lookup above is asynchronous. Recheck the original view at
      // the actual native dispatch boundary, never acquire a new page's lease.
      checkCurrent();
      await manager.groupManager.setGroupInfo(
        GroupInfo(
          groupID: id,
          groupName: name?.trim(),
          notification: announcement,
          faceURL: avatar,
        ),
      );
    });
  }

  Future<void> nickname(String id, String value) => _sdk((manager) async {
    if (value.length > 128) throw const AppFailure(FailureKind.invalidInput);
    await manager.groupManager.setGroupMemberNickname(
      groupID: id,
      userID: access.credentials.userId,
      groupNickname: value.trim(),
    );
  });

  Future<void> setMute(String id, bool enabled) async {
    await _manager(id);
    await _sdk((manager) async {
      await manager.groupManager.changeGroupMute(groupID: id, mute: enabled);
    });
  }

  Future<void> setHideProfiles(String id, bool enabled) async {
    await _manager(id);
    await _sdk((manager) async {
      await manager.groupManager.setGroupLookMemberInfo(
        groupID: id,
        status: enabled ? 1 : 0,
      );
    });
  }

  Future<void> setPreventFriends(String id, bool enabled) async {
    await _manager(id);
    await _sdk((manager) async {
      await manager.groupManager.setGroupApplyMemberFriend(
        groupID: id,
        status: enabled ? 1 : 0,
      );
    });
  }

  Future<void> verification(String id, int value) async {
    if (![0, 1, 2].contains(value)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    await _manager(id);
    await _sdk((manager) async {
      await manager.groupManager.setGroupVerification(
        groupID: id,
        needVerification: value,
      );
    });
  }

  Future<void> transfer(String id, String userId) async {
    await _manager(id, ownerOnly: true);
    if (userId == access.credentials.userId) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    await _sdk((manager) async {
      final users = await manager.groupManager.getGroupMembersInfo(
        groupID: id,
        userIDList: [userId],
      );
      access.check();
      if (!users.any(
        (member) => member.userID == userId && member.roleLevel != 100,
      )) {
        throw const AppFailure(FailureKind.invalidInput);
      }
      await manager.groupManager.transferGroupOwner(
        groupID: id,
        userID: userId,
      );
    });
  }

  Future<void> role(String id, String userId, bool admin) async {
    await _manager(id, ownerOnly: true);
    if (userId == access.credentials.userId) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    await _sdk((manager) async {
      await manager.groupManager.setGroupMemberRoleLevel(
        groupID: id,
        userID: userId,
        roleLevel: admin ? 60 : 20,
      );
    });
  }

  Future<void> muteMember(String id, String userId, int seconds) async {
    final current = await _manager(id);
    if (seconds < 0 || seconds > 30 * 24 * 60 * 60) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    await _sdk((manager) async {
      final members = await manager.groupManager.getGroupMembersInfo(
        groupID: id,
        userIDList: [userId],
      );
      access.check();
      if (members.length != 1 ||
          !current.me!.canModerate(mapGroupMember(members.single))) {
        throw const AppFailure(FailureKind.permissionDenied);
      }
      await manager.groupManager.changeGroupMemberMute(
        groupID: id,
        userID: userId,
        seconds: seconds,
      );
    });
  }

  Future<void> remove(String id, List<String> userIds) async {
    final current = await _manager(id);
    final ids = userIds.toSet().toList();
    if (ids.isEmpty) throw const AppFailure(FailureKind.invalidInput);
    await _sdk((manager) async {
      final members = await manager.groupManager.getGroupMembersInfo(
        groupID: id,
        userIDList: ids,
      );
      access.check();
      if (members.length != ids.length ||
          members.any(
            (member) => !current.me!.canModerate(mapGroupMember(member)),
          )) {
        throw const AppFailure(FailureKind.permissionDenied);
      }
      await manager.groupManager.kickGroupMember(groupID: id, userIDList: ids);
    });
  }

  /// One accepted native intent per user, in the frozen selection order.
  /// Never replay a dispatched operation after a missing/ambiguous ACK.
  Future<GroupRemovalReport> removeSelected(
    String id,
    List<String> userIds, {
    required bool Function() isCurrent,
    void Function(GroupRemovalItem item)? onResult,
  }) async {
    final ids = List<String>.unmodifiable(userIds);
    if (ids.isEmpty ||
        ids.length > 999 ||
        ids.any((uid) => uid.isEmpty) ||
        ids.toSet().length != ids.length) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final items = <GroupRemovalItem>[];
    void record(GroupRemovalItem item) {
      items.add(item);
      if (isCurrent() && access.isCurrent) onResult?.call(item);
    }

    bool stopped = false;
    for (final userId in ids) {
      if (stopped || !isCurrent() || !access.isCurrent) {
        record(GroupRemovalItem(userId, GroupRemovalState.notAttempted));
        continue;
      }
      var dispatched = false;
      try {
        final group = await _manager(id);
        access.check();
        if (!isCurrent()) throw const AppFailure(FailureKind.cancelled);
        if (group.summary.status == 2) {
          throw const AppFailure(FailureKind.permissionDenied);
        }
        await _sdk((manager) async {
          // Read both roles together; the owner can remove admins, an admin
          // only ordinary members. No send/mute permission is consulted.
          final actor = access.credentials.userId;
          if (actor == userId) {
            throw const AppFailure(FailureKind.permissionDenied);
          }
          final members = await manager.groupManager.getGroupMembersInfo(
            groupID: id,
            userIDList: [actor, userId],
          );
          access.check();
          if (!isCurrent()) throw const AppFailure(FailureKind.cancelled);
          if (members.length != 2 ||
              members.any((member) => member.groupID != id) ||
              members.map((member) => member.userID).toSet().length != 2) {
            throw const AppFailure(FailureKind.invalidResponse);
          }
          final me = members
              .where((member) => member.userID == actor)
              .firstOrNull;
          final target = members
              .where((member) => member.userID == userId)
              .firstOrNull;
          if (me == null ||
              target == null ||
              !mapGroupMember(me).canModerate(mapGroupMember(target))) {
            throw const AppFailure(FailureKind.permissionDenied);
          }
          if (!isCurrent()) throw const AppFailure(FailureKind.cancelled);
          dispatched = true;
          final result = await manager.groupManager.kickGroupMember(
            groupID: id,
            userIDList: [userId],
          );
          // Current server response is empty and the public core returns only
          // an error. Do not invent success semantics for unknown enterprise
          // per-user result values, duplicates, or partial DTOs.
          if (result.isNotEmpty) {
            throw const AppFailure(FailureKind.invalidResponse);
          }
        });
        record(GroupRemovalItem(userId, GroupRemovalState.acknowledged));
      } catch (error) {
        final failure = error is AppFailure
            ? error
            : const AppFailure(FailureKind.unknown);
        if (dispatched) {
          // Even permission errors can follow a committed delete when the
          // post-delete callback rejects. A code alone is not rollback proof.
          record(
            GroupRemovalItem(userId, GroupRemovalState.unconfirmed, failure),
          );
          stopped = true;
        } else if (failure.kind == FailureKind.permissionDenied &&
            isCurrent() &&
            access.isCurrent) {
          record(GroupRemovalItem(userId, GroupRemovalState.rejected, failure));
        } else {
          record(
            GroupRemovalItem(userId, GroupRemovalState.notAttempted, failure),
          );
          stopped = true;
        }
      }
    }
    return GroupRemovalReport(items);
  }

  Future<void> invite(String id, List<String> userIds) => _sdk((manager) async {
    final ids = userIds
        .where((id) => id.isNotEmpty && id != access.credentials.userId)
        .toSet()
        .toList();
    if (ids.isEmpty) throw const AppFailure(FailureKind.invalidInput);
    await manager.groupManager.inviteUserToGroup(groupID: id, userIDList: ids);
  });
  Future<void> join(String id, String reason, {int source = 3}) =>
      _sdk((manager) async {
        if (![2, 3, 4].contains(source)) {
          throw const AppFailure(FailureKind.invalidInput);
        }
        await manager.groupManager.joinGroup(
          groupID: id,
          reason: reason,
          joinSource: source,
        );
      });
  Future<void> leave(String id) async {
    final current = await profile(id);
    if (!current.joined || current.me?.owner == true) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    await _sdk((manager) async {
      await manager.groupManager.quitGroup(groupID: id);
    });
  }

  Future<void> dismiss(String id) async {
    await _manager(id, ownerOnly: true);
    await _sdk((manager) async {
      await manager.groupManager.dismissGroup(groupID: id);
    });
  }

  Future<String> conversation(String id) async {
    final current = await profile(id);
    if (!current.joined || current.summary.status == 2) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    return _sdk(
      (manager) async => (await manager.conversationManager.getOneConversation(
        sourceID: id,
        sessionType: current.summary.sessionType,
      )).conversationID,
    );
  }

  Future<String> create(String name, List<String> userIds, {String? avatar}) =>
      _sdk((manager) async {
        final ids = userIds
            .where((id) => id.isNotEmpty && id != access.credentials.userId)
            .toSet()
            .toList();
        if (ids.length < 2 || name.trim().isEmpty || name.length > 128) {
          throw const AppFailure(FailureKind.invalidInput);
        }
        final group = await manager.groupManager.createGroup(
          groupInfo: GroupInfo(
            groupID: '',
            groupName: name.trim(),
            faceURL: avatar,
            groupType: GroupType.work,
          ),
          memberUserIDs: ids,
        );
        access.check();
        final conversation = await manager.conversationManager
            .getOneConversation(
              sourceID: group.groupID,
              sessionType: group.sessionType,
            );
        return conversation.conversationID;
      });
}

GroupMember mapGroupMember(GroupMembersInfo member) => GroupMember(
  id: member.userID ?? '',
  name: member.nickname?.isNotEmpty == true
      ? member.nickname!
      : member.userID ?? '',
  role: member.roleLevel ?? 20,
  mutedUntil: member.muteEndTime ?? 0,
  avatar: secureMediaUri(member.faceURL),
);
