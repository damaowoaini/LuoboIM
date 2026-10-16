import 'dart:async';
import 'dart:convert';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/search/domain/global_search.dart';
import 'global_search_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late GlobalFixture f;
  setUp(() async { f = GlobalFixture(); await f.start(); });
  tearDown(() => f.close());
  test('global is one untruncated request and includes hidden CID outside the loaded inbox', () async {
    f.messages[cidA] = [for (var i = 0; i < 55; i++) searchText('a$i', 'match $i')];
    final cut = f.repository.begin(' match ', () => true);
    final result = await f.repository.messages(cut, GlobalSearchKind.text);
    expect(result.length, 56);
    expect(result.any((hit) => hit.conversationId == cidHidden), isTrue);
    expect(f.calls('getAllConversationList'), 0);
    expect(f.calls('getConversationListSplit'), 0);
    final filter = (f.sdk.calls.singleWhere((c) => c.method == 'searchLocalMessages').arguments as Map)['filter'] as Map;
    expect(filter['conversationID'], isNull);
    expect(filter['keywordList'], ['match']);
    expect(filter['searchTimePosition'], 1700000000);
    expect(filter['pageIndex'], 1);
  });
  test('complete friend and joined-group sources are not capped to the inbox or first40', () async {
    f.friends.addAll([for (var i = 0; i < 55; i++) {'friendInfo': {'userID': 'match-$i', 'nickname': '同名'}}]);
    f.groups.addAll([for (var i = 0; i < 55; i++) {'groupID': 'match-$i', 'groupName': '同名', 'groupType': 2, 'memberCount': 2}]);
    final cut = f.repository.begin('match', () => true);
    expect((await f.repository.friends(cut)).length, 57);
    expect((await f.repository.groups(cut)).length, 56);
  });
  test('remark matching and user/group ID namespaces remain distinct', () async {
    var cut = f.repository.begin('客户', () => true);
    expect((await f.repository.friends(cut)).single.id, 'bob');
    f.friends.add({'friendInfo': {'userID': 'group-a', 'nickname': 'same'}});
    cut = f.repository.begin('group-a', () => true);
    expect((await f.repository.friends(cut)).single.id, 'group-a');
    expect((await f.repository.groups(cut)).single.id, 'group-a');
  });
  test('equal client IDs in different CIDs remain two navigation identities', () async {
    f.messages[cidA] = [searchText('same', 'match A')];
    f.messages[cidHidden] = [searchText('same', 'match hidden')];
    final hits = await f.repository.messages(f.repository.begin('match', () => true), GlobalSearchKind.text);
    expect(hits.length, 2); expect(hits.map((h) => h.key).toSet().length, 2);
    for (final hit in hits) {
      final selection = f.repository.selection(hit);
      expect(selection.matches(hit.conversationId, 'same'), isTrue);
      expect(selection.matches(hit.conversationId == cidA ? cidHidden : cidA, 'same'), isFalse);
      expect((await f.repository.readExact(hit)).text, hit.message.text);
    }
  });
  test('same clientID private facts conservatively affect both CIDs rather than leaking a body', () async {
    f.messages[cidA] = [{...searchText('same', 'match private'), 'attachedInfoElem': {'isPrivateChat': true}}];
    f.messages[cidHidden] = [searchText('same', 'match ordinary other')];
    final hits = await f.repository.messages(f.repository.begin('match', () => true), GlobalSearchKind.text);
    expect(hits.length, 2);
    expect(hits.every((h) => h.summary == '[阅后即焚消息]'), isTrue);
  });
  test('CID clear begun after dispatch blocks unknown late IDs before privacy observe', () async {
    final response = f.defer<String>(wireGroups({}));
    final entered = Completer<void>();
    f.pending = (call) {
      if (call.method == 'searchLocalMessages') { entered.complete(); return response.future; }
      return null;
    };
    final pending = f.repository.messages(f.repository.begin('match', () => true), GlobalSearchKind.text);
    await entered.future.timeout(const Duration(seconds: 2));
    await f.sdk.gateway.clearConversationMessages(f.sdk.session.state.generation, cidA);
    response.complete(wireGroups({cidA: [searchText('never-observed', 'match old')], cidHidden: [searchText('kept', 'match other')]}));
    final hits = await pending;
    expect(hits.map((h) => h.message.id), ['kept']);
    expect(f.sdk.gateway.privacyLedger.lookup('never-observed').inspectionComplete, isFalse);
    expect(f.calls('searchLocalMessages'), 1);
  });
  test('global clear permanently invalidates old query even after ACK', () async {
    final cut = f.repository.begin('match', () => true);
    await f.sdk.gateway.clearAllMessages(f.sdk.session.state.generation);
    await expectLater(f.repository.messages(cut, GlobalSearchKind.text), throwsA(isA<AppFailure>()));
    expect(f.calls('searchLocalMessages'), 0);
  });
  test('global clear drains an already-dispatched search without teaching late IDs to the ledger', () async {
    final response = f.defer<String>(wireGroups({}));
    final entered = Completer<void>();
    f.pending = (call) {
      if (call.method == 'searchLocalMessages') { entered.complete(); return response.future; }
      return null;
    };
    final pending = f.repository.messages(f.repository.begin('match', () => true), GlobalSearchKind.text);
    final checked = expectLater(pending, throwsA(isA<AppFailure>()));
    await entered.future.timeout(const Duration(seconds: 2));
    final clearing = f.sdk.gateway.clearAllMessages(f.sdk.session.state.generation);
    response.complete(wireGroups({cidA: [searchText('late-global', 'match')]}));
    await checked; await clearing;
    expect(f.sdk.gateway.privacyLedger.lookup('late-global').inspectionComplete, isFalse);
  });
  test('timed-out clear/native work cannot be admitted by a new global query', () async {
    await f.close();
    f = GlobalFixture(operationTimeout: const Duration(milliseconds: 100));
    await f.start();
    final response = f.defer<dynamic>('{"messageList":[],"isEnd":true}');
    final entered = Completer<void>();
    f.pending = (call) {
      if (call.method == 'getAdvancedHistoryMessageList') { entered.complete(); return response.future; }
      return null;
    };
    final scoped = ChatRepository(gateway: f.sdk.gateway, session: f.sdk.session,
        conversationId: cidA);
    final history = expectLater(scoped.history(), throwsA(isA<AppFailure>()));
    await entered.future.timeout(const Duration(seconds: 2));
    await expectLater(f.sdk.gateway.clearConversationMessages(f.sdk.session.state.generation, cidA),
        throwsA(isA<AppFailure>()));
    await history;
    // This public timeout path may quarantine the entire native runtime too.
    // It is not claimed as an isolated-CID-only runtime reproduction.
    final cut = f.repository.begin('match', () => true);
    expect(f.repository.cidCurrent(cut, cidA), isFalse);
    await expectLater(f.repository.messages(cut, GlobalSearchKind.text), throwsA(isA<AppFailure>()));
    expect(f.calls('searchLocalMessages'), 0);
    response.complete(jsonEncode({'messageList': <Object?>[], 'isEnd': true}));
    await Future<void>.delayed(Duration.zero);
  });
  for (final revoked in [false, true]) {
    test('real native ${revoked ? 'revoke' : 'delete'} callback prevents a late snapshot refill', () async {
      await f.controller.search('match');
      final response = f.defer<String>(wireGroups({}));
    final entered = Completer<void>();
      f.pending = (call) {
        if (call.method == 'searchLocalMessages') { entered.complete(); return response.future; }
        return null;
      };
      final pending = f.controller.retry(GlobalSearchKind.text);
      await entered.future.timeout(const Duration(seconds: 2));
      await f.sdk.callback(ListenerType.advancedMsgListener, {
        'type': revoked ? 'onNewRecvMessageRevoked' : 'onMsgDeleted',
        'data': revoked ? {'messageRevoked': '{"clientMsgID":"a1"}'}
            : {'message': '{"clientMsgID":"a1","contentType":101}'},
      });
      response.complete(wireGroups({cidA: [searchText('a1', 'match old')]}));
      await pending;
      expect(f.controller.text, isEmpty);
    });
  }
  test('nested private metadata and deleted or wrong-type results never reveal ordinary summaries', () async {
    f.pending = (call) => call.method == 'searchLocalMessages' ? Future.value(wireGroups({cidA: [
      {...searchText('nested', 'match outer'), 'quoteElem': {'text': 'match', 'quoteMessage': {
        ...searchText('inner', 'secret'), 'attachedInfoElem': {'isPrivateChat': true},
      }}},
      {...searchText('deleted', 'match removed'), 'status': 4},
      searchFile('wrong-type', 'match.txt'),
    ]})) : null;
    final hits = await f.repository.messages(f.repository.begin('match', () => true), GlobalSearchKind.text);
    expect(hits.single.message.id, 'nested');
    expect(hits.single.summary, '[阅后即焚消息]');
  });
  test('metadata-only retry keeps known private and hidden tombstone facts', () async {
    final cut = f.repository.begin('match', () => true);
    f.messages[cidA] = [{...searchText('private', 'match secret'), 'attachedInfoElem': {'isPrivateChat': true}}];
    await f.repository.messages(cut, GlobalSearchKind.text);
    f.messages[cidA] = [searchText('private', 'match secret')];
    var hits = await f.repository.messages(cut, GlobalSearchKind.text);
    expect(hits.firstWhere((h) => h.message.id == 'private').summary, '[阅后即焚消息]');
    f.repository.forget(['private']);
    hits = await f.repository.messages(cut, GlobalSearchKind.text);
    expect(hits.where((h) => h.message.id == 'private'), isEmpty);
  });
  test('exact lookup ignores same-ID foreign-CID records and cannot renew a cleared cut', () async {
    final hits = await f.repository.messages(f.repository.begin('match', () => true), GlobalSearchKind.text);
    final hit = hits.firstWhere((h) => h.conversationId == cidA);
    f.pending = (call) => call.method == 'findMessageList' ? Future.value(wireGroups({
      cidHidden: [searchText('a1', 'foreign body')],
      cidA: [searchText('a1', 'match ordinary')],
    })) : null;
    final source = await f.repository.readExact(hit);
    expect(source.text, 'match ordinary');
    final response = f.defer<String>(wireGroups({}));
    final entered = Completer<void>();
    f.pending = (call) {
      if (call.method == 'findMessageList') { entered.complete(); return response.future; }
      return null;
    };
    final pending = f.repository.readExact(hit);
    final checked = expectLater(pending, throwsA(isA<AppFailure>()));
    await entered.future.timeout(const Duration(seconds: 2));
    final clearing = f.sdk.gateway.clearConversationMessages(f.sdk.session.state.generation, cidA);
    response.complete(wireGroups({cidA: [searchText('a1', 'match late')]}));
    await checked; await clearing;
    expect(f.repository.selection(hit).isCurrent(), isFalse);
  });
  test('query cancellation after dispatch rejects response without mapping it', () async {
    final response = f.defer<String>(wireGroups({}));
    final entered = Completer<void>(); var active = true;
    f.pending = (call) {
      if (call.method == 'searchLocalMessages') { entered.complete(); return response.future; }
      return null;
    };
    final pending = f.repository.messages(f.repository.begin('match', () => active), GlobalSearchKind.text);
    final checked = expectLater(pending, throwsA(isA<AppFailure>()));
    await entered.future.timeout(const Duration(seconds: 2)); active = false;
    response.complete(wireGroups({cidA: [searchText('late-id', 'match')]}));
    await checked;
    expect(f.sdk.gateway.privacyLedger.lookup('late-id').inspectionComplete, isFalse);
  });
  for (final bad in ['{"totalCount":1}',
    '{"totalCount":1,"searchResultItems":[{"conversationID":"","messageCount":1,"messageList":[]}]}']) {
    test('malformed global structure is an error rather than a successful empty search: $bad', () async {
      f.pending = (call) => call.method == 'searchLocalMessages' ? Future.value(bad) : null;
      await expectLater(f.repository.messages(f.repository.begin('match', () => true), GlobalSearchKind.text),
          throwsA(isA<AppFailure>().having((e) => e.kind, 'kind', FailureKind.invalidResponse)));
    });
  }
}
