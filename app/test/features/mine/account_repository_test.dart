import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/features/mine/data/account_repository.dart';

import '../../core/im/sdk_harness.dart';
import '../../support/http_adapter.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness harness;
  late TenantApi api;
  late TestHttpAdapter http;
  late AccountRepository repository;
  late Map<String, Object?> user;
  Future<void> Function()? beforeRead;
  var receive = 0, applyChanges = true, pendingSync = false;
  setUp(() async {
    harness = SdkHarness();
    await harness.start();
    receive = 0;
    applyChanges = true;
    pendingSync = false;
    beforeRead = null;
    user = {
      'userID': 'alice',
      'nickname': '原昵称',
      'faceURL': 'https://objects.example.com/old.jpg',
      'gender': 0,
      'birth': '0',
      'email': 'alice@example.com',
      'allowAddFriend': 1,
      'allowBeep': 1,
      'allowVibration': 2,
    };
    harness.overrideCall = (call) async {
      if (call.method == 'getSelfUserInfo') {
        return jsonEncode({
          'userID': harness.userId,
          'globalRecvMsgOpt': receive,
        });
      }
      if (call.method == 'setGlobalRecvMessageOpt') {
        receive = (call.arguments as Map)['status'] as int;
      }
      return harness.respond(call);
    };
    http = TestHttpAdapter((request) async {
      if (request.uri.path.endsWith('/update')) {
        if (applyChanges) {
          final changes = Map<String, Object?>.from(request.data as Map)
            ..remove('userID');
          user.addAll(changes);
        }
        return jsonResponse(
          jsonEncode({
            'errCode': 0,
            'data': {'imSyncPending': pendingSync},
          }),
        );
      }
      await beforeRead?.call();
      return jsonResponse(
        jsonEncode({
          'errCode': 0,
          'data': {
            'users': [user],
          },
        }),
      );
    });
    api = TenantApi(
      SessionAccess(harness.session),
      dio: Dio()..httpClientAdapter = http,
    );
    repository = AccountRepository(api, harness.gateway);
  });
  tearDown(() async {
    api.close();
    await harness.dispose();
  });

  test('self-only typed profile uses chat token, rejects a foreign returned identity', () async {
    final result = await repository.profile();
    expect(result.email, 'alice@example.com');
    expect(result.sound, isTrue);
    expect(result.vibration, isFalse);
    expect(http.requests.single.headers['token'], 'fixture-chat');
    expect(http.requests.single.data, {
      'userIDs': ['alice'],
    });
    user['userID'] = 'bob';
    await expectLater(
      repository.profile(),
      throwsA(
        isA<AppFailure>().having(
          (e) => e.kind,
          'kind',
          FailureKind.invalidResponse,
        ),
      ),
    );
  });

  test('partial update sends one requested field, preserves login identities and avatar', () async {
    final result = (await repository.updateProfile(nickname: ' 新昵称 ')).profile;
    expect(result.nickname, '新昵称');
    expect(result.email, 'alice@example.com');
    expect(result.avatar.toString(), user['faceURL']);
    expect(http.requests.first.data, {'userID': 'alice', 'nickname': '新昵称'});
    final date = DateTime(1950, 6, 15);
    expect(
      (await repository.updateProfile(birthday: date)).profile.birthday,
      date,
    );
    expect(http.requests[2].data, {
      'userID': 'alice',
      'birth': date.millisecondsSinceEpoch,
    });
  });

  test('invalid names, URL credentials/fragments, enum and future birth never leave device', () async {
    for (final action in <Future<Object?> Function()>[
      () => repository.updateProfile(nickname: '  '),
      () => repository.updateProfile(nickname: List.filled(65, '字').join()),
      () => repository.updateProfile(nickname: '昵称\n伪造'),
      () => repository.updateProfile(
        avatar: Uri.parse('https://token@objects.example.com/avatar'),
      ),
      () => repository.updateProfile(
        avatar: Uri.parse('http://objects.example.com/avatar'),
      ),
      () => repository.updateProfile(
        avatar: Uri.parse('https://objects.example.com/avatar#private'),
      ),
      () => repository.updateProfile(gender: 3),
      () => repository.updateProfile(
        birthday: DateTime.now().add(const Duration(days: 1)),
      ),
    ]) {
      await expectLater(action(), throwsA(isA<AppFailure>()));
    }
    expect(http.requests, isEmpty);
  });

  test('three notification switches retain legacy1-enabled2-disabled and confirm authoritative read', () async {
    for (final entry in {
      AccountPreference.friendRequests: 'allowAddFriend',
      AccountPreference.sound: 'allowBeep',
      AccountPreference.vibration: 'allowVibration',
    }.entries) {
      final result = await repository.setPreference(entry.key, false);
      expect(result.profile.enabled(entry.key), isFalse);
      expect(http.requests[http.requests.length - 2].data, {
        'userID': 'alice',
        entry.value: 2,
      });
    }
  });

  test(
    'global DND uses the real SDK boundary2 and0 and reads native self option',
    () async {
      expect((await repository.setDoNotDisturb(true)).profile.globalReceive, 2);
      expect(
        (await repository.setDoNotDisturb(false)).profile.globalReceive,
        0,
      );
      final calls = harness.calls
          .where((call) => call.method == 'setGlobalRecvMessageOpt')
          .toList();
      expect(calls.map((call) => (call.arguments as Map)['status']), [2, 0]);
      expect(
        http.requests.every((r) => r.uri.path.endsWith('/find/full')),
        isTrue,
      );
    },
  );

  test('a successful ACK with unchanged authoritative value cannot report save success', () async {
    applyChanges = false;
    await expectLater(
      repository.updateProfile(nickname: '未真正保存'),
      throwsA(
        isA<AppFailure>().having(
          (e) => e.kind,
          'kind',
          FailureKind.dataChanged,
        ),
      ),
    );
    expect(
      http.requests.where((r) => r.uri.path.endsWith('/update')).length,
      1,
    );
  });

  test('saved canonical profile preserves an explicit pending IM projection outcome', () async {
    pendingSync = true;
    final result = await repository.updateProfile(nickname: '待同步昵称');
    expect(result.profile.nickname, '待同步昵称');
    expect(result.imSyncPending, isTrue);
    expect(
      http.requests.where((r) => r.uri.path.endsWith('/update')).length,
      1,
    );
  });

  test('explicit clear uses empty-string presence, including an unsafe legacy avatar', () async {
    user['faceURL'] = 'http://legacy.example.com/avatar';
    final legacy = await repository.profile();
    expect(legacy.avatar, isNull);
    expect(legacy.hasStoredAvatar, isTrue);
    final result = await repository.updateProfile(clearAvatar: true);
    expect(result.profile.hasStoredAvatar, isFalse);
    expect(http.requests[1].data, {'userID': 'alice', 'faceURL': ''});
    await expectLater(
      repository.updateProfile(
        clearAvatar: true,
        avatar: Uri.parse('https://objects.example.com/avatar'),
      ),
      throwsA(isA<AppFailure>()),
    );
  });

  test('delayed old-account response is discarded and its screen cannot mutate the new account', () async {
    final entered = Completer<void>(), release = Completer<void>();
    beforeRead = () async {
      entered.complete();
      await release.future;
    };
    final pending = repository.profile();
    final rejected = expectLater(pending, throwsA(isA<AppFailure>()));
    await entered.future;
    await harness.session.signOut();
    release.complete();
    await rejected;
    final count = http.requests.length;
    await expectLater(
      repository.updateProfile(nickname: '迟到编辑'),
      throwsA(isA<AppFailure>()),
    );
    expect(http.requests.length, count);
  });
}
