import 'dart:async';
import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/privacy/message_privacy.dart';
import 'package:imapp/features/conversation/data/conversation_repository.dart';

import 'next_unread_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('next unread reads the entire SDK inbox beyond forty and preserves muted ordering without writes', () async {
    final f = NextUnreadFixture(); await f.start();
    try {
      final snapshot = await f.repository.unreadSnapshot(sourceCurrent: () => true);
      expect(snapshot.items, hasLength(125)); expect(snapshot.items[84].id, 'inbox-84');
      expect(snapshot.items[84].muted, isTrue); expect(snapshot.items[84].unread, 2);
      expect(snapshot.items.last.id, 'inbox-124'); expect(f.reads, 1); expect(f.writes, 0);
      final call = f.sdk.calls.singleWhere((call) => call.method == 'getAllConversationList');
      expect((call.arguments as Map).containsKey('offset'), isFalse);
      expect((call.arguments as Map).containsKey('count'), isFalse);
      expect(f.sdk.calls.any((call) => call.method == 'getConversationListSplit'), isFalse);
    } finally { await f.closeUnit(); }
  });

  test('next unread complete empty snapshot differs from a native read failure', () async {
    final f = NextUnreadFixture(); await f.start(count: 0);
    try {
      expect((await f.repository.unreadSnapshot(sourceCurrent: () => true)).items, isEmpty);
      f.sdk.overrideCall = (call) async {
        if (call.method == 'getAllConversationList') throw PlatformException(code: '999', message: 'private native detail');
        return f.sdk.respond(call);
      };
      await expectLater(f.repository.unreadSnapshot(sourceCurrent: () => true), throwsA(isA<AppFailure>()));
      expect(f.writes, 0);
    } finally { await f.closeUnit(); }
  });

  test('next unread rejects duplicate conversation identities rather than claiming a complete cycle', () async {
    final f = NextUnreadFixture(); await f.start(count: 1);
    try {
      f.sdk.conversations.add(Map.of(f.sdk.conversations.single));
      await expectLater(f.repository.unreadSnapshot(sourceCurrent: () => true),
        throwsA(isA<AppFailure>().having((failure) => failure.kind, 'kind', FailureKind.invalidResponse)));
    } finally { await f.closeUnit(); }
  });

  test('next unread keeps the original query ticket through a delayed native reply', () async {
    final f = NextUnreadFixture(); await f.start();
    try {
      var current = true; f.allReply = Completer<dynamic>(); f.allEntered = Completer<void>();
      final loading = f.repository.unreadSnapshot(sourceCurrent: () => current);
      final assertion = expectLater(loading, throwsA(isA<AppFailure>()));
      await f.allEntered!.future; current = false; f.allReply!.complete(jsonEncode(f.sdk.conversations));
      await assertion; expect(f.writes, 0);
    } finally { await f.closeUnit(); }
  });

  test('next unread source cannot renew its owner after real logout and login starts', () async {
    final f = NextUnreadFixture(); await f.start();
    try {
      f.allReply = Completer<dynamic>(); f.allEntered = Completer<void>();
      final loading = f.repository.unreadSnapshot(sourceCurrent: () => true);
      final assertion = expectLater(loading, throwsA(isA<AppFailure>()));
      await f.allEntered!.future; final change = f.changeOwner();
      f.allReply!.complete(jsonEncode(f.sdk.conversations)); await change; await assertion;
      final fresh = ConversationRepository(f.sdk.gateway, f.sdk.session);
      f.allReply = null;
      expect((await fresh.unreadSnapshot(sourceCurrent: () => true)).items, hasLength(125));
    } finally { await f.closeUnit(); }
  });

  test('next unread original CID revision prevents private preview observation after a concurrent clear', () async {
    final f = NextUnreadFixture(); await f.start(count: 1, unread: const {0});
    try {
      f.allReply = Completer<dynamic>(); f.allEntered = Completer<void>();
      final loading = f.repository.unreadSnapshot(sourceCurrent: () => true);
      await f.allEntered!.future;
      await f.sdk.gateway.clearConversationMessages(f.sdk.session.state.generation, 'inbox-0');
      f.allReply!.complete(jsonEncode(f.sdk.conversations)); final snapshot = await loading;
      expect(snapshot.cidCurrent('inbox-0'), isFalse); expect(snapshot.items.single.preview, isEmpty);
      expect(snapshot.items.single.unread, 2);
    } finally { await f.closeUnit(); }
  });

  test('next unread admission rejects a quarantined CID instead of replacing the original cut with a fresh lease', () async {
    final f = NextUnreadFixture(timeout: const Duration(milliseconds: 40));
    await f.start(count: 1, unread: const {0}); final blocked = Completer<void>();
    try {
      final generation = f.sdk.session.state.generation;
      final originalCut = f.sdk.gateway.historyAccess(generation, conversationId: 'inbox-0');
      final latest = Map<String, dynamic>.from(f.sdk.conversations.single['latestMsg'] as Map);
      latest['attachedInfoElem'] = {'isPrivateChat': true, 'burnDuration': 30};
      f.sdk.conversations.single['latestMsg'] = latest;
      expect(inspectMessagePrivacy(latest).containsPrivateContent, isTrue);
      expect(f.sdk.gateway.privacyLedger.lookup('message-0').inspectionComplete, isFalse);
      f.sdk.overrideCall = (call) async {
        if (call.method == 'clearConversationAndDeleteAllMsg') { await blocked.future; return null; }
        if (call.method == 'getAllConversationList') return jsonEncode(f.sdk.conversations);
        return f.sdk.respond(call);
      };
      await expectLater(f.sdk.gateway.clearConversationMessages(f.sdk.session.state.generation, 'inbox-0'),
        throwsA(isA<AppFailure>()));
      // A timed-out dispatched native call quarantines the whole SDK. No new
      // snapshot may be admitted just to inspect one unavailable CID.
      expect(() => f.repository.unreadSnapshot(sourceCurrent: () => true),
        throwsA(isA<AppFailure>().having((failure) => failure.kind, 'kind', FailureKind.sessionExpired)));
      expect(() => originalCut.check(),
        throwsA(isA<AppFailure>().having((failure) => failure.kind, 'kind', FailureKind.sessionExpired)));
      expect(f.reads, 0);
      expect(f.sdk.gateway.privacyLedger.lookup('message-0').inspectionComplete, isFalse);
    } finally { if (!blocked.isCompleted) blocked.complete(); await f.closeUnit(); }
  });
  for (final dimension in ['issuer', 'tenant']) {
    test('next unread $dimension replacement cannot adopt an old in-flight native snapshot', () async {
      final f = NextUnreadFixture(); await f.start();
      try {
        f.allReply = Completer<dynamic>(); f.allEntered = Completer<void>();
        final loading = f.repository.unreadSnapshot(sourceCurrent: () => true);
        final assertion = expectLater(loading, throwsA(isA<AppFailure>()));
        await f.allEntered!.future;
        final change = f.changeOwner(user: 'alice',
          issuer: dimension == 'issuer' ? 'https://other.example.com/' : 'https://im.example.com/',
          tenant: dimension == 'tenant' ? 'other-tenant' : 'fixture-tenant');
        f.allReply!.complete(jsonEncode(f.sdk.conversations)); await change; await assertion;
        expect(f.writes, 0);
      } finally { await f.closeUnit(); }
    });
  }

}
