import 'dart:async';
import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/domain/chat_search.dart';
import 'package:imapp/features/chat/presentation/chat_search_controller.dart';

import '../../core/im/sdk_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness harness;
  late ChatRepository repository;
  late ChatSearchController controller;
  Map<String, dynamic> hit(String id, String text, {int type = 101}) => {
    ...SdkHarness.message(id: id, text: text),
    'contentType': type,
  };
  String page(List<Map<String, dynamic>> values, {String? other}) =>
      jsonEncode({
        'totalCount': values.length,
        'searchResultItems': [
          if (other != null)
            {
              'conversationID': 'other',
              'messageList': [hit('foreign', other)],
            },
          {'conversationID': 'single-alice-bob', 'messageList': values},
        ],
      });
  setUp(() async {
    harness = SdkHarness()..conversations.add(SdkHarness.conversation());
    await harness.start();
    repository = ChatRepository(
      gateway: harness.gateway,
      session: harness.session,
      conversationId: 'single-alice-bob',
    );
    controller = ChatSearchController(repository, pageSize: 2);
  });
  tearDown(() async {
    controller.dispose();
    repository.clear();
    await harness.dispose();
  });

  test('search codec freezes conversation and time; foreign/deleted results do not escape', () async {
    harness.overrideCall = (call) async {
      if (call.method == 'searchLocalMessages') {
        return page([
          hit('kept', '目标结果'),
          {...hit('deleted', '已删除'), 'status': 4},
          hit('kept', '目标结果'),
        ], other: '另一会话的私密结果');
      }
      return harness.respond(call);
    };
    final result = await repository.search(
      query: ' 目标 ',
      filter: ChatHistoryFilter.all,
      timePosition: 1700000000,
      pageIndex: 3,
      count: 30,
    );
    expect(result.messages.map((item) => item.id), ['kept']);
    final args =
        harness.calls
                .lastWhere((call) => call.method == 'searchLocalMessages')
                .arguments
            as Map;
    expect(args['filter'], containsPair('conversationID', 'single-alice-bob'));
    expect(args['filter']['keywordList'], ['目标']);
    expect(args['filter']['pageIndex'], 3);
    expect(args['filter']['searchTimePosition'], 1700000000);
    await expectLater(
      repository.favoriteSnapshot('foreign'),
      throwsA(isA<AppFailure>()),
    );
  });

  test('media browse never asks unsupported native keyword search and filters wrong types', () async {
    harness.overrideCall = (call) async {
      if (call.method == 'searchLocalMessages') {
        return page([hit('image', '', type: 102), hit('text', '不应出现在媒体结果')]);
      }
      return harness.respond(call);
    };
    await controller.search(query: '旧关键词', filter: ChatHistoryFilter.media);
    expect(controller.query, isEmpty);
    expect(controller.messages.map((item) => item.id), ['image']);
    final filter =
        (harness.calls
                    .lastWhere((call) => call.method == 'searchLocalMessages')
                    .arguments
                as Map)['filter']
            as Map;
    expect(filter['keywordList'], isEmpty);
    expect(filter['messageTypeList'], [102, 104]);
  });

  test('query change discards a slow earlier search and keeps only the latest query', () async {
    final old = Completer<String>();
    harness.overrideCall = (call) async {
      if (call.method == 'searchLocalMessages') {
        final filter = (call.arguments as Map)['filter'] as Map;
        if ((filter['keywordList'] as List).single == '旧') return old.future;
        return page([hit('new', '新结果')]);
      }
      return harness.respond(call);
    };
    final pending = controller.search(query: '旧');
    await Future<void>.delayed(Duration.zero);
    await controller.search(query: '新');
    old.complete(page([hit('old', '旧结果')]));
    await pending;
    expect(controller.messages.map((item) => item.id), ['new']);
    expect(controller.query, '新');
    expect(controller.loading, isFalse);
  });

  test(
    'paging de-duplicates overlap and retains the first search time boundary',
    () async {
      harness.overrideCall = (call) async {
        if (call.method == 'searchLocalMessages') {
          final index = ((call.arguments as Map)['filter'] as Map)['pageIndex'];
          return index == 1
              ? page([hit('one', '一'), hit('two', '二')])
              : page([hit('two', '二')]);
        }
        return harness.respond(call);
      };
      await controller.search(query: '查找');
      expect(controller.hasMore, isTrue);
      await controller.loadMore();
      expect(controller.messages.map((item) => item.id).toSet(), {
        'one',
        'two',
      });
      expect(controller.hasMore, isFalse);
      final filters = harness.calls
          .where((call) => call.method == 'searchLocalMessages')
          .map((call) => (call.arguments as Map)['filter'] as Map)
          .toList();
      expect(filters.map((item) => item['pageIndex']), [1, 2]);
      expect(
        filters.map((item) => item['searchTimePosition']).toSet(),
        hasLength(1),
      );
    },
  );

  test('failed next page keeps results and retries that same page without raw error text', () async {
    var attempts = 0;
    harness.overrideCall = (call) async {
      if (call.method == 'searchLocalMessages') {
        final index = ((call.arguments as Map)['filter'] as Map)['pageIndex'];
        if (index == 1) return page([hit('one', '一'), hit('two', '二')]);
        if (++attempts == 1) {
          throw PlatformException(
            code: '10005',
            message: 'private-token internal error',
          );
        }
        return page([hit('three', '三')]);
      }
      return harness.respond(call);
    };
    await controller.search(query: '查找');
    await controller.loadMore();
    expect(controller.messages, hasLength(2));
    expect(controller.failure?.kind, FailureKind.unknown);
    expect(controller.failure.toString(), isNot(contains('private-token')));
    await controller.loadMore();
    expect(controller.messages, hasLength(3));
    expect(controller.failure, isNull);
    final indices = harness.calls
        .where((call) => call.method == 'searchLocalMessages')
        .map((call) => ((call.arguments as Map)['filter'] as Map)['pageIndex']);
    expect(indices, [1, 2, 2]);
  });

  test('sign out discards a late result and an old search cannot dispatch in a new account', () async {
    final pending = Completer<String>();
    harness.overrideCall = (call) async {
      if (call.method == 'searchLocalMessages') return pending.future;
      return harness.respond(call);
    };
    final searching = controller.search(query: '旧账号内容');
    await Future<void>.delayed(Duration.zero);
    await harness.session.signOut();
    pending.complete(page([hit('late', '旧账号迟返内容')]));
    await searching;
    await Future<void>.delayed(Duration.zero);
    expect(controller.messages, isEmpty);
    expect(controller.query, isEmpty);
    harness.userId = 'charlie';
    await harness.session.signIn((_) async => harness.credentials);
    final count = harness.calls
        .where((call) => call.method == 'searchLocalMessages')
        .length;
    await controller.search(query: '不能跨代');
    expect(
      harness.calls.where((call) => call.method == 'searchLocalMessages'),
      hasLength(count),
    );
  });
}
