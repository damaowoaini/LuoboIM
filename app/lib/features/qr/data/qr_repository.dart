import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/im_event.dart';
import '../../../core/network/tenant_api.dart';
import '../../../core/tenant/session_access.dart';
import '../../../providers.dart';
import '../../contacts/contacts_providers.dart';
import '../../contacts/data/contacts_repository.dart';
import '../../group/data/group_repository.dart';
import '../../mine/data/mine_repository.dart';
import '../domain/qr_invite.dart';

final class QrSubject {
  const QrSubject(this.kind, this.id, this.name);
  final QrInviteKind kind;
  final String id, name;
}

final class QrRepository {
  const QrRepository(this.access, this.contacts, this.groups, this.mine);
  final SessionAccess access;
  final ContactsRepository contacts;
  final GroupRepository groups;
  final MineRepository mine;
  bool get current => access.isCurrent &&
      identical(access.session.state.credentials, access.credentials);
  void check() {
    if (!current) throw const AppFailure(FailureKind.cancelled);
  }
  Future<QrSubject> resolve(QrInvite invite) async {
    check();
    if (invite.kind == QrInviteKind.user) {
      final user = await contacts.profile(invite.subjectId);
      check();
      if (user.id != invite.subjectId) throw const AppFailure(FailureKind.invalidResponse);
      return QrSubject(invite.kind, user.id, user.displayName);
    }
    final group = await groups.profile(invite.subjectId);
    check();
    if (group.summary.id != invite.subjectId || group.summary.status == 2) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return QrSubject(invite.kind, group.summary.id, group.summary.name);
  }
  Future<QrSubject> ownCode(String? groupId) async {
    check();
    if (groupId == null) {
      final user = await mine.profile();
      check();
      if (user.userId != access.credentials.userId || !QrInvite.validId(user.userId)) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      return QrSubject(QrInviteKind.user, user.userId, user.nickname);
    }
    if (!QrInvite.validId(groupId)) throw const AppFailure(FailureKind.invalidInput);
    final group = await groups.profile(groupId);
    check();
    if (group.summary.id != groupId || group.summary.status == 2 ||
        !group.joined || group.me?.id != access.credentials.userId) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    return QrSubject(QrInviteKind.group, groupId, group.summary.name);
  }
}

final qrRepositoryProvider = Provider.autoDispose<QrRepository?>((ref) {
  final access = ref.watch(sessionAccessProvider);
  final contacts = ref.watch(contactsRepositoryProvider);
  final groups = ref.watch(groupRepositoryProvider);
  if (access == null || contacts == null || groups == null) return null;
  return QrRepository(access, contacts, groups,
    MineRepository(ref.watch(imGatewayProvider), access.generation));
});

final qrSubjectProvider = FutureProvider.autoDispose.family<QrSubject, String?>((ref, id) {
  final repository = ref.watch(qrRepositoryProvider);
  if (repository == null) throw const AppFailure(FailureKind.cancelled);
  final events = repository.groups.gateway.events.listen((event) {
    if (ref.mounted && repository.current && event.generation == repository.access.generation &&
        (event.kind == ImEventKind.groupsChanged || event.kind == ImEventKind.selfProfileChanged)) {
      ref.invalidateSelf();
    }
  });
  ref.onDispose(() => unawaited(events.cancel()));
  return repository.ownCode(id);
});
