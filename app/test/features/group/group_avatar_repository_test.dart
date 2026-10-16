import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/features/group/data/group_repository.dart';

import '../../core/im/sdk_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness sdk;
  late GroupRepository repository;
  int role = 100, status = 0;
  Future<void> Function()? beforeRole;
  setUp(() async {
    sdk = SdkHarness();
    await sdk.start();
    repository = GroupRepository(sdk.gateway, SessionAccess(sdk.session));
    role = 100;
    status = 0;
    beforeRole = null;
    sdk.overrideCall = (call) async {
      final args = call.arguments as Map;
      switch (call.method) {
        case 'getGroupsInfo':
          return jsonEncode([
            {
              'groupID': 'group-a',
              'groupType': 2,
              'groupName': '测试群',
              'ownerUserID': 'alice',
              'status': status,
            },
          ]);
        case 'isJoinGroup':
          return 'true';
        case 'getGroupMembersInfo':
          await beforeRole?.call();
          return jsonEncode([
            {'groupID': args['groupID'], 'userID': 'alice', 'roleLevel': role},
          ]);
      }
      return sdk.respond(call);
    };
  });
  tearDown(() => sdk.dispose());

  test(
    'view invalidation while current role read waits never dispatches metadata',
    () async {
      final entered = Completer<void>(), reply = Completer<void>();
      beforeRole = () {
        entered.complete();
        return reply.future;
      };
      var current = true;
      final pending = repository.update(
        'group-a',
        avatar: 'https://objects.example.com/avatar.png',
        isCurrent: () => current,
      );
      final expectation = expectLater(
        pending,
        throwsA(
          isA<AppFailure>().having(
            (e) => e.kind,
            'kind',
            FailureKind.cancelled,
          ),
        ),
      );
      await entered.future;
      current = false;
      reply.complete();
      await expectation;
      expect(sdk.calls.where((c) => c.method == 'setGroupInfo'), isEmpty);
    },
  );

  test('latest role withdrawal prevents avatar metadata', () async {
    role = 20;
    await expectLater(
      repository.update(
        'group-a',
        avatar: 'https://objects.example.com/avatar.png',
        isCurrent: () => true,
      ),
      throwsA(
        isA<AppFailure>().having(
          (e) => e.kind,
          'kind',
          FailureKind.permissionDenied,
        ),
      ),
    );
    expect(sdk.calls.where((c) => c.method == 'setGroupInfo'), isEmpty);
  });

  test(
    'muted administrator retains avatar management without send permission',
    () async {
      role = 60;
      status = 3;
      await repository.update(
        'group-a',
        avatar: 'https://objects.example.com/avatar.png',
        isCurrent: () => true,
      );
      final call = sdk.calls.singleWhere((c) => c.method == 'setGroupInfo');
      expect(call.arguments['groupInfo']['groupID'], 'group-a');
      expect(
        call.arguments['groupInfo']['faceURL'],
        'https://objects.example.com/avatar.png',
      );
    },
  );

  test(
    'existing name and announcement callers keep optional-guard compatibility',
    () async {
      await repository.update('group-a', name: '  新群名  ', announcement: '说明');
      final info =
          sdk.calls
                  .singleWhere((c) => c.method == 'setGroupInfo')
                  .arguments['groupInfo']
              as Map;
      expect(info['groupName'], '新群名');
      expect(info['notification'], '说明');
      expect(info['faceURL'], isNull);
    },
  );
}
