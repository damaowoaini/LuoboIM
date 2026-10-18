import 'dart:convert';
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/favorite/data/favorite_repository.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/features/favorite/presentation/favorite_forward_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/sdk_harness.dart';
import 'favorite_harness.dart';

void main() {
  late FavoriteHarness fixture;
  late SdkHarness sdk;
  late FavoriteRepository repository;
  final source = SdkHarness.message(
    id: 'shared-source',
    text: 'forward-sensitive-body',
  );
  Future<void> start() async {
    fixture = FavoriteHarness()
      ..items.add({
        ...FavoriteHarness.item(
          'one',
          title: 'forward-sensitive-title',
          content: jsonEncode(source),
        ),
        'sourceID': 'shared-source',
        'conversationID': 'single-alice-bob',
      });
    sdk = SdkHarness()
      ..conversations.addAll([
        {
          ...SdkHarness.conversation(id: 'target', title: '同名收件人'),
          'userID': 'carol',
        },
        {
          ...SdkHarness.conversation(
            id: 'target-group',
            title: '同名收件人',
            group: true,
          ),
          'groupID': 'team-red',
        },
      ]);
    await sdk.start();
    await fixture.start();
    repository = FavoriteRepository(
      fixture.api,
      privacyLedger: sdk.gateway.privacyLedger,
    );
    sdk.overrideCall = (call) async {
      if (call.method == 'createForwardMessage') {
        return jsonEncode({
          ...Map<String, dynamic>.from(
            (call.arguments as Map)['message'] as Map,
          ),
          'clientMsgID': 'new-forward-message',
          'sendID': 'alice',
        });
      }
      return sdk.respond(call);
    };
  }

  Future<void> mount(WidgetTester tester) async {
    await tester.runAsync(start);
    final item = await tester.runAsync(() => repository.info('one'));
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          appRuntimeProvider.overrideWith((_) => fixture.runtime),
          imGatewayProvider.overrideWithValue(sdk.gateway),
        ],
        child: MaterialApp(
          home: FavoriteForwardPage(item: item!, repository: repository),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  Future<void> confirmation(WidgetTester tester) async {
    await tester.enterText(find.byType(TextField), 'CAROL');
    await tester.pumpAndSettle();
    await tester.tap(find.text('同名收件人'));
    await tester.pumpAndSettle();
    expect(find.text('账号 ID：carol\n\n确认转发这条消息。'), findsOneWidget);
    expect(find.textContaining('forward-sensitive'), findsNothing);
  }

  Future<void> close(WidgetTester tester) async {
    await tester.runAsync(() async {
      await sdk.dispose();
      await fixture.dispose();
    });
    await tester.pumpWidget(const SizedBox.shrink());
  }

  testWidgets(
    'favorite chooser exposes stable user/group identity and only searches displayed names or account IDs',
    (tester) async {
      await mount(tester);
      try {
        expect(find.text('同名收件人'), findsNWidgets(2));
        expect(find.text('账号 ID：carol'), findsOneWidget);
        expect(find.text('群 ID：team-red'), findsOneWidget);
        await tester.enterText(find.byType(TextField), 'target-group');
        await tester.pumpAndSettle();
        expect(find.text('同名收件人'), findsNothing);
        await tester.enterText(find.byType(TextField), 'TEAM-RED');
        await tester.pumpAndSettle();
        expect(find.text('同名收件人'), findsOneWidget);
        expect(find.text('群 ID：team-red'), findsOneWidget);
      } finally {
        await close(tester);
      }
    },
  );
  for (final boundary in [
    'target identity',
    'source denial',
    'history clear',
    'during create',
  ]) {
    testWidgets(
      'favorite forwarding refuses $boundary changes after the confirmed lease',
      (tester) async {
        await mount(tester);
        try {
          await confirmation(tester);
          if (boundary == 'target identity') {
            sdk.conversations[0]['userID'] = 'replacement-account';
          }
          if (boundary == 'source denial') {
            sdk.gateway.privacyLedger.denyFavoriteRetention(
              'one',
              'shared-source',
            );
          }
          if (boundary == 'history clear') {
            await tester.runAsync(
              () => sdk.gateway.clearConversationMessages(
                sdk.session.state.generation,
                'single-alice-bob',
              ),
            );
          }
          if (boundary == 'during create') {
            final original = sdk.overrideCall!;
            sdk.overrideCall = (call) async {
              if (call.method == 'createForwardMessage') {
                sdk.gateway.privacyLedger.denyFavoriteRetention(
                  'one',
                  'shared-source',
                );
              }
              return original(call);
            };
          }
          await tester.tap(find.widgetWithText(TextButton, '发送'));
          await tester.pumpAndSettle();
          expect(sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
          expect(
            sdk.calls.where((c) => c.method == 'createForwardMessage'),
            hasLength(boundary == 'during create' ? 1 : 0),
          );
        } finally {
          await close(tester);
        }
      },
    );
  }
  testWidgets(
    'ordinary favorite forwarding still reaches exactly the confirmed account once',
    (tester) async {
      await mount(tester);
      try {
        await confirmation(tester);
        await tester.tap(find.widgetWithText(TextButton, '发送'));
        await tester.pumpAndSettle();
        final sent = sdk.calls.where((c) => c.method == 'sendMessage').toList();
        expect(sent, hasLength(1));
        expect((sent.single.arguments as Map)['userID'], 'carol');
      } finally {
        await close(tester);
      }
    },
  );

  testWidgets(
    'favorite source route cannot revive after foreign coverage while native create waits',
    (tester) async {
      await mount(tester);
      final entered = Completer<void>(), release = Completer<void>();
      final original = sdk.overrideCall!;
      sdk.overrideCall = (call) async {
        if (call.method == 'createForwardMessage') {
          if (!entered.isCompleted) {
            entered.complete();
          }
          await release.future;
        }
        return original(call);
      };
      try {
        await confirmation(tester);
        await tester.tap(find.widgetWithText(TextButton, '发送'));
        for (var i = 0; i < 20 && !entered.isCompleted; i++) {
          await tester.pump(const Duration(milliseconds: 50));
        }
        expect(entered.isCompleted, isTrue);
        final navigator = Navigator.of(
          tester.element(find.byType(FavoriteForwardPage)),
        );
        final covered = navigator.push<void>(
          MaterialPageRoute<void>(
            builder: (_) => const Scaffold(body: Text('另一个页面')),
          ),
        );
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 350));
        navigator.pop();
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 350));
        await covered;
        release.complete();
        await tester.pumpAndSettle();
        expect(find.text('此转发已失效，请重新确认收件人。'), findsOneWidget);
        expect(
          sdk.calls.where((c) => c.method == 'createForwardMessage'),
          hasLength(1),
        );
        expect(sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
        expect(find.textContaining('转发结果未确认'), findsNothing);
        await confirmation(tester);
        await tester.tap(find.widgetWithText(TextButton, '发送'));
        await tester.pumpAndSettle();
        expect(
          sdk.calls.where((c) => c.method == 'createForwardMessage'),
          hasLength(2),
        );
        expect(sdk.calls.where((c) => c.method == 'sendMessage'), hasLength(1));
        expect(tester.takeException(), isNull);
      } finally {
        if (!release.isCompleted) {
          release.complete();
        }
        await close(tester);
      }
    },
  );

  testWidgets(
    'favorite rechecks the confirmed destination after native creation before dispatch',
    (tester) async {
      await mount(tester);
      final original = sdk.overrideCall!;
      sdk.overrideCall = (call) async {
        final result = await original(call);
        if (call.method == 'createForwardMessage') {
          sdk.conversations[0]['userID'] = 'replacement-account';
        }
        return result;
      };
      try {
        await confirmation(tester);
        await tester.tap(find.widgetWithText(TextButton, '发送'));
        await tester.pumpAndSettle();
        expect(
          sdk.calls.where((c) => c.method == 'createForwardMessage'),
          hasLength(1),
        );
        expect(sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
        expect(find.textContaining('转发结果未确认'), findsNothing);
        expect(tester.takeException(), isNull);
      } finally {
        await close(tester);
      }
    },
  );

  testWidgets(
    'favorite unknown dispatch blocks replay and reviews only its original confirmed account',
    (tester) async {
      await mount(tester);
      final original = sdk.overrideCall!;
      sdk.overrideCall = (call) async {
        if (call.method == 'sendMessage') {
          throw PlatformException(code: '10005');
        }
        return original(call);
      };
      try {
        await confirmation(tester);
        final originalTap = tester
            .widget<ListTile>(
              find.ancestor(
                of: find.text('同名收件人'),
                matching: find.byType(ListTile),
              ),
            )
            .onTap!;
        await tester.tap(find.widgetWithText(TextButton, '发送'));
        await tester.pumpAndSettle();
        expect(find.textContaining('转发结果未确认'), findsWidgets);
        expect(
          tester.widget<TextField>(find.byType(TextField)).enabled,
          isFalse,
        );
        originalTap();
        await tester.pumpAndSettle();
        expect(
          sdk.calls.where((c) => c.method == 'createForwardMessage'),
          hasLength(1),
        );
        expect(sdk.calls.where((c) => c.method == 'sendMessage'), hasLength(1));
        expect(find.text('转发给 同名收件人？'), findsNothing);
        await tester.tap(find.text('前往核对 · 账号 ID：carol'));
        await tester.pumpAndSettle();
        final reviewPage = tester.widget<ChatPage>(find.byType(ChatPage));
        expect(reviewPage.conversationId, 'target');
        expect(reviewPage.sourceCurrent, isNotNull);
        expect(reviewPage.sourceCurrent!(), isTrue);
        await tester.runAsync(fixture.session.signOut);
        expect(reviewPage.sourceCurrent!(), isFalse);
        expect(sdk.calls.where((c) => c.method == 'sendMessage'), hasLength(1));
        expect(tester.takeException(), isNull);
      } finally {
        await close(tester);
      }
    },
  );

  testWidgets(
    'favorite preserves a definitive friendship rejection without creating a forward hint or unknown result',
    (tester) async {
      await mount(tester);
      final original = sdk.overrideCall!;
      sdk.overrideCall = (call) async {
        if (call.method == 'sendMessage') {
          throw PlatformException(code: '1302');
        }
        return original(call);
      };
      try {
        await confirmation(tester);
        await tester.tap(find.widgetWithText(TextButton, '发送'));
        await tester.pumpAndSettle();
        expect(sdk.calls.where((c) => c.method == 'sendMessage'), hasLength(1));
        expect(find.textContaining('转发结果未确认'), findsNothing);
        expect(find.textContaining('前往核对'), findsNothing);
        expect(
          tester
              .widget<ListTile>(
                find.ancestor(
                  of: find.text('同名收件人'),
                  matching: find.byType(ListTile),
                ),
              )
              .onTap,
          isNotNull,
        );
        expect(
          sdk.calls.where(
            (c) =>
                c.method == 'createCustomMessage' ||
                c.method == 'insertSingleMessageToLocalStorage',
          ),
          isEmpty,
        );
        expect(tester.takeException(), isNull);
      } finally {
        await close(tester);
      }
    },
  );
}
