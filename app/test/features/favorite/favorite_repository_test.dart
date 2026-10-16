import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/favorite/domain/favorite.dart';
import 'package:imapp/features/favorite/presentation/favorite_list_controller.dart';

import 'favorite_harness.dart';

void main() {
  late FavoriteHarness fixture;
  setUp(() async {
    fixture = FavoriteHarness()..items.add(FavoriteHarness.item('one'));
    await fixture.start();
  });
  tearDown(() => fixture.dispose());
  test('server-isolated favorites cannot be edited or batch-edited after reading, but explicit deletion remains possible', () async {
    fixture.items.single['retentionBlocked'] = true;
    final item = await fixture.repository.info('one');
    expect(item.canUseContent, false);
    final before = fixture.http.requests.length;
    await expectLater(
      fixture.repository.update('one', notes: 'new'),
      throwsA(isA<AppFailure>()),
    );
    await expectLater(
      fixture.repository.batch(['one'], operation: 2, tags: ['new']),
      throwsA(isA<AppFailure>()),
    );
    expect(fixture.http.requests.length, before);
    await fixture.repository.remove(['one']);
    expect(fixture.items, isEmpty);
  });
  test('direct favorite API entry rejects private root and quoted content before HTTP without blocking normal encrypted messages', () async {
    final private = {
      'clientMsgID': 'private-source',
      'attachedInfoElem': {'isPrivateChat': true},
    };
    for (final content in [
      private,
      {
        'clientMsgID': 'quote',
        'quoteElem': {'quoteMessage': private},
      },
    ]) {
      final before = fixture.http.requests.length;
      await expectLater(
        fixture.repository.add(
          kind: FavoriteKind.message,
          sourceId: 'source',
          content: jsonEncode(content),
        ),
        throwsA(isA<AppFailure>()),
      );
      expect(fixture.http.requests.length, before);
    }
    await fixture.repository.add(
      kind: FavoriteKind.message,
      sourceId: 'encrypted-source',
      content: jsonEncode({'clientMsgID': 'encrypted-source', 'keyVersion': 8}),
    );
    expect(fixture.http.requests.last.uri.path, endsWith('/favorite/add'));
  });
  test('source lookup requires the exact message, conversation and type rather than a partial keyword match', () async {
    final repo = fixture.repository;
    expect(
      (await repo.findSource(
        'source-one',
        'conversation-a',
        FavoriteKind.message,
      ))?.id,
      'one',
    );
    expect(
      await repo.findSource(
        'source-one',
        'different-conversation',
        FavoriteKind.message,
      ),
      isNull,
    );
    expect(
      await repo.findSource('source-one', 'conversation-a', FavoriteKind.video),
      isNull,
    );
    fixture.overrideRequest = (_) async => {
      'favorites': [FavoriteHarness.item('different-source')],
    };
    await expectLater(
      repo.findSource('source-one', 'conversation-a', FavoriteKind.message),
      throwsA(isA<AppFailure>()),
    );
  });
  test('mismatched detail IDs and foreign or duplicate statistics categories fail closed', () async {
    fixture.overrideRequest = (_) async => {
      'favoriteInfo': FavoriteHarness.item('different'),
    };
    await expectLater(
      fixture.repository.info('one'),
      throwsA(isA<AppFailure>()),
    );
    fixture.overrideRequest = (_) async => {
      'categoriesWithCount': [
        {...fixture.categories.last, 'userID': 'bob'},
      ],
    };
    await expectLater(fixture.repository.stats(), throwsA(isA<AppFailure>()));
    fixture.overrideRequest = (_) async => {
      'categoriesWithCount': [fixture.categories.last, fixture.categories.last],
    };
    await expectLater(fixture.repository.stats(), throwsA(isA<AppFailure>()));
    fixture.overrideRequest = (_) async => {
      'lastFavoriteTime': 8640000000000001,
    };
    await expectLater(fixture.repository.stats(), throwsA(isA<AppFailure>()));
  });
  test('all 12 favorite endpoints keep the IM domain token and fixed owner; omitted fields remain absent', () async {
    final repo = fixture.repository;
    await repo.list();
    await repo.list(query: const FavoriteQuery(keyword: '设计'));
    await repo.info('one');
    await repo.add(
      kind: FavoriteKind.message,
      sourceId: 'native-id',
      content: '{"cloudCustomData":"keep-me"}',
    );
    await repo.update('one', notes: '', tags: []);
    final update = fixture.http.requests.last.data as Map;
    expect(update['notes'], '');
    expect(update['tags'], isEmpty);
    expect(update.containsKey('title'), isFalse);
    expect(update.containsKey('categoryID'), isFalse);
    await repo.batch(['one', 'one'], operation: 1, categoryId: 'work');
    expect(fixture.http.requests.last.data['favoriteIDs'], ['one']);
    await repo.remove(['added']);
    await repo.stats();
    await repo.categories();
    await repo.createCategory(' 新分类 ');
    await repo.updateCategory('work', color: '#2563EB', order: 8);
    await repo.deleteCategory('work', moveToDefault: false);
    expect(
      fixture.http.requests.map((request) => request.uri.path).toSet(),
      hasLength(12),
    );
    for (final request in fixture.http.requests) {
      expect(request.uri.origin, 'https://im.example.com');
      expect(request.headers['token'], 'fixture-im');
      expect(request.data['userID'], 'alice');
    }
    expect(fixture.http.requests.last.data['moveToDefault'], isFalse);
  });
  test('default category cannot be renamed or deleted and page/batch bounds fail before network', () async {
    final repo = fixture.repository;
    await expectLater(
      repo.updateCategory('default', name: '改名'),
      throwsA(isA<AppFailure>()),
    );
    await expectLater(
      repo.deleteCategory('default', moveToDefault: false),
      throwsA(isA<AppFailure>()),
    );
    await expectLater(repo.list(page: 0), throwsA(isA<AppFailure>()));
    await expectLater(
      repo.remove(List.generate(101, (i) => '$i')),
      throwsA(isA<AppFailure>()),
    );
    expect(fixture.http.requests, isEmpty);
  });
  test(
    'foreign owner and malformed list never become visible empty success',
    () async {
      fixture.overrideRequest = (_) async => {
        'favorites': [
          {...FavoriteHarness.item('foreign'), 'userID': 'bob'},
        ],
        'total': 1,
        'hasMore': false,
      };
      await expectLater(
        fixture.repository.list(),
        throwsA(
          isA<AppFailure>().having(
            (e) => e.kind,
            'kind',
            FailureKind.invalidResponse,
          ),
        ),
      );
      fixture.overrideRequest = (_) async => {'total': 0, 'hasMore': false};
      await expectLater(fixture.repository.list(), throwsA(isA<AppFailure>()));
    },
  );
  test('metadata validates UTF8 lengths and malformed timestamps rather than failing during UI rendering', () async {
    await expectLater(
      fixture.repository.update('one', title: List.filled(400, '中').join()),
      throwsA(isA<AppFailure>()),
    );
    expect(fixture.http.requests, isEmpty);
    expect(
      () => FavoriteItem.fromJson({
        ...FavoriteHarness.item('broken'),
        'createTime': 99999999999999999,
      }, 'alice'),
      throwsA(isA<AppFailure>()),
    );
  });
  test('full native message JSON remains unchanged and embedded local paths never become playable media', () {
    const raw =
        '{"clientMsgID":"native","cloudCustomData":"opaque","keyVersion":8,"textElem":{"content":"真实文本"},"fileElem":{"filePath":"/private/other-account/file","fileName":"材料"}}';
    final item = FavoriteItem.fromJson(
      FavoriteHarness.item('wire', content: raw),
      'alice',
    );
    expect(item.content, raw);
    expect(item.toJson()['content'], raw);
    expect(item.decoded.summary, '真实文本');
    expect(item.decoded.media, isNull);
    expect(
      FavoriteContent.decode('file:///private/file', FavoriteKind.file).media,
      isNull,
    );
    expect(
      FavoriteContent.decode('javascript:alert(1)', FavoriteKind.link).media,
      isNull,
    );
  });
  test(
    'load-more failure keeps previous rows and retries the failed page',
    () async {
      fixture.items
        ..clear()
        ..addAll(List.generate(35, (i) => FavoriteHarness.item('$i')));
      final controller = FavoriteListController(fixture.repository);
      await controller.refresh();
      expect(controller.items, hasLength(30));
      fixture.overrideRequest = (request) async {
        throw DioException(
          requestOptions: request,
          type: DioExceptionType.connectionError,
        );
      };
      await controller.next();
      expect(controller.items, hasLength(30));
      expect(controller.failure?.kind, FailureKind.network);
      fixture.overrideRequest = null;
      await controller.next();
      expect(controller.items, hasLength(35));
      expect(controller.more, isFalse);
      expect(controller.failure, isNull);
      controller.dispose();
    },
  );
  test(
    'a false success acknowledgement never counts as a completed mutation',
    () async {
      fixture.overrideRequest = (_) async => {'success': false};
      await expectLater(
        fixture.repository.remove(['one']),
        throwsA(
          isA<AppFailure>().having(
            (e) => e.kind,
            'kind',
            FailureKind.invalidResponse,
          ),
        ),
      );
    },
  );
  test('new search wins over a late previous query', () async {
    final old = Completer<Object?>();
    final started = Completer<void>();
    fixture.overrideRequest = (request) async {
      if (request.data['keyword'] == '旧') {
        started.complete();
        return old.future;
      }
      return {
        'favorites': [FavoriteHarness.item('new')],
        'total': 1,
        'hasMore': false,
      };
    };
    final controller = FavoriteListController(fixture.repository);
    final first = controller.filter(const FavoriteQuery(keyword: '旧'));
    await started.future;
    await controller.filter(const FavoriteQuery(keyword: '新'));
    old.complete({
      'favorites': [FavoriteHarness.item('old')],
      'total': 1,
      'hasMore': false,
    });
    await first;
    expect(controller.items.single.id, 'new');
    controller.dispose();
  });
  test('logout immediately clears favorite selection and late data, old repository cannot dispatch after another account signs in', () async {
    final controller = FavoriteListController(fixture.repository);
    await controller.refresh();
    controller.selectionMode(true);
    controller.toggle('one');
    final old = Completer<Object?>();
    final started = Completer<void>();
    fixture.overrideRequest = (_) {
      started.complete();
      return old.future;
    };
    final refreshing = controller.refresh();
    await started.future;
    await fixture.session.signOut();
    expect(controller.items, isEmpty);
    expect(controller.selected, isEmpty);
    old.complete({
      'favorites': [FavoriteHarness.item('old-private')],
      'total': 1,
      'hasMore': false,
    });
    await refreshing;
    await fixture.session.signIn((_) async => fixture.credentials('bob'));
    final before = fixture.http.requests.length;
    await expectLater(
      fixture.repository.remove(['one']),
      throwsA(isA<AppFailure>()),
    );
    expect(fixture.http.requests.length, before);
    expect(controller.items, isEmpty);
    controller.dispose();
  });
}
