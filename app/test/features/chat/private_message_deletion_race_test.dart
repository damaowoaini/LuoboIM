import 'dart:async';
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
  late Map<String, dynamic> original;
  final now = DateTime.fromMillisecondsSinceEpoch(1700000000000);

  setUp(() async {
    harness = SdkHarness();
    harness.conversations.add(SdkHarness.conversation());
    original = {
      ...SdkHarness.message(id: 'private-source', text: 'private-body'),
      'attachedInfoElem': {'isPrivateChat': true, 'burnDuration': 30},
    };
    harness.messages.add(original);
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

  Future<void> remove(bool revoked) async {
    harness.messages.clear();
    await harness.callback(ListenerType.advancedMsgListener, {
      'type': revoked ? 'onNewRecvMessageRevoked' : 'onMsgDeleted',
      'data': revoked
          ? {
              'messageRevoked': jsonEncode({'clientMsgID': 'private-source'}),
            }
          : {'message': jsonEncode(original)},
    });
  }

  String findResult() => jsonEncode({
    'findResultItems': [
      {
        'conversationID': 'single-alice-bob',
        'messageList': [original],
      },
    ],
  });

  for (final revoked in [false, true]) {
    final event = revoked ? 'revoke' : 'delete';
    test(
      'a late history response after $event cannot restore UI or quote cache',
      () async {
        await controller.start();
        controller.reply(controller.messages.single);
        final entered = Completer<void>();
        final response = Completer<String>();
        harness.overrideCall = (call) async {
          if (call.method == 'getAdvancedHistoryMessageList') {
            entered.complete();
            return response.future;
          }
          return harness.respond(call);
        };
        final pending = controller.refresh();
        await entered.future;
        await remove(revoked);
        response.complete(
          jsonEncode({
            'messageList': [original],
            'isEnd': true,
            'errCode': 0,
          }),
        );
        await pending;
        expect(controller.messages, isEmpty);
        expect(controller.quote, isNull);
        final before = harness.calls.length;
        await expectLater(
          controller.repository.prepareText(
            'new reply',
            quoteId: 'private-source',
          ),
          throwsA(isA<AppFailure>()),
        );
        expect(harness.calls.length, before);
      },
    );
    test(
      'a late read-metadata response after $event cannot restore the message',
      () async {
        final entered = Completer<void>();
        final response = Completer<String>();
        harness.overrideCall = (call) async {
          if (call.method == 'findMessageList') {
            entered.complete();
            return response.future;
          }
          return harness.respond(call);
        };
        final pending = controller.start();
        await entered.future;
        await remove(revoked);
        response.complete(findResult());
        await pending;
        expect(controller.messages, isEmpty);
        final before = harness.calls.length;
        await expectLater(
          controller.repository.prepareText(
            'new reply',
            quoteId: 'private-source',
          ),
          throwsA(isA<AppFailure>()),
        );
        expect(harness.calls.length, before);
      },
    );
    test(
      'a late source lookup after $event cannot recreate an actionable snapshot',
      () async {
        await controller.start();
        final entered = Completer<void>();
        final response = Completer<String>();
        harness.overrideCall = (call) async {
          if (call.method == 'findMessageList') {
            entered.complete();
            return response.future;
          }
          return harness.respond(call);
        };
        final pending = controller.repository.findSource('private-source');
        await entered.future;
        await remove(revoked);
        response.complete(findResult());
        expect(await pending, isNull);
        final before = harness.calls.length;
        await expectLater(
          controller.repository.prepareText(
            'new reply',
            quoteId: 'private-source',
          ),
          throwsA(isA<AppFailure>()),
        );
        expect(harness.calls.length, before);
      },
    );
  }
}
