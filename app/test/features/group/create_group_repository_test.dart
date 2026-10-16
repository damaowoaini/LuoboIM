import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';

import 'create_group_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late CreationFixture f;
  setUp(() => f = CreationFixture());
  tearDown(() async {
    if (!f.started) return;
    for (final finish in f.pending) { finish(); }
    f.base.api.close(); await f.base.sdk.dispose(); f.base.body.dispose();
  });
  test('create ACK retains the exact group before any conversation read', () async {
    await f.start(); var dispatched = 0;
    final created = await f.repository.createRecord('  讨论群  ', ['alice', 'bob', 'carol', 'bob'],
      avatar: 'https://objects.example.com/avatar.png', onDispatched: () => dispatched++);
    expect(created.id, 'created-group'); expect(dispatched, 1);
    final payload = f.creates.single;
    expect(payload['memberUserIDs'], ['bob', 'carol']);
    expect((payload['groupInfo'] as Map)['groupName'], '讨论群');
    expect((payload['groupInfo'] as Map)['faceURL'], 'https://objects.example.com/avatar.png');
    expect((payload['groupInfo'] as Map)['groupType'], 2);
    expect(payload['adminUserIDs'], isEmpty);
    expect(f.base.sdk.calls.where((c) => c.method == 'getOneConversation'), isEmpty);
  });
  test('existing create convenience wrapper retains normal group and conversation behavior', () async {
    await f.start();
    expect(await f.repository.create('讨论群', ['bob', 'carol']), 'created-cid');
    expect(f.creates, hasLength(1));
    expect(f.base.sdk.calls.where((c) => c.method == 'getOneConversation'), hasLength(1));
  });
  test('avatar remains optional and creation needs no storage request', () async {
    await f.start(); await f.repository.createRecord('讨论群', ['bob', 'carol']);
    expect((f.creates.single['groupInfo'] as Map)['faceURL'], isNull);
    expect(f.providerBodies, isEmpty);
  });
  test('invalid draft or insecure avatar is rejected before native dispatch', () async {
    await f.start(); var dispatched = 0;
    for (final value in ['', 'valid']) {
      await expectLater(f.repository.createRecord(value, ['bob'], onDispatched: () => dispatched++),
        throwsA(isA<AppFailure>()));
    }
    await expectLater(f.repository.createRecord('讨论群', ['bob', 'carol'],
      avatar: 'http://untrusted.example.com/a.png', onDispatched: () => dispatched++), throwsA(isA<AppFailure>()));
    expect(dispatched, 0); expect(f.creates, isEmpty);
  });
  test('expired caller view dispatches neither create nor its admission callback', () async {
    await f.start(); var dispatched = 0;
    await expectLater(f.repository.createRecord('讨论群', ['bob', 'carol'],
      isCurrent: () => false, onDispatched: () => dispatched++), throwsA(isA<AppFailure>()));
    expect(dispatched, 0); expect(f.creates, isEmpty);
  });
  test('session invalidation during real native create cannot issue a created receipt', () async {
    await f.start(); final entered = Completer<void>(), finish = f.defer<void>(null);
    f.createReply = () async { entered.complete(); await finish.future; };
    final request = f.repository.createRecord('讨论群', ['bob', 'carol']);
    final failed = expectLater(request, throwsA(isA<AppFailure>()));
    await entered.future;
    final rotation = f.base.sdk.session.signOut(revoke: false);
    finish.complete(); await failed;
    await rotation;
    expect(f.creates, hasLength(1));
    expect(f.base.sdk.calls.where((c) => c.method == 'getOneConversation'), isEmpty);
  });
  test('final admission boundary rechecks a revoked caller before native create', () async {
    await f.start(); var current = true;
    await expectLater(f.repository.createRecord('讨论群', ['bob', 'carol'], isCurrent: () => current,
      onDispatched: () => current = false), throwsA(isA<AppFailure>()));
    expect(f.creates, isEmpty);
  });
  test('foreign owner in malformed create ACK is not accepted as an owned group', () async {
    await f.start();
    f.createReply = () async { f.base.sdk.userId = 'foreign'; };
    await expectLater(f.repository.createRecord('讨论群', ['bob', 'carol']), throwsA(isA<AppFailure>()));
    expect(f.creates, hasLength(1));
    expect(f.base.sdk.calls.where((c) => c.method == 'getOneConversation'), isEmpty);
  });
  test('conversation metadata wait cannot renew the original create view', () async {
    await f.start(); var current = true;
    final entered = Completer<void>(), finish = f.defer<void>(null);
    f.base.roleReply = () async { entered.complete(); await finish.future; };
    final pending = f.repository.conversation('created-group', isCurrent: () => current);
    final failed = expectLater(pending, throwsA(isA<AppFailure>()));
    await entered.future; current = false; finish.complete(); await failed;
    expect(f.base.sdk.calls.where((c) => c.method == 'getOneConversation'), isEmpty);
    expect(f.creates, isEmpty);
  });
  test('created group recovery rejects a foreign conversation response instead of navigating it', () async {
    await f.start();
    f.conversationReply = () async => '{"conversationID":"foreign-cid","conversationType":3,"groupID":"foreign-group"}';
    await expectLater(f.repository.conversation('created-group'), throwsA(isA<AppFailure>()));
    expect(f.base.sdk.calls.where((c) => c.method == 'getOneConversation'), hasLength(1));
    expect(f.creates, isEmpty);
  });
  test('Unicode creation limit counts emoji and combining graphemes rather than UTF16 units', () async {
    await f.start();
    for (final cluster in ['🙂', 'e\u0301', '🧑🏽‍💻']) {
      final accepted = List.filled(128, cluster).join();
      final rejected = List.filled(129, cluster).join();
      final oldCount = f.creates.length;
      await f.repository.createRecord(accepted, ['bob', 'carol']);
      expect((f.creates.last['groupInfo'] as Map)['groupName'], accepted);
      await expectLater(f.repository.createRecord(rejected, ['bob', 'carol']), throwsA(isA<AppFailure>()));
      expect(f.creates.length, oldCount + 1);
    }
  });
}
