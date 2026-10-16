import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/favorite/data/favorite_exporter.dart';
import 'package:imapp/features/favorite/data/favorite_repository.dart';
import 'package:imapp/features/favorite/favorite_providers.dart';
import 'package:imapp/features/favorite/presentation/favorite_detail_page.dart';
import 'package:imapp/features/favorite/presentation/favorites_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/sdk_harness.dart';
import 'favorite_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late FavoriteHarness fixture;
  late SdkHarness sdk;
  Map<String, dynamic> wire({bool private = false, int? read}) => {
    ...SdkHarness.message(id: 'shared-source', text: 'favorite-sensitive-body'),
    'attachedInfoElem': {
      'isPrivateChat': private,
      'hasReadTime': ?read,
      'burnDuration': 30,
    },
  };
  Map<String, Object?> row() => {
    ...FavoriteHarness.item(
      'one',
      title: 'favorite-sensitive-title',
      content: jsonEncode(wire()),
    ),
    'sourceID': 'shared-source',
    'conversationID': 'single-alice-bob',
  };
  Future<void> start() async {
    fixture = FavoriteHarness()..items.add(row());
    sdk = SdkHarness()..conversations.add(SdkHarness.conversation());
    await sdk.start();
    await fixture.start();
    expect(
      sdk.credentials.accountKey,
      fixture.api.access.credentials.accountKey,
    );
  }

  ChatRepository chat() => ChatRepository(
    gateway: sdk.gateway,
    session: sdk.session,
    conversationId: 'single-alice-bob',
  );
  overrides() => [
    appRuntimeProvider.overrideWith((_) => fixture.runtime),
    tenantApiProvider.overrideWithValue(fixture.api),
    imGatewayProvider.overrideWithValue(sdk.gateway),
  ];
  Future<void> close() async {
    await sdk.dispose();
    await fixture.dispose();
  }

  for (final state in ['private', 'expired', 'ordinary']) {
    testWidgets(
      'actual favorite copy/title/detail share remembered $state policy despite omitted snapshot flags',
      (tester) async {
        await tester.runAsync(start);
        sdk.messages.add(
          wire(
            private: state != 'ordinary',
            read: state == 'expired' ? 1700000000000 : null,
          ),
        );
        await tester.runAsync(() => chat().history());
        var clipboard = 0;
        tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
          SystemChannels.platform,
          (call) async {
            if (call.method == 'Clipboard.setData') clipboard++;
            return null;
          },
        );
        addTearDown(
          () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
            SystemChannels.platform,
            null,
          ),
        );
        try {
          await tester.pumpWidget(
            ProviderScope(
              overrides: overrides(),
              child: const MaterialApp(
                home: FavoriteDetailPage(favoriteId: 'one'),
              ),
            ),
          );
          await tester.pumpAndSettle();
          final copy = find.text('复制完整内容');
          final wasVisible = find
              .text('favorite-sensitive-title')
              .evaluate()
              .isNotEmpty;
          if (state == 'ordinary') expect(wasVisible, true);
          if (wasVisible) {
            await tester.scrollUntilVisible(
              copy,
              180,
              maxScrolls: 30,
              scrollable: find.byType(Scrollable).first,
            );
            await tester.ensureVisible(copy);
            await tester.tap(copy);
            await tester.pumpAndSettle();
          }
          expect(clipboard, state == 'ordinary' ? 1 : 0);
          if (state != 'ordinary') {
            expect(find.textContaining('favorite-sensitive'), findsNothing);
            expect(find.text('删除收藏'), findsOneWidget);
            expect(find.byTooltip('导出此条收藏'), findsNothing);
          }
        } finally {
          await tester.runAsync(close);
          await tester.pumpWidget(const SizedBox.shrink());
        }
      },
    );
  }
  testWidgets(
    'opened favorite detail withdraws its body when chat later learns private metadata without refetch',
    (tester) async {
      await tester.runAsync(start);
      sdk.messages.add(wire());
      await tester.runAsync(() => chat().history());
      try {
        await tester.pumpWidget(
          ProviderScope(
            overrides: overrides(),
            child: const MaterialApp(
              home: FavoriteDetailPage(favoriteId: 'one'),
            ),
          ),
        );
        await tester.pumpAndSettle();
        expect(find.text('favorite-sensitive-title'), findsWidgets);
        final before = fixture.http.requests
            .where((r) => r.uri.path.endsWith('/favorite/info'))
            .length;
        sdk.messages[0] = wire(private: true);
        await tester.runAsync(() => chat().history());
        await tester.pump();
        await tester.pumpAndSettle();
        expect(find.textContaining('favorite-sensitive'), findsNothing);
        expect(find.text('复制完整内容'), findsNothing);
        expect(
          fixture.http.requests.where(
            (r) => r.uri.path.endsWith('/favorite/info'),
          ),
          hasLength(before),
        );
      } finally {
        await tester.runAsync(close);
        await tester.pumpWidget(const SizedBox.shrink());
      }
    },
  );
  test('actual favorite repository rejects a remembered private snapshot without editing or forwarding it', () async {
    await start();
    final container = ProviderContainer(overrides: overrides());
    try {
      sdk.messages.add(wire(private: true));
      await chat().history();
      final repo = container.read(favoriteRepositoryProvider)!;
      final item = await repo.info('one');
      expect(item.canUseContent, false);
      expect(item.decoded.canForward, false);
      expect(item.displayTitle, isNot(contains('favorite-sensitive')));
    } finally {
      container.dispose();
      await close();
    }
  });
  test('ordinary legacy URL favorites without a native message identity stay usable', () async {
    await start();
    final container = ProviderContainer(overrides: overrides());
    try {
      fixture.items[0] = {
        ...row(),
        'sourceID': 'legacy-upload',
        'favoriteType': 2,
        'content': 'https://files.example.com/ordinary.txt',
      };
      final item = await container
          .read(favoriteRepositoryProvider)!
          .info('one');
      expect(item.canUseContent, true);
      expect(item.decoded.media, isNotNull);
    } finally {
      container.dispose();
      await close();
    }
  });
  for (final identity in ['favorite', 'source']) {
    test(
      'server retention denial survives omitted flags and repository remount by $identity identity without hiding ordinary chat',
      () async {
        await start();
        try {
          final ledger = sdk.gateway.privacyLedger;
          fixture.items[0] = {...row(), 'retentionBlocked': true};
          final first = FavoriteRepository(fixture.api, privacyLedger: ledger);
          expect((await first.info('one')).canUseContent, false);
          fixture.items[0] = {
            ...row(),
            if (identity == 'source') 'favoriteID': 'new-row',
            if (identity == 'favorite') 'sourceID': 'new-source',
          };
          final second = FavoriteRepository(fixture.api, privacyLedger: ledger);
          if (identity == 'favorite') {
            await expectLater(
              second.update('one', title: 'must-not-save'),
              throwsA(isA<AppFailure>()),
            );
            expect(
              fixture.http.requests.where(
                (r) => r.uri.path.endsWith('/favorite/update'),
              ),
              isEmpty,
            );
          }
          final restored = await second.info(
            identity == 'source' ? 'new-row' : 'one',
          );
          expect(restored.canUseContent, false);
          expect(restored.decoded.canForward, false);
          sdk.messages.add(wire());
          final ordinary = (await chat().history()).messages.single;
          expect(ordinary.privacy.containsPrivateContent, false);
          expect(ordinary.privacy.canViewAt(DateTime.now()), true);
          expect(ordinary.privacy.allowsRetention, true);
        } finally {
          await close();
        }
      },
    );
  }
  test('private facts learned first from a favorite actual message survive mismatched source IDs in later favorites and omitted chat metadata', () async {
    await start();
    try {
      final ledger = sdk.gateway.privacyLedger;
      fixture.items[0] = {
        ...row(),
        'sourceID': 'different-legacy-source',
        'content': jsonEncode(wire(private: true, read: 1700000000000)),
      };
      final first = FavoriteRepository(fixture.api, privacyLedger: ledger);
      expect((await first.info('one')).canUseContent, false);
      final omitted = {...wire(), 'attachedInfoElem': <String, Object?>{}};
      fixture.items[0] = {
        ...row(),
        'favoriteID': 'second',
        'sourceID': 'another-legacy-source',
        'content': jsonEncode(omitted),
      };
      final other = await FavoriteRepository(
        fixture.api,
        privacyLedger: ledger,
      ).info('second');
      sdk.messages.add(omitted);
      final message = (await chat().history()).messages.single;
      expect(
        [
          other.canUseContent,
          message.privacy.containsPrivateContent,
          message.privacy.canViewAt(DateTime.now()),
        ],
        [false, true, false],
      );
    } finally {
      await close();
    }
  });
  testWidgets(
    'loaded favorite list withdraws subtitle and tags together with the title after a privacy update',
    (tester) async {
      await tester.runAsync(start);
      fixture.items[0]['tags'] = ['favorite-sensitive-tag'];
      sdk.messages.add(wire());
      await tester.runAsync(() => chat().history());
      try {
        await tester.pumpWidget(
          ProviderScope(
            overrides: overrides(),
            child: const MaterialApp(home: FavoritesPage()),
          ),
        );
        await tester.pumpAndSettle();
        expect(find.textContaining('favorite-sensitive'), findsWidgets);
        sdk.messages[0] = wire(private: true);
        await tester.runAsync(() => chat().history());
        await tester.pumpAndSettle();
        expect(find.textContaining('favorite-sensitive'), findsNothing);
        expect(find.text('私密收藏已隔离'), findsOneWidget);
      } finally {
        await tester.runAsync(close);
        await tester.pumpWidget(const SizedBox.shrink());
      }
    },
  );
  testWidgets(
    'an open favorite editor discards its text when shared privacy changes',
    (tester) async {
      await tester.runAsync(start);
      sdk.messages.add(wire());
      await tester.runAsync(() => chat().history());
      try {
        await tester.pumpWidget(
          ProviderScope(
            overrides: overrides(),
            child: const MaterialApp(
              home: FavoriteDetailPage(favoriteId: 'one'),
            ),
          ),
        );
        await tester.pumpAndSettle();
        final title = find.widgetWithText(ListTile, '标题');
        await tester.scrollUntilVisible(
          title,
          180,
          scrollable: find.byType(Scrollable).first,
        );
        await tester.tap(title);
        await tester.pumpAndSettle();
        await tester.enterText(
          find.byType(TextField),
          'unsubmitted-sensitive-edit',
        );
        sdk.messages[0] = wire(private: true);
        await tester.runAsync(() => chat().history());
        await tester.pumpAndSettle();
        expect(find.byType(TextField), findsNothing);
        expect(find.textContaining('unsubmitted-sensitive'), findsNothing);
        expect(find.text('内容已不可用'), findsOneWidget);
        expect(
          fixture.http.requests.where(
            (r) => r.uri.path.endsWith('/favorite/update'),
          ),
          isEmpty,
        );
      } finally {
        await tester.runAsync(close);
        await tester.pumpWidget(const SizedBox.shrink());
      }
    },
  );
  test('completed export retains a live source guard until explicit OS sharing and discards on rejection', () async {
    await start();
    final container = ProviderContainer(overrides: overrides());
    final directory = await Directory.systemTemp.createTemp(
      'luobo-final-export-',
    );
    try {
      sdk.messages.add(wire());
      await chat().history();
      final file = await FavoriteExporter(
        container.read(favoriteRepositoryProvider)!,
        directory: () async => directory,
      ).export();
      expect(File(file.path).existsSync(), true);
      file.check();
      sdk.gateway.privacyLedger.denyFavoriteRetention('one', 'shared-source');
      expect(file.check, throwsA(isA<AppFailure>()));
      await file.discard();
      expect(File(file.path).existsSync(), false);
    } finally {
      container.dispose();
      await close();
      await directory.delete(recursive: true);
    }
  });
  test('export discards previously written rows when their shared source becomes private during a later page', () async {
    await start();
    final container = ProviderContainer(overrides: overrides());
    final directory = await Directory.systemTemp.createTemp(
      'luobo-privacy-export-',
    );
    try {
      fixture.items.addAll(
        List.generate(100, (i) => FavoriteHarness.item('extra-$i')),
      );
      sdk.messages.add(wire());
      await chat().history();
      final entered = Completer<void>(), release = Completer<Object?>();
      Object? later;
      fixture.overrideRequest = (request) async {
        if (request.uri.path.endsWith('/favorite/list') &&
            request.data['pagination']['pageNumber'] == 2) {
          later = fixture.respond(request);
          entered.complete();
          return release.future;
        }
        return fixture.respond(request);
      };
      final future = FavoriteExporter(
        container.read(favoriteRepositoryProvider)!,
        directory: () async => directory,
      ).export();
      final rejected = expectLater(future, throwsA(isA<AppFailure>()));
      await entered.future;
      sdk.messages[0] = wire(private: true);
      await chat().history();
      release.complete(later);
      await rejected;
      expect(
        await directory.list(recursive: true).where((f) => f is File).toList(),
        isEmpty,
      );
    } finally {
      container.dispose();
      await close();
      await directory.delete(recursive: true);
    }
  });
}
