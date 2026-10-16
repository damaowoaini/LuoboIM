import 'dart:async';
import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/data/message_mapper.dart';
import 'package:imapp/features/chat/domain/chat_message.dart';
import 'package:imapp/features/chat/presentation/chat_controller.dart';

import '../../core/im/sdk_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness harness;
  late ChatController controller;
  setUp(() async {
    harness = SdkHarness();
    harness.conversations.add(SdkHarness.conversation(draft: '原有草稿'));
    harness.messages.add(SdkHarness.message(id: 'incoming-1', text: '旧历史消息'));
    await harness.start();
    controller = ChatController(
      ChatRepository(
        gateway: harness.gateway,
        session: harness.session,
        conversationId: 'single-alice-bob',
      ),
    );
    await controller.start();
  });
  tearDown(() async {
    controller.dispose();
    await harness.dispose();
  });

  test('history is converted to domain data and marked read once per latest incoming message', () async {
    expect(controller.messages.single.text, '旧历史消息');
    expect(controller.messages.single.mine, isFalse);
    expect(controller.draft, '原有草稿');
    await controller.refresh();
    expect(
      harness.calls.where(
        (call) => call.method == 'markConversationMessageAsRead',
      ),
      hasLength(1),
    );
    final before = harness.calls
        .where((call) => call.method == 'getAdvancedHistoryMessageList')
        .length;
    await harness.callback(ListenerType.conversationListener, {
      'type': 'onConversationChanged',
      'data': harness.conversations,
    });
    await Future<void>.delayed(const Duration(milliseconds: 150));
    expect(
      harness.calls.where(
        (call) => call.method == 'getAdvancedHistoryMessageList',
      ),
      hasLength(before),
    );
  });

  test('failed sends retain the SDK message ID and retry without creating a duplicate', () async {
    var failFirst = true;
    harness.overrideCall = (call) async {
      if (call.method == 'sendMessage' && failFirst) {
        failFirst = false;
        throw PlatformException(code: '999', message: 'private failure');
      }
      return harness.respond(call);
    };
    expect(await controller.sendText('消息正文'), isTrue);
    await Future<void>.delayed(Duration.zero);
    final failed = controller.messages.firstWhere((item) => item.mine);
    expect(failed.delivery, MessageDelivery.failed);
    await controller.resend(failed.id);
    expect(
      controller.messages.where((item) => item.id == failed.id).single.delivery,
      MessageDelivery.sent,
    );
    expect(
      harness.calls.where((call) => call.method == 'createTextMessage'),
      hasLength(1),
    );
    final sends = harness.calls
        .where((call) => call.method == 'sendMessage')
        .toList();
    expect(sends, hasLength(2));
    expect(sends.map((call) => call.arguments['message']['clientMsgID']), [
      failed.id,
      failed.id,
    ]);
    expect(sends.last.arguments['userID'], 'bob');
    expect(sends.last.arguments['groupID'], '');
    expect(
      sends.last.arguments['offlinePushInfo']['desc'],
      isNot(contains('消息正文')),
    );
  });

  test('quote and delete preserve identifiers; deletion happens only after the API succeeds', () async {
    controller.reply(controller.messages.single);
    expect(await controller.sendText('引用回复正文'), isTrue);
    await Future<void>.delayed(Duration.zero);
    final call = harness.calls.firstWhere(
      (call) => call.method == 'createQuoteMessage',
    );
    expect(call.arguments['quoteMessage']['clientMsgID'], 'incoming-1');
    expect(call.arguments['quoteText'], '引用回复正文');
    final original = controller.messages.firstWhere(
      (item) => item.id == 'incoming-1',
    );
    harness.overrideCall = (call) async {
      if (call.method == 'deleteMessageFromLocalAndSvr') {
        throw PlatformException(code: '999');
      }
      return harness.respond(call);
    };
    await controller.delete(original);
    expect(controller.messages.any((item) => item.id == original.id), isTrue);
    harness.overrideCall = null;
    await controller.delete(original);
    expect(controller.messages.any((item) => item.id == original.id), isFalse);
  });

  test('draft writes are serialized so an older slow save cannot overwrite newer input', () async {
    final first = Completer<void>();
    final started = Completer<void>();
    var count = 0;
    harness.overrideCall = (call) async {
      if (call.method == 'setConversationDraft' && ++count == 1) {
        started.complete();
        await first.future;
      }
      return harness.respond(call);
    };
    controller.updateDraft('较早内容');
    final oldSave = controller.flushDraft();
    await started.future;
    controller.updateDraft('较新的内容');
    final newSave = controller.flushDraft();
    expect(count, 1);
    first.complete();
    await oldSave;
    await newSave;
    expect(harness.conversations.single['draftText'], '较新的内容');
  });

  test(
    'logout immediately removes chat contents and disables pending work',
    () async {
      await harness.session.signOut();
      expect(controller.messages, isEmpty);
      expect(controller.conversation, isNull);
      expect(controller.usable, isFalse);
      expect(await controller.sendText('不能发给旧账号'), isFalse);
    },
  );

  test(
    'media URL mapping rejects executable and credential-bearing values',
    () {
      for (final value in [
        'javascript:alert(1)',
        'data:text/plain,secret',
        'https://token:secret@example.com/file',
        'http://example.com/file',
      ]) {
        expect(secureMediaUri(value), isNull);
      }
      expect(secureMediaUri('https://example.com/file')?.host, 'example.com');
    },
  );

  test('a late latest-history refresh cannot overwrite the selected original-message timeline', () async {
    final refreshResult = Completer<String>();
    final refreshStarted = Completer<void>();
    harness.overrideCall = (call) async {
      if (call.method == 'getAdvancedHistoryMessageList' &&
          (call.arguments as Map)['startClientMsgID'] == '') {
        refreshStarted.complete();
        return refreshResult.future;
      }
      if (call.method == 'findMessageList') {
        return jsonEncode({
          'findResultItems': [
            {
              'conversationID': 'single-alice-bob',
              'messageList': [SdkHarness.message(id: 'source', text: '定位消息')],
            },
          ],
        });
      }
      return harness.respond(call);
    };
    final refreshing = controller.refresh();
    await refreshStarted.future.timeout(const Duration(seconds: 2));
    expect(await controller.viewSource('source'), isTrue);
    refreshResult.complete(
      jsonEncode({
        'messageList': [SdkHarness.message(id: 'late-latest', text: '迟到的最新消息')],
        'isEnd': true,
      }),
    );
    await refreshing;
    expect(controller.sourceMessageId, 'source');
    expect(
      controller.messages.any((item) => item.id == 'late-latest'),
      isFalse,
    );
  });

  test('a superseded source lookup cannot issue its next history request or replace a newer source', () async {
    final firstResult = Completer<String>();
    final firstStarted = Completer<void>();
    String source(String id) => jsonEncode({
      'findResultItems': [
        {
          'conversationID': 'single-alice-bob',
          'messageList': [SdkHarness.message(id: id, text: id)],
        },
      ],
    });
    harness.overrideCall = (call) async {
      if (call.method == 'findMessageList') {
        final id =
            ((call.arguments as Map)['searchParams'] as List)
                    .single['clientMsgIDList']
                    .single
                as String;
        if (id == 'first') {
          firstStarted.complete();
          return firstResult.future;
        }
        return source(id);
      }
      return harness.respond(call);
    };
    final historyCount = harness.calls
        .where((call) => call.method == 'getAdvancedHistoryMessageList')
        .length;
    final first = controller.viewSource('first');
    await firstStarted.future.timeout(const Duration(seconds: 2));
    expect(
      await controller.viewSource('second').timeout(const Duration(seconds: 2)),
      isTrue,
    );
    firstResult.complete(source('first'));
    expect(await first, isFalse);
    expect(controller.sourceMessageId, 'second');
    expect(
      harness.calls.where(
        (call) => call.method == 'getAdvancedHistoryMessageList',
      ),
      hasLength(historyCount + 1),
    );
  });
}
