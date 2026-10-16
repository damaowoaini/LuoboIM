import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:imapp/providers.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/data/message_mapper.dart';
import 'package:imapp/features/chat/domain/chat_message.dart';
import 'package:imapp/features/chat/presentation/message_readers_sheet.dart';

import '../../core/im/sdk_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness harness;
  late ChatRepository repository;
  setUp(() async {
    harness = SdkHarness();
    harness.conversations.add(
      SdkHarness.conversation(id: 'sg_group-a', group: true),
    );
    harness.messages.addAll([
      {
        ...SdkHarness.message(id: 'incoming', text: '群消息'),
        'sessionType': 3,
        'groupID': 'group-a',
      },
      {
        ...SdkHarness.message(id: 'own', text: '自己发的', sender: 'alice'),
        'sessionType': 3,
        'groupID': 'group-a',
      },
      {
        ...SdkHarness.message(id: 'failed', text: '发送未成功', status: 3),
        'sessionType': 3,
      },
      {...SdkHarness.message(id: 'notice', text: '群通知'), 'contentType': 1501},
    ]);
    await harness.start();
    repository = ChatRepository(
      gateway: harness.gateway,
      session: harness.session,
      conversationId: 'sg_group-a',
    );
    await repository.conversation();
    await repository.history();
  });
  tearDown(() async {
    repository.clear();
    await harness.dispose();
  });

  test('loading history and clearing conversation badge never marks unseen group messages', () async {
    await repository.markRead();
    expect(
      harness.calls.where(
        (call) => call.method == 'sendGroupMessageReadReceipt',
      ),
      isEmpty,
    );
  });

  test(
    'legacy custom call901 summaries do not become ordinary read receipts',
    () async {
      harness.messages.addAll([
        {
          ...SdkHarness.message(id: 'call-summary', text: ''),
          'contentType': MessageType.custom,
          'customElem': {
            'data': jsonEncode({
              'customType': 901,
              'data': {'state': 1},
            }),
          },
        },
        {
          ...SdkHarness.message(id: 'ordinary-custom', text: ''),
          'contentType': MessageType.custom,
          'customElem': {
            'data': jsonEncode({'customType': 902}),
          },
        },
      ]);
      await repository.history();
      await repository.markVisibleRead(['call-summary', 'ordinary-custom']);
      final calls = harness.calls.where(
        (call) => call.method == 'sendGroupMessageReadReceipt',
      );
      expect(calls, hasLength(1));
      expect((calls.single.arguments as Map)['clientMsgIDs'], [
        'ordinary-custom',
      ]);
    },
  );

  test('only visible incoming successful group messages send coalesced receipts and failed ACK remains retryable', () async {
    var fail = true;
    harness.overrideCall = (call) async {
      if (call.method == 'sendGroupMessageReadReceipt' && fail) {
        fail = false;
        throw PlatformException(code: '500', message: 'private response');
      }
      return harness.respond(call);
    };
    await expectLater(
      repository.markVisibleRead(['incoming', 'own', 'failed', 'notice']),
      throwsA(isA<AppFailure>()),
    );
    await Future.wait([
      repository.markVisibleRead(['incoming']),
      repository.markVisibleRead(['incoming']),
    ]);
    await repository.markVisibleRead(['incoming']);
    final receipts = harness.calls
        .where((call) => call.method == 'sendGroupMessageReadReceipt')
        .toList();
    expect(receipts, hasLength(2));
    expect((receipts.last.arguments as Map)['clientMsgIDs'], ['incoming']);
    expect((receipts.last.arguments as Map)['conversationID'], 'sg_group-a');
  });

  test(
    'sign out between receipt batches prevents further write dispatch',
    () async {
      final ids = List.generate(105, (index) => 'visible-$index');
      for (final id in ids) {
        harness.messages.add({
          ...SdkHarness.message(id: id, text: '可见消息'),
          'sessionType': 3,
        });
      }
      await repository.history();
      final entered = Completer<void>();
      final release = Completer<void>();
      harness.overrideCall = (call) async {
        if (call.method == 'sendGroupMessageReadReceipt') {
          if (!entered.isCompleted) entered.complete();
          await release.future;
        }
        return harness.respond(call);
      };
      final operation = repository.markVisibleRead(ids);
      final rejected = expectLater(operation, throwsA(isA<AppFailure>()));
      await entered.future;
      final logout = harness.session.signOut();
      release.complete();
      await rejected;
      await logout;
      final writes = harness.calls
          .where((call) => call.method == 'sendGroupMessageReadReceipt')
          .toList();
      expect(writes, hasLength(1));
      expect((writes.single.arguments as Map)['clientMsgIDs'], hasLength(100));
    },
  );

  test('reader filters preserve wire0 read/1 unread and return domain data with safe avatars', () async {
    harness.overrideCall = (call) async {
      if (call.method == 'getGroupMessageReaderList') {
        return jsonEncode([
          {
            'userID': 'bob',
            'nickname': '小乙',
            'faceURL': 'https://secret:token@example.com/avatar',
          },
        ]);
      }
      return harness.respond(call);
    };
    final read = await repository.readers('own', read: true, offset: 30);
    await repository.readers('own', read: false);
    expect(read.readers.single, isA<MessageReader>());
    expect(read.readers.single.name, '小乙');
    expect(read.readers.single.avatar, isNull);
    final requests = harness.calls
        .where((call) => call.method == 'getGroupMessageReaderList')
        .toList();
    expect((requests.first.arguments as Map)['filter'], 0);
    expect((requests.first.arguments as Map)['offset'], 30);
    expect((requests.last.arguments as Map)['filter'], 1);
    await expectLater(
      repository.readers('incoming', read: true),
      throwsA(isA<AppFailure>()),
    );
  });

  test(
    'group read count survives optimistic delivery and staged-media mapping',
    () {
      final message = mapMessage(
        Message.fromJson({
          ...SdkHarness.message(id: 'group', text: '已读'),
          'sessionType': 3,
          'attachedInfoElem': {
            'groupHasReadInfo': {'hasReadCount': 7, 'unreadCount': 2},
          },
        }),
        'alice',
      );
      expect(message.groupReadCount, 7);
      expect(message.withDelivery(MessageDelivery.sent).groupReadCount, 7);
      expect(message.withLocalMedia('/account/file').groupReadCount, 7);
    },
  );

  testWidgets(
    'group receipts follow viewport and exclude unheard voice messages',
    (tester) async {
      harness.messages.clear();
      for (var index = 0; index < 35; index++) {
        harness.messages.add({
          ...SdkHarness.message(id: 'visible-$index', text: '消息索引$index'),
          'sessionType': 3,
          'groupID': 'group-a',
          'sendTime': 1700000000000 + index * 1000,
        });
      }
      harness.messages.add({
        ...SdkHarness.message(id: 'unheard', text: ''),
        'sessionType': 3,
        'groupID': 'group-a',
        'contentType': MessageType.voice,
        'soundElem': {
          'sourceUrl': 'https://example.com/voice.m4a',
          'duration': 5,
        },
        'sendTime': 1700000100000,
      });
      tester.view.physicalSize = const Size(360, 640);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            imGatewayProvider.overrideWithValue(harness.gateway),
            appRuntimeProvider.overrideWith((_) => harness.runtime),
          ],
          child: const MaterialApp(
            home: ChatPage(conversationId: 'sg_group-a'),
          ),
        ),
      );
      await tester.pumpAndSettle();
      List<String> marked() => harness.calls
          .where((call) => call.method == 'sendGroupMessageReadReceipt')
          .expand(
            (call) => List<String>.from(
              (call.arguments as Map)['clientMsgIDs'] as List,
            ),
          )
          .toList();
      expect(marked(), contains('visible-34'));
      expect(marked(), isNot(contains('visible-0')));
      expect(marked(), isNot(contains('unheard')));
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
      final background = {
        ...SdkHarness.message(id: 'background-new', text: '后台新消息'),
        'sessionType': 3,
        'groupID': 'group-a',
        'sendTime': 1700000200000,
      };
      harness.messages.add(background);
      await tester.runAsync(
        () => harness.callback(ListenerType.advancedMsgListener, {
          'type': 'onRecvNewMessage',
          'data': {'message': jsonEncode(background)},
        }),
      );
      await tester.runAsync(
        () => Future<void>.delayed(const Duration(milliseconds: 150)),
      );
      await tester.pump(const Duration(milliseconds: 200));
      expect(marked(), isNot(contains('background-new')));
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pumpAndSettle();
      expect(marked(), contains('background-new'));
      await tester.scrollUntilVisible(
        find.text('消息索引0'),
        450,
        scrollable: find.byType(Scrollable).first,
        maxScrolls: 30,
      );
      await tester.pumpAndSettle();
      expect(marked(), contains('visible-0'));
      expect(marked(), isNot(contains('unheard')));
      await tester.runAsync(harness.dispose);
      await tester.pumpWidget(const SizedBox());
    },
  );

  testWidgets('switching reader tab ignores delayed previous-tab response', (
    tester,
  ) async {
    final oldRead = Completer<String>();
    harness.overrideCall = (call) async {
      if (call.method == 'getGroupMessageReaderList') {
        if ((call.arguments as Map)['filter'] == 0) return oldRead.future;
        return jsonEncode([
          {'userID': 'unread', 'nickname': '未读成员乙'},
        ]);
      }
      return harness.respond(call);
    };
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: MessageReadersSheet(repository: repository, messageId: 'own'),
        ),
      ),
    );
    await tester.pump();
    await tester.tap(find.text('未读成员'));
    await tester.pumpAndSettle();
    expect(find.text('未读成员乙'), findsOneWidget);
    oldRead.complete(
      jsonEncode([
        {'userID': 'late', 'nickname': '已读迟到甲'},
      ]),
    );
    await tester.pumpAndSettle();
    expect(find.text('已读迟到甲'), findsNothing);
    expect(find.text('未读成员乙'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });
}
