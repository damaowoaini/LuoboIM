import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';

import '../../core/im/sdk_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  for (final group in [false, true]) {
    test(
      'forwarding at-text to ${group ? 'a different-membership group' : 'a singlechat'} preserves original metadata in a standard non-pinging single merge',
      () async {
        final sdk = SdkHarness()
          ..conversations.add({
            ...SdkHarness.conversation(group: group),
            if (group) 'groupID': 'destination-group',
          });
        final original = {
          ...SdkHarness.message(id: 'original-at', text: ''),
          'contentType': 106,
          'groupID': 'original-group',
          'sessionType': 3,
          'cloudCustomData': 'preserve-extension',
          'keyVersion': 8,
          'atTextElem': {
            'text': '@AtAllTag @not-a-destination-member ',
            'atUserList': ['AtAllTag', 'not-a-destination-member'],
            'atUsersInfo': [
              {
                'atUserID': 'not-a-destination-member',
                'groupNickname': '原提及成员',
              },
            ],
            'quoteMessage': {
              ...SdkHarness.message(id: 'original-quote', text: '被引用原文'),
              'cloudCustomData': 'quote-extension',
            },
          },
        };
        sdk.overrideCall = (call) async {
          if (call.method == 'getGroupsInfo' ||
              call.method == 'getGroupMembersInfo') {
            throw StateError(
              'must not authorize historical ping recipients in destination',
            );
          }
          if (call.method == 'createMergerMessage') {
            return jsonEncode({
              ...SdkHarness.message(
                id: 'display-only-forward',
                text: '',
                sender: 'alice',
                status: 1,
              ),
              'contentType': 107,
              'mergeElem': {
                'title': (call.arguments as Map)['title'],
                'abstractList': (call.arguments as Map)['summaryList'],
                'multiMessage': (call.arguments as Map)['messageList'],
              },
            });
          }
          return sdk.respond(call);
        };
        await sdk.start();
        try {
          final repo = ChatRepository(
            gateway: sdk.gateway,
            session: sdk.session,
            conversationId: 'single-alice-bob',
          );
          await repo.conversation();
          final sent = await repo.forwardMessageJson(jsonEncode(original));
          final message =
              (sdk.calls.singleWhere((c) => c.method == 'sendMessage').arguments
                      as Map)['message']
                  as Map;
          expect(message['contentType'], 107);
          expect(message['atTextElem'], isNull);
          expect(message['atUserIDList'], isNull);
          final nested = (message['mergeElem'] as Map)['multiMessage'] as List;
          expect(nested, hasLength(1));
          final child = nested.single as Map;
          expect(child['clientMsgID'], 'original-at');
          expect(child['groupID'], 'original-group');
          expect(child['cloudCustomData'], 'preserve-extension');
          expect(child['keyVersion'], 8);
          expect((child['atTextElem'] as Map)['atUserList'], [
            'AtAllTag',
            'not-a-destination-member',
          ]);
          final quote = (child['atTextElem'] as Map)['quoteMessage'] as Map;
          expect(quote['clientMsgID'], 'original-quote');
          expect(quote['cloudCustomData'], 'quote-extension');
          expect(sent.forwardedMessage?.sourceGroupId, 'original-group');
          expect(sent.forwardedMessage?.quoteId, 'original-quote');
          expect(
            sdk.calls.where((c) => c.method == 'createForwardMessage'),
            isEmpty,
          );
        } finally {
          await sdk.dispose();
        }
      },
    );
  }
  test('private facts learned while the at-forward merge is created still block final send', () async {
    final sdk = SdkHarness()..conversations.add(SdkHarness.conversation());
    final original = {
      ...SdkHarness.message(id: 'original-at', text: ''),
      'contentType': 106,
      'atTextElem': {
        'text': '@bob ',
        'atUserList': ['bob'],
      },
    };
    sdk.overrideCall = (call) async {
      if (call.method == 'createMergerMessage') {
        sdk.gateway.privacyLedger.observe('source', {
          'clientMsgID': 'original-at',
          'attachedInfoElem': {'isPrivateChat': true},
        }, now: DateTime.now());
        return jsonEncode({
          ...SdkHarness.message(
            id: 'merged',
            text: '',
            sender: 'alice',
            status: 1,
          ),
          'contentType': 107,
          'mergeElem': {'multiMessage': (call.arguments as Map)['messageList']},
        });
      }
      return sdk.respond(call);
    };
    await sdk.start();
    try {
      final repo = ChatRepository(
        gateway: sdk.gateway,
        session: sdk.session,
        conversationId: 'single-alice-bob',
      );
      await repo.conversation();
      await expectLater(
        repo.forwardMessageJson(jsonEncode(original)),
        throwsA(isA<AppFailure>()),
      );
      expect(sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
    } finally {
      await sdk.dispose();
    }
  });
}
