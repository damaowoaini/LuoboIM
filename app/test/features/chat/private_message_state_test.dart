import 'dart:convert';

import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/presentation/chat_controller.dart';

import '../../core/im/sdk_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness harness;
  late ChatController controller;
  var stampRead = true;
  final now = DateTime.fromMillisecondsSinceEpoch(1700000000000);
  setUp(() async {
    stampRead = true;
    harness = SdkHarness();
    harness.conversations.add(SdkHarness.conversation());
    harness.messages.add({
      ...SdkHarness.message(id: 'private-source', text: 'private-body'),
      'attachedInfoElem': {'isPrivateChat': true, 'burnDuration': 30},
    });
    harness.overrideCall = (call) async {
      if (call.method == 'markConversationMessageAsRead' && stampRead) {
        harness.messages.single['attachedInfoElem'] = {
          'isPrivateChat': true,
          'burnDuration': 30,
          'hasReadTime': now.millisecondsSinceEpoch,
        };
      }
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
    await harness.start();
    controller = ChatController(
      ChatRepository(
        gateway: harness.gateway,
        session: harness.session,
        conversationId: 'single-alice-bob',
        now: () => now,
      ),
    );
  });
  tearDown(() async {
    controller.dispose();
    await harness.dispose();
  });
  test('read ACK is followed by native scoped metadata re-read instead of inventing the private read timestamp', () async {
    await controller.start();
    expect(
      controller.messages.single.privacy.messageReadAtMilliseconds,
      now.millisecondsSinceEpoch,
    );
    expect(
      controller.messages.single.privacy.expiresAtMilliseconds,
      now.millisecondsSinceEpoch + 30000,
    );
    final methods = harness.calls.map((call) => call.method).toList();
    expect(
      methods.indexOf('findMessageList'),
      greaterThan(methods.indexOf('markConversationMessageAsRead')),
    );
    final args =
        harness.calls
                .lastWhere((call) => call.method == 'findMessageList')
                .arguments
            as Map;
    expect(args['searchParams'].single['conversationID'], 'single-alice-bob');
    expect(args['searchParams'].single['clientMsgIDList'], ['private-source']);
  });
  test('an ACK without native read metadata does not fabricate a deadline from isRead or sendTime', () async {
    stampRead = false;
    harness.messages.single['isRead'] = true;
    await controller.start();
    expect(controller.messages.single.privacy.expiresAtMilliseconds, isNull);
  });
  test('native delete callback removes visible and quote caches immediately and prevents stale source retention', () async {
    await controller.start();
    controller.reply(controller.messages.single);
    final original = harness.messages.single;
    harness.messages.clear();
    await harness.callback(ListenerType.advancedMsgListener, {
      'type': 'onMsgDeleted',
      'data': {'message': jsonEncode(original)},
    });
    expect(controller.messages, isEmpty);
    expect(controller.quote, isNull);
    await expectLater(
      controller.repository.favoriteSnapshot('private-source'),
      throwsA(isA<AppFailure>()),
    );
  });
  test('a known-expired quote cannot be recreated as a new native message after the editor was opened', () async {
    await controller.start();
    harness.messages.single['attachedInfoElem'] = {
      'isPrivateChat': true,
      'burnDuration': 30,
      'hasReadTime': now.millisecondsSinceEpoch - 31000,
    };
    await controller.repository.history();
    final before = harness.calls.length;
    await expectLater(
      controller.repository.prepareText('new reply', quoteId: 'private-source'),
      throwsA(isA<AppFailure>()),
    );
    expect(harness.calls.length, before);
  });
}
