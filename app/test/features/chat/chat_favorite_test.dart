import 'dart:convert';
import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';

import '../../core/im/sdk_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness harness;
  late ChatRepository repository;
  setUp(() async {
    harness = SdkHarness();
    harness.conversations.add(SdkHarness.conversation());
    harness.messages.add({
      ...SdkHarness.message(id: 'full-protocol', text: '正文'),
      'ex': 'custom-encryption-metadata',
      'cloudCustomData': 'custom-cloud',
      'serverMsgID': 'server-id',
      'seq': 712,
      'keyVersion': 9,
      'attachedInfoElem': {'isPrivateChat': false, 'burnDuration': 86400},
    });
    await harness.start();
    repository = ChatRepository(
      gateway: harness.gateway,
      session: harness.session,
      conversationId: 'single-alice-bob',
    );
    await repository.conversation();
    await repository.history();
  });
  tearDown(() async {
    repository.clear();
    await harness.dispose();
  });

  test('favorite snapshot retains protocol identity, sequence, custom and privacy metadata', () async {
    final snapshot = await repository.favoriteSnapshot('full-protocol');
    final value = jsonDecode(snapshot.content) as Map;
    expect(value['textElem']['content'], '正文');
    expect(value['ex'], 'custom-encryption-metadata');
    expect(value['cloudCustomData'], 'custom-cloud');
    expect(value['seq'], 712);
    expect(value['attachedInfoElem']['isPrivateChat'], false);
    expect(value['keyVersion'], 9);
    expect(snapshot.messageId, 'full-protocol');
    expect(snapshot.conversationId, 'single-alice-bob');
  });
  test('unavailable or old-account snapshot cannot be collected under a new account', () async {
    await expectLater(
      repository.favoriteSnapshot('missing'),
      throwsA(isA<AppFailure>()),
    );
    await harness.session.signOut();
    harness.userId = 'charlie';
    await harness.session.signIn((_) async => harness.credentials);
    await expectLater(
      repository.favoriteSnapshot('full-protocol'),
      throwsA(isA<AppFailure>()),
    );
  });

  test('private root, quoted and merged messages cannot leave through snapshots or old favorite forwarding', () async {
    final private = {
      ...SdkHarness.message(id: 'private', text: 'private body'),
      'attachedInfoElem': {'isPrivateChat': true, 'burnDuration': 30},
    };
    final values = [
      private,
      {
        ...SdkHarness.message(id: 'quote-private', text: 'reply'),
        'contentType': 114,
        'quoteElem': {'text': 'reply', 'quoteMessage': private},
      },
      {
        ...SdkHarness.message(id: 'merge-private', text: ''),
        'contentType': 107,
        'mergeElem': {
          'title': 'history',
          'multiMessage': [private],
        },
      },
    ];
    harness.messages.addAll(values);
    await repository.history();
    for (final value in values) {
      await expectLater(
        repository.favoriteSnapshot(value['clientMsgID'] as String),
        throwsA(isA<AppFailure>()),
      );
      final before = harness.calls.length;
      await expectLater(
        repository.forwardMessageJson(jsonEncode(value)),
        throwsA(isA<AppFailure>()),
      );
      expect(harness.calls.length, before);
    }
  });

  test(
    'source lookup is scoped to its conversation and reports a missing source',
    () async {
      harness.overrideCall = (call) async {
        if (call.method == 'findMessageList') {
          return jsonEncode({
            'findResultItems': [
              {
                'conversationID': 'other-conversation',
                'messageList': [
                  SdkHarness.message(id: 'source', text: 'wrong'),
                ],
              },
              {
                'conversationID': 'single-alice-bob',
                'messageList': [
                  SdkHarness.message(id: 'source', text: 'correct'),
                ],
              },
            ],
          });
        }
        return harness.respond(call);
      };
      expect((await repository.findSource('source'))?.text, 'correct');
      expect(await repository.findSource('missing'), isNull);
      final args =
          harness.calls
                  .lastWhere((call) => call.method == 'findMessageList')
                  .arguments
              as Map;
      expect(args['searchParams'].single['conversationID'], 'single-alice-bob');
    },
  );

  test('forward strips local paths, retains extensions and sends a new native identity', () async {
    harness.overrideCall = (call) async {
      if (call.method == 'createForwardMessage') {
        final json = Map<String, dynamic>.from(
          (call.arguments as Map)['message'] as Map,
        );
        expect(json['fileElem']['filePath'], isNull);
        expect(json['keyVersion'], 0);
        expect(json['cloudCustomData'], 'preserved');
        return jsonEncode({
          ...json,
          'clientMsgID': 'new-forward-id',
          'sendID': 'alice',
        });
      }
      return harness.respond(call);
    };
    final sent = await repository.forwardMessageJson(
      jsonEncode({
        ...SdkHarness.message(id: 'old-id', text: ''),
        'contentType': 105,
        'cloudCustomData': 'preserved',
        'keyVersion': 99,
        'fileElem': {
          'filePath': '/private/sensitive-file',
          'sourceUrl': 'https://files.example.com/report.pdf',
          'fileName': 'report.pdf',
          'fileSize': 10,
        },
      }),
    );
    expect(sent.id, 'new-forward-id');
    final send =
        harness.calls
                .lastWhere((call) => call.method == 'sendMessage')
                .arguments
            as Map;
    expect(send['userID'], 'bob');
    expect(send['message']['fileElem']['filePath'], isNull);
  });

  test('local-path-only attachment cannot be forwarded or uploaded', () async {
    final before = harness.calls.length;
    await expectLater(
      repository.forwardMessageJson(
        jsonEncode({
          ...SdkHarness.message(id: 'old-id', text: ''),
          'contentType': 105,
          'fileElem': {
            'filePath': '/private/sensitive-file',
            'fileName': 'report.pdf',
          },
        }),
      ),
      throwsA(isA<AppFailure>()),
    );
    expect(harness.calls.length, before);
  });

  test(
    'exit during native forward creation fences the later send dispatch',
    () async {
      final created = Completer<String>();
      harness.overrideCall = (call) async {
        if (call.method == 'createForwardMessage') return created.future;
        return harness.respond(call);
      };
      final forwarding = repository.forwardMessageJson(
        jsonEncode(SdkHarness.message(id: 'source', text: 'text')),
      );
      final assertion = expectLater(forwarding, throwsA(isA<AppFailure>()));
      await Future<void>.delayed(Duration.zero);
      final exit = harness.session.signOut();
      created.complete(
        jsonEncode(SdkHarness.message(id: 'new-id', text: 'text')),
      );
      await assertion;
      await exit;
      expect(
        harness.calls.where((call) => call.method == 'sendMessage'),
        isEmpty,
      );
    },
  );
}
