import 'dart:async';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/search/domain/global_search.dart';
import 'global_search_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late GlobalFixture f;
  setUp(() async { f = GlobalFixture(); await f.start(); });
  tearDown(() => f.close());
  test('new query drops deferred old results and never renews their ticket', () async {
    final response = f.defer<String>(wireGroups({}));
    final entered = Completer<void>();
    f.pending = (call) {
      if (call.method == 'searchLocalMessages' &&
          (((call.arguments as Map)['filter'] as Map)['keywordList'] as List).single == 'old' &&
          ((((call.arguments as Map)['filter'] as Map)['messageTypeList'] as List).contains(101))) {
        entered.complete(); return response.future;
      }
      return null;
    };
    final old = f.controller.search('old');
    await entered.future.timeout(const Duration(seconds: 2));
    await f.controller.search('match');
    response.complete(wireGroups({cidA: [searchText('old', 'old body')]}));
    await old;
    expect(f.controller.text.map((h) => h.message.id), isNot(contains('old')));
    expect(f.controller.query, 'match');
    expect(f.sdk.gateway.privacyLedger.lookup('old').inspectionComplete, isFalse);
  });
  test('section errors are not empty success and explicit retry keeps other sections', () async {
    f.pending = (call) => call.method == 'getFriendList'
        ? Future.error(PlatformException(code: '10005', message: 'connection refused')) : null;
    await f.controller.search('match');
    expect(f.controller.phases[GlobalSearchKind.contacts], GlobalSearchPhase.error);
    expect(f.controller.phases[GlobalSearchKind.text], GlobalSearchPhase.ready);
    final native = f.calls('searchLocalMessages');
    f.pending = null;
    await f.controller.retry(GlobalSearchKind.contacts);
    expect(f.controller.contacts.length, 2);
    expect(f.calls('searchLocalMessages'), native);
  });
  test('show more is deterministic local snapshot paging with zero extra SDK requests', () async {
    f.messages[cidA] = [for (var i = 0; i < 70; i++) searchFile('f$i', 'match-$i.txt')];
    await f.controller.search('match');
    final before = f.calls('searchLocalMessages');
    expect(f.controller.visibleFiles.length, 70);
    expect(f.controller.shown[GlobalSearchKind.files], 30);
    f.controller.more(GlobalSearchKind.files);
    expect(f.controller.shown[GlobalSearchKind.files], 60);
    f.controller.more(GlobalSearchKind.files);
    expect(f.calls('searchLocalMessages'), before);
  });
  test('global clear removes result and old selection after ACK without auto search', () async {
    await f.controller.search('match');
    final selection = f.repository.selection(f.controller.text.first);
    final before = f.calls('searchLocalMessages');
    await f.sdk.gateway.clearAllMessages(f.sdk.session.state.generation);
    await Future<void>.delayed(Duration.zero);
    expect(f.controller.text, isEmpty);
    expect(selection.isCurrent(), isFalse);
    expect(f.calls('searchLocalMessages'), before);
    await f.controller.retry(GlobalSearchKind.text);
    expect(f.calls('searchLocalMessages'), before + 1);
  });
  test('CID clear keeps unrelated hits but rejects its old snapshot selection', () async {
    await f.controller.search('match');
    final selected = f.repository.selection(f.controller.text.firstWhere((h) => h.conversationId == cidA));
    await f.sdk.gateway.clearConversationMessages(f.sdk.session.state.generation, cidA);
    await Future<void>.delayed(Duration.zero);
    expect(f.controller.text.every((h) => h.conversationId == cidHidden), isTrue);
    expect(selected.isCurrent(), isFalse);
  });
  test('session invalidation immediately empties state and late reply does not refill', () async {
    final response = f.defer<String>(wireGroups({}));
    final entered = Completer<void>();
    f.pending = (call) {
      if (call.method == 'searchLocalMessages' &&
          ((((call.arguments as Map)['filter'] as Map)['messageTypeList'] as List).contains(101))) {
        entered.complete(); return response.future;
      }
      return null;
    };
    final pending = f.controller.search('match');
    await entered.future.timeout(const Duration(seconds: 2));
    final signedOut = f.sdk.session.signOut(revoke: false);
    await Future<void>.delayed(Duration.zero);
    expect(f.controller.usable, isFalse);
    expect(f.controller.text, isEmpty);
    response.complete(wireGroups({cidA: [searchText('late', 'match')]}));
    await pending; await signedOut;
    expect(f.controller.text, isEmpty);
  });
  test('same user on a different issuer and tenant cannot reuse old controller or selection', () async {
    await f.controller.search('match');
    final selection = f.repository.selection(f.controller.text.first);
    final before = f.calls('searchLocalMessages');
    await f.rotateOwner();
    expect(f.controller.usable, isFalse); expect(selection.isCurrent(), isFalse);
    await f.controller.retry(GlobalSearchKind.text);
    expect(f.calls('searchLocalMessages'), before);
    expect(f.controller.text, isEmpty);
  });
  test('repeated same-query submit while loading does not duplicate native scans', () async {
    final response = f.defer<String>(wireGroups({}));
    final entered = Completer<void>();
    f.pending = (call) {
      if (call.method == 'searchLocalMessages' &&
          ((((call.arguments as Map)['filter'] as Map)['messageTypeList'] as List).contains(101))) {
        entered.complete(); return response.future;
      }
      return null;
    };
    final pending = f.controller.search('match');
    await entered.future.timeout(const Duration(seconds: 2));
    final before = f.calls('searchLocalMessages');
    await f.controller.search('match');
    expect(f.calls('searchLocalMessages'), before);
    response.complete(wireGroups({cidA: [searchText('one', 'match')]}));
    await pending;
    expect(f.controller.phases[GlobalSearchKind.text], GlobalSearchPhase.ready);
  });
}
