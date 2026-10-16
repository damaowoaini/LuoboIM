import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/qr/domain/qr_invite.dart';

import 'qr_flow_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late QrFlowFixture f;
  setUp(() { f = QrFlowFixture(); });
  tearDown(() => f.business.close());

  test('personal code requires exact authoritative self identity', () async {
    await f.start();
    final subject = await f.repository.ownCode(null);
    expect(subject.id, 'alice'); expect(subject.kind, QrInviteKind.user);
    expect(f.business.calls('getSelfUserInfo'), greaterThan(0));
    f.business.pending = (call) => call.method == 'getSelfUserInfo'
        ? Future.value(jsonEncode({'userID': 'other'})) : null;
    await expectLater(f.repository.ownCode(null), throwsA(isA<AppFailure>()));
  });
  test('ordinary muted member can display a current group code without manager permission', () async {
    await f.start();
    f.business.pending = (call) => call.method == 'getGroupMembersInfo'
        ? Future.value(jsonEncode([{'groupID': 'group-a', 'userID': 'alice', 'roleLevel': 20,
            'muteEndTime': 4102444800000}])) : null;
    final subject = await f.repository.ownCode('group-a');
    expect(subject.id, 'group-a'); expect(subject.kind, QrInviteKind.group);
    expect(f.business.calls('isJoinGroup'), greaterThan(0));
  });
  test('nonmember may inspect group discovery but cannot publish its QR', () async {
    await f.start();
    f.business.pending = (call) => call.method == 'isJoinGroup' ? Future.value('false') : null;
    final invite = QrInvite.decode(f.code(QrInviteKind.group, 'group-a'), f.repository.access.credentials);
    expect((await f.repository.resolve(invite)).id, 'group-a');
    await expectLater(f.repository.ownCode('group-a'), throwsA(isA<AppFailure>()));
    expect(f.business.calls('joinGroup'), 0);
  });
  test('dismissed group fails discovery and display instead of showing an old code', () async {
    await f.start();
    f.business.groups[0]['status'] = 2;
    final invite = QrInvite.decode(f.code(QrInviteKind.group, 'group-a'), f.repository.access.credentials);
    await expectLater(f.repository.resolve(invite), throwsA(isA<AppFailure>()));
    await expectLater(f.repository.ownCode('group-a'), throwsA(isA<AppFailure>()));
  });
  test('public user hydration rejects a mismatched native ID', () async {
    await f.start();
    f.business.pending = (call) => call.method == 'getUsersInfo'
        ? Future.value(jsonEncode([{'publicInfo': {'userID': 'foreign', 'nickname': 'same'}}])) : null;
    final invite = QrInvite.decode(f.code(QrInviteKind.user, 'bob'), f.repository.access.credentials);
    await expectLater(f.repository.resolve(invite), throwsA(isA<AppFailure>()));
    expect(f.business.calls('addFriend'), 0);
  });
  test('group hydration never accepts the first foreign group record', () async {
    await f.start();
    f.business.pending = (call) => call.method == 'getGroupsInfo'
        ? Future.value(jsonEncode([{'groupID': 'foreign', 'groupName': 'same', 'status': 0}])) : null;
    final invite = QrInvite.decode(f.code(QrInviteKind.group, 'group-a'), f.repository.access.credentials);
    await expectLater(f.repository.resolve(invite), throwsA(isA<AppFailure>()));
  });
  test('old repository cannot acquire a fresh issuer tenant or owner after rotation', () async {
    await f.start();
    final invite = QrInvite.decode(f.code(QrInviteKind.user, 'bob'), f.repository.access.credentials);
    await f.business.rotateOwner();
    final before = f.business.calls('getUsersInfo');
    await expectLater(f.repository.resolve(invite), throwsA(isA<AppFailure>()));
    expect(f.business.calls('getUsersInfo'), before);
    expect(f.repository.current, isFalse);
  });
}
