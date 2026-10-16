import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../core/network/tenant_api.dart';
import '../../../core/tenant/session_access.dart';
import '../../chat/data/message_mapper.dart' show secureMediaUri;
import '../../group/domain/group.dart';
import '../domain/contact.dart';

final class ContactsRepository {
  ContactsRepository(this.gateway, this.access, this.api);
  final OpenImGateway gateway;
  final SessionAccess access;
  final TenantApi api;

  Future<T> _sdk<T>(Future<T> Function(IMManager manager) operation) async {
    access.check();
    final result = await gateway.runForSession(access.generation, operation);
    access.check();
    return result;
  }

  Future<List<Contact>> friends() => _sdk((manager) async {
    final users = await manager.friendshipManager.getFriendList();
    final contacts = users
        .where((user) => user.blackInfo == null && user.userID.isNotEmpty)
        .map(mapContact)
        .toList();
    contacts.sort(
      (a, b) =>
          a.displayName.toLowerCase().compareTo(b.displayName.toLowerCase()),
    );
    return contacts;
  });

  Future<List<Contact>> blocked() => _sdk(
    (manager) async => (await manager.friendshipManager.getBlacklist())
        .where((user) => (user.userID ?? '').isNotEmpty)
        .map(
          (user) => Contact(
            id: user.userID!,
            name: user.nickname ?? user.userID!,
            avatar: secureMediaUri(user.faceURL),
            blocked: true,
          ),
        )
        .toList(),
  );

  Future<Contact> profile(String id) async {
    final target = _lookupValue(id);
    final contact = await _sdk((manager) async {
      final users = await manager.userManager.getUsersInfo(
        userIDList: [target],
      );
      if (users.length != 1) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      final user = users.single;
      if (user.userID != target ||
          (user.publicInfo != null && user.publicInfo!.userID != target) ||
          (user.friendInfo != null && user.friendInfo!.userID != target) ||
          (user.blackInfo != null && user.blackInfo!.userID != target)) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      return mapContact(user);
    });
    final full = await _businessById(target);
    final policy = full['allowAddFriend'];
    // 0 is a known legacy deny value; absence or an unknown policy means the
    // dependency is unavailable, never permission to show mutation actions.
    if (policy is! int || !const {0, 1, 2}.contains(policy)) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    access.check();
    return Contact(
      id: contact.id,
      name: contact.name,
      remark: contact.remark,
      avatar: contact.avatar,
      friend: contact.friend,
      blocked: contact.blocked,
      canRequestFriend: policy == 1,
    );
  }

