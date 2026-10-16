import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';

Map<String, dynamic> source(String shape) {
  final child = {
    ...SdkHarness.message(id: 'nested', text: 'private-child'),
    'attachedInfoElem': {'isPrivateChat': true},
  };
  final root = SdkHarness.message(id: 'source', text: 'private-followup-body');
  switch (shape) {
    case 'root':
      root['attachedInfoElem'] = {'isPrivateChat': true};
    case 'quote':
      root['quoteElem'] = {
        'text': 'private-followup-body',
        'quoteMessage': child,
      };
      root['contentType'] = 114;
    case 'atquote':
      root['atTextElem'] = {
        'text': 'private-followup-body',
        'atUsersInfo': [],
        'quoteMessage': child,
      };
      root['contentType'] = 106;
    case 'merge':
      root['mergeElem'] = {
        'title': 'private-followup-body',
        'multiMessage': [child],
      };
      root['contentType'] = 107;
    case 'ordinary':
      root['keyVersion'] = 3;
      root['attachedInfoElem'] = {'isPrivateChat': false};
  }
  return root;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness harness;
  setUp(() {
    harness = SdkHarness();
    harness.conversations.add(SdkHarness.conversation());
  });
  tearDown(() => harness.dispose());
  for (final shape in ['root', 'quote', 'atquote', 'merge']) {
    test(
      'private $shape cannot dispatch native quote creation or send',
      () async {
        harness.messages.add(source(shape));
        await harness.start();
        final repository = ChatRepository(
          gateway: harness.gateway,
          session: harness.session,
          conversationId: 'single-alice-bob',
        );
        await repository.conversation();
        await repository.history();
        await expectLater(
          repository.prepareText('new persistent reply', quoteId: 'source'),
          throwsA(isA<AppFailure>()),
        );
        expect(
          harness.calls.where(
            (c) =>
                c.method == 'createQuoteMessage' || c.method == 'sendMessage',
          ),
          isEmpty,
        );
      },
    );
  }
  test('ordinary encrypted messages remain quotable and sendable', () async {
    harness.messages.add(source('ordinary'));
    await harness.start();
    final repository = ChatRepository(
      gateway: harness.gateway,
      session: harness.session,
      conversationId: 'single-alice-bob',
    );
    await repository.conversation();
    await repository.history();
    final prepared = await repository.prepareText(
      'ordinary reply',
      quoteId: 'source',
    );
    await repository.send(prepared.id);
    expect(
      harness.calls.where((c) => c.method == 'createQuoteMessage'),
      hasLength(1),
    );
    expect(harness.calls.where((c) => c.method == 'sendMessage'), hasLength(1));
  });
  for (final private in [true, false]) {
    testWidgets(
      'actual copy control and Clipboard dispatch respect private=$private',
      (tester) async {
        harness.messages.add(source(private ? 'root' : 'ordinary'));
        harness.overrideCall = (call) async {
          if (call.method == 'findMessageList') {
            return jsonEncode({
              'findResultItems': [
                {
                  'conversationID': 'single-alice-bob',
                  'messageList': harness.messages,
                },
              ],
            });
          }
          return harness.respond(call);
        };
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
        await tester.runAsync(harness.start);
        try {
          await tester.pumpWidget(
            ProviderScope(
              overrides: [
                imGatewayProvider.overrideWithValue(harness.gateway),
                appRuntimeProvider.overrideWith((_) => harness.runtime),
              ],
              child: MaterialApp(
                theme: await captureTheme(tester),
                home: const ChatPage(conversationId: 'single-alice-bob'),
              ),
            ),
          );
          await tester.pumpAndSettle();
          await tester.longPress(find.text('private-followup-body'));
          await tester.pumpAndSettle();
          if (private) expect(find.text('引用回复'), findsNothing);
          final copy = find.text('复制文字');
          if (copy.evaluate().isNotEmpty) {
            await tester.tap(copy);
            await tester.pumpAndSettle();
          }
          expect(clipboard, private ? 0 : 1);
        } finally {
          await tester.runAsync(harness.dispose);
          await tester.pumpWidget(const SizedBox.shrink());
        }
      },
    );
  }
  test('a source becoming private while native quote creation waits cannot complete a retainable draft', () async {
    harness.messages.add(source('ordinary'));
    await harness.start();
    final repository = ChatRepository(
      gateway: harness.gateway,
      session: harness.session,
      conversationId: 'single-alice-bob',
    );
    await repository.conversation();
    await repository.history();
    final waiting = Completer<dynamic>();
    harness.overrideCall = (call) async => call.method == 'createQuoteMessage'
        ? waiting.future
        : harness.respond(call);
    final pending = repository.prepareText('reply', quoteId: 'source');
    final rejected = expectLater(pending, throwsA(isA<AppFailure>()));
    await Future<void>.delayed(Duration.zero);
    final creation = harness.calls.lastWhere(
      (call) => call.method == 'createQuoteMessage',
    );
    harness.messages[0] = source('root');
    await repository.history();
    waiting.complete(harness.respond(creation));
    await rejected;
    expect(
      harness.calls.where((call) => call.method == 'sendMessage'),
      isEmpty,
    );
  });
  test('final send rechecks the actual quote source after creation, even when native omits quote metadata', () async {
    harness.messages.add(source('ordinary'));
    await harness.start();
    final repository = ChatRepository(
      gateway: harness.gateway,
      session: harness.session,
      conversationId: 'single-alice-bob',
    );
    await repository.conversation();
    await repository.history();
    harness.overrideCall = (call) async => call.method == 'createQuoteMessage'
        ? jsonEncode(
            SdkHarness.message(
              id: 'created-without-quote',
              text: 'reply',
              sender: 'alice',
              status: 1,
            ),
          )
        : harness.respond(call);
    final prepared = await repository.prepareText('reply', quoteId: 'source');
    harness.messages[0] = source('root');
    await repository.history();
    await expectLater(repository.send(prepared.id), throwsA(isA<AppFailure>()));
    expect(
      harness.calls.where((call) => call.method == 'sendMessage'),
      isEmpty,
    );
  });
  for (final private in [true, false]) {
    testWidgets(
      'external file opening is retention and respects private=$private',
      (tester) async {
        harness.messages.add({
          ...SdkHarness.message(id: 'file-source', text: ''),
          'contentType': 105,
          'fileElem': {
            'fileName': 'synthetic-file.txt',
            'fileSize': 8,
            'sourceUrl': 'https://files.example.com/synthetic.txt',
          },
          'attachedInfoElem': {'isPrivateChat': private},
        });
        var opened = 0;
        tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
          const MethodChannel('plugins.flutter.io/url_launcher'),
          (call) async {
            if (call.method == 'launch') {
              opened++;
              return true;
            }
            return true;
          },
        );
        addTearDown(
          () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
            const MethodChannel('plugins.flutter.io/url_launcher'),
            null,
          ),
        );
        await tester.runAsync(harness.start);
        try {
          await tester.pumpWidget(
            ProviderScope(
              overrides: [
                imGatewayProvider.overrideWithValue(harness.gateway),
                appRuntimeProvider.overrideWith((_) => harness.runtime),
              ],
              child: MaterialApp(
                theme: await captureTheme(tester),
                home: const ChatPage(conversationId: 'single-alice-bob'),
              ),
            ),
          );
          await tester.pumpAndSettle();
          await tester.tap(find.text('synthetic-file.txt'));
          await tester.pumpAndSettle();
          expect(opened, private ? 0 : 1);
          if (private) expect(find.textContaining('外部'), findsWidgets);
        } finally {
          await tester.runAsync(harness.dispose);
          await tester.pumpWidget(const SizedBox.shrink());
        }
      },
    );
  }
  test(
    'a newly composed private root without a quote remains sendable',
    () async {
      await harness.start();
      final repository = ChatRepository(
        gateway: harness.gateway,
        session: harness.session,
        conversationId: 'single-alice-bob',
      );
      await repository.conversation();
      harness.overrideCall = (call) async => call.method == 'createTextMessage'
          ? jsonEncode({
              ...SdkHarness.message(
                id: 'new-private',
                text: 'new text',
                sender: 'alice',
                status: 1,
              ),
              'attachedInfoElem': {'isPrivateChat': true},
            })
          : harness.respond(call);
      final message = await repository.prepareText('new text');
      await repository.send(message.id);
      expect(
        harness.calls.where((call) => call.method == 'sendMessage'),
        hasLength(1),
      );
    },
  );
  for (final boundary in ['clear', 'owner']) {
    testWidgets(
      'an already open copy portal cannot borrow a new $boundary lease',
      (tester) async {
        harness.messages.add(source('ordinary'));
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
        await tester.runAsync(harness.start);
        try {
          await tester.pumpWidget(
            ProviderScope(
              overrides: [
                imGatewayProvider.overrideWithValue(harness.gateway),
                appRuntimeProvider.overrideWith((_) => harness.runtime),
              ],
              child: MaterialApp(
                home: const ChatPage(conversationId: 'single-alice-bob'),
              ),
            ),
          );
          await tester.pumpAndSettle();
          await tester.longPress(find.text('private-followup-body'));
          await tester.pumpAndSettle();
          if (boundary == 'clear') {
            await tester.runAsync(
              () => ChatRepository(
                gateway: harness.gateway,
                session: harness.session,
                conversationId: 'single-alice-bob',
              ).clearHistory(),
            );
          } else {
            await tester.runAsync(() async {
              await harness.session.signOut();
              harness.userId = 'replacement-owner';
              await harness.session.signIn((_) async => harness.credentials);
            });
          }
          await tester.pump();
          await tester.tap(find.text('复制文字'));
          await tester.pumpAndSettle();
          expect(clipboard, 0);
        } finally {
          await tester.runAsync(harness.dispose);
          await tester.pumpWidget(const SizedBox.shrink());
        }
      },
    );
  }
  for (final operation in ['delete', 'clear']) {
    testWidgets(
      'a second confirmation for $operation cannot renew an invalidated CID lease',
      (tester) async {
        harness.messages.add(source('ordinary'));
        await tester.runAsync(harness.start);
        try {
          await tester.pumpWidget(
            ProviderScope(
              overrides: [
                imGatewayProvider.overrideWithValue(harness.gateway),
                appRuntimeProvider.overrideWith((_) => harness.runtime),
              ],
              child: MaterialApp(
                home: const ChatPage(conversationId: 'single-alice-bob'),
              ),
            ),
          );
          await tester.pumpAndSettle();
          if (operation == 'delete') {
            await tester.longPress(find.text('private-followup-body'));
            await tester.pumpAndSettle();
            await tester.tap(find.text('删除消息'));
          } else {
            await tester.tap(find.byTooltip('聊天设置'));
            await tester.pumpAndSettle();
            await tester.tap(find.text('清空聊天记录'));
          }
          await tester.pumpAndSettle();
          expect(find.text('确认'), findsOneWidget);
          await tester.runAsync(
            () => ChatRepository(
              gateway: harness.gateway,
              session: harness.session,
              conversationId: 'single-alice-bob',
            ).clearHistory(),
          );
          await tester.pump();
          await tester.tap(find.text('确认'));
          await tester.pumpAndSettle();
          expect(
            harness.calls.where(
              (c) => c.method == 'deleteMessageFromLocalAndSvr',
            ),
            isEmpty,
          );
          expect(
            harness.calls.where(
              (c) => c.method == 'clearConversationAndDeleteAllMsg',
            ),
            hasLength(1),
          );
        } finally {
          await tester.runAsync(harness.dispose);
          await tester.pumpWidget(const SizedBox.shrink());
        }
      },
    );
  }
}