  static String _lookupValue(String raw) {
    final value = raw.trim();
    if (value.isEmpty ||
        value.length > 128 ||
        RegExp(r'[\x00-\x20\x7f]').hasMatch(value)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    return value;
  }

  Future<Map> _businessById(String id) async {
    access.check();
    final raw = await api.chat.post('user/find/full', {
      'userIDs': [id],
    });
    access.check();
    if (raw is! Map || raw['users'] is! List) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final users = raw['users'] as List;
    if (users.length != 1 ||
        users.single is! Map ||
        users.single['userID'] != id) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return users.single as Map;
  }

  /// ID and phone are separate namespaces. Never fall back to another lookup
  /// mode or pick the first row of an ambiguous result.
  Future<ContactSearchPage> lookupUser(
    String rawValue,
    ContactLookupMode mode,
  ) async {
    access.check();
    final value = _lookupValue(rawValue);
    final Object? raw;
    if (mode == ContactLookupMode.userId) {
      raw = await api.chat.post('user/find/full', {
        'userIDs': [value],
      });
    } else {
      if (!RegExp(r'^\d{6,32}$').hasMatch(value)) {
        throw const AppFailure(FailureKind.invalidInput);
      }
      raw = await api.chat.post('user/search/full', {
        'keyword': value,
        'pagination': {'pageNumber': 1, 'showNumber': 2},
      });
    }
    access.check();
    if (raw is! Map || raw['users'] is! List) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final rows = raw['users'] as List;
    final total = mode == ContactLookupMode.userId ? rows.length : raw['total'];
    if (total == 0 && rows.isEmpty) return const ContactSearchPage([], 0);
    if (total is! int ||
        total != 1 ||
        rows.length != 1 ||
        rows.single is! Map) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final user = rows.single as Map;
    final id = user['userID'];
    if (id is! String ||
        _lookupValue(id) != id ||
        (mode == ContactLookupMode.userId && id != value)) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return ContactSearchPage([
      Contact(
        id: id,
        name: user['nickname'] is String ? user['nickname'] as String : id,
        avatar: secureMediaUri(
          user['faceURL'] is String ? user['faceURL'] as String : null,
        ),
        canRequestFriend: user['allowAddFriend'] == 1,
      ),
    ], 1);
  }

  Future<ContactSearchPage> searchUsers(
    String keyword, {
    int page = 1,
    int count = 20,
  }) async {
    access.check();
    if (keyword.trim().isEmpty || page < 1 || count < 1 || count > 100) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final raw = await api.chat.post('user/search/full', {
      'keyword': keyword.trim(),
      'pagination': {'pageNumber': page, 'showNumber': count},
    });
    access.check();
    if (raw is! Map || raw['users'] is! List) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final values = (raw['users'] as List)
        .whereType<Map>()
        .map(
          (user) => Contact(
            id: user['userID'] is String ? user['userID'] as String : '',
            name: user['nickname'] is String ? user['nickname'] as String : '',
            avatar: secureMediaUri(
              user['faceURL'] is String ? user['faceURL'] as String : null,
            ),
            canRequestFriend: user['allowAddFriend'] == 1,
          ),
        )
        .where((user) => user.id.isNotEmpty)
        .toList();
    return ContactSearchPage(
      values,
      raw['total'] is int
          ? raw['total'] as int
          : (page - 1) * count + values.length,
    );
  }

  Future<List<GroupSummary>> groups() => _sdk(
    (manager) async => (await manager.groupManager.getJoinedGroupList())
        .map(mapGroup)
        .toList(),
  );
  Future<List<GroupSummary>> searchGroups(String id) => _sdk(
    (manager) async =>
        (await manager.groupManager.getGroupsInfo(groupIDList: [id.trim()]))
            .map(mapGroup)
            .toList(),
  );

  Future<List<ContactRequest>> requests(ContactRequestKind kind) => _sdk((
    manager,
  ) async {
    final records = <ContactRequest>[];
    if (kind == ContactRequestKind.friend) {
      final lists = await Future.wait([
        manager.friendshipManager.getFriendApplicationListAsRecipient(),
        manager.friendshipManager.getFriendApplicationListAsApplicant(),
      ]);
      for (var index = 0; index < lists.length; index++) {
        for (final request in lists[index]) {
          final incoming = index == 0;
          final id = incoming ? request.fromUserID : request.toUserID;
          if (id == null || id.isEmpty) continue;
          records.add(
            ContactRequest(
              kind: kind,
              userId: id,
              name:
                  (incoming ? request.fromNickname : request.toNickname) ?? id,
              incoming: incoming,
              status: request.handleResult ?? 0,
              createdAt: request.createTime ?? 0,
              reason: request.reqMsg ?? '',
              avatar: secureMediaUri(
                incoming ? request.fromFaceURL : request.toFaceURL,
              ),
            ),
          );
        }
      }
    } else {
      final lists = await Future.wait([
        manager.groupManager.getGroupApplicationListAsRecipient(),
        manager.groupManager.getGroupApplicationListAsApplicant(),
      ]);
      for (var index = 0; index < lists.length; index++) {
        for (final request in lists[index]) {
          if ((request.userID ?? '').isEmpty ||
              (request.groupID ?? '').isEmpty) {
            continue;
          }
          records.add(
            ContactRequest(
              kind: kind,
              userId: request.userID!,
              name: request.nickname ?? request.userID!,
              incoming: index == 0,
              status: request.handleResult ?? 0,
              createdAt: request.reqTime ?? 0,
              groupId: request.groupID,
              groupName: request.groupName,
              inviterId: request.inviterUserID,
              reason: request.reqMsg ?? '',
              avatar: secureMediaUri(request.userFaceURL),
            ),
          );
        }
      }
    }
    final unique = {for (final record in records) record.key: record}.values
        .toList();
    unique.sort((a, b) => b.createdAt.compareTo(a.createdAt));
    return unique;
  });

  Future<void> requestFriend(String id, String reason) async {
    access.check();
    final target = _lookupValue(id);
    if (target == access.credentials.userId) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final current = await profile(target);
    access.check();
    if (!current.canRequestFriend || current.blocked || current.friend) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    await _sdk(
      (manager) =>
          manager.friendshipManager.addFriend(userID: target, reason: reason),
    );
  }

  Future<void> remark(String id, String value) => _sdk((manager) async {
    await manager.friendshipManager.setFriendRemark(userID: id, remark: value);
  });
  Future<void> block(String id, bool value) => _sdk((manager) async {
    if (id == access.credentials.userId) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    if (value) {
      await manager.friendshipManager.addBlacklist(userID: id);
    } else {
      await manager.friendshipManager.removeBlacklist(userID: id);
    }
  });
  Future<void> deleteFriend(String id) => _sdk((manager) async {
    await manager.friendshipManager.deleteFriend(userID: id);
  });
  Future<void> handle(
    ContactRequest request, {
    required bool accept,
    String reason = '',
  }) => _sdk((manager) async {
    if (!request.incoming ||
        !request.pending ||
        request.userId == access.credentials.userId) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    if (request.kind == ContactRequestKind.friend) {
      if (accept) {
        await manager.friendshipManager.acceptFriendApplication(
          userID: request.userId,
          handleMsg: reason,
        );
      } else {
        await manager.friendshipManager.refuseFriendApplication(
          userID: request.userId,
          handleMsg: reason,
        );
      }
    } else {
      if (request.groupId == null || request.groupId!.isEmpty) {
        throw const AppFailure(FailureKind.invalidInput);
      }
      final members = await manager.groupManager.getGroupMembersInfo(
        groupID: request.groupId!,
        userIDList: [access.credentials.userId],
      );
      access.check();
      if (!members.any(
        (member) =>
            member.userID == access.credentials.userId &&
            [60, 100].contains(member.roleLevel),
      )) {
        throw const AppFailure(FailureKind.permissionDenied);
      }
      if (accept) {
        await manager.groupManager.acceptGroupApplication(
          groupID: request.groupId!,
          userID: request.userId,
          handleMsg: reason,
        );
      } else {
        await manager.groupManager.refuseGroupApplication(
          groupID: request.groupId!,
          userID: request.userId,
          handleMsg: reason,
        );
      }
    }
  });
  Future<String> conversation(String id) => _sdk(
    (manager) async => (await manager.conversationManager.getOneConversation(
      sourceID: id,
      sessionType: 1,
    )).conversationID,
  );
}

Contact mapContact(FullUserInfo user) => Contact(
  id: user.userID,
  name: user.nickname,
  remark: user.friendInfo?.remark ?? '',
  avatar: secureMediaUri(user.faceURL),
  friend: user.friendInfo != null,
  blocked: user.blackInfo != null,
);
GroupSummary mapGroup(GroupInfo group) => GroupSummary(
  id: group.groupID,
  name: group.groupName?.isNotEmpty == true ? group.groupName! : group.groupID,
  ownerId: group.ownerUserID ?? '',
  members: group.memberCount ?? 0,
  sessionType: group.sessionType,
  avatar: secureMediaUri(group.faceURL),
  status: group.status ?? 0,
);
