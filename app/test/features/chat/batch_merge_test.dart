import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/data/retained_merge_codec.dart';

import 'batch_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late BatchHarness h;
  setUp(() async {
    h = BatchHarness();
    await h.start();
  });
  tearDown(() => h.dispose());
  Future<ChatRepository> target({bool group = false}) async {
    h.sdk.conversations.last.addAll({
      'conversationType': group ? 3 : 1,
      'groupID': group ? 'destination-group' : '',
      'userID': group ? '' : 'carol',
    });
    final repo = ChatRepository(
      gateway: h.sdk.gateway,
      session: h.sdk.session,
      conversationId: 'target-chat',
    );
    await repo.conversation();
    return repo;
  }

  for (final group in [false, true]) {
    test(
      'standard107 to ${group ? 'different-membership group' : 'singlechat'} sorts original createTime and preserves full children without repinging',
      () async {
        h.sdk.messages.first.addAll({
          'createTime': 1700000000009,
          'contentType': 106,
          'groupID': 'original-group',
          'atTextElem': {
            'text': '@AtAllTag @not-destination-member ',
            'atUserList': ['AtAllTag', 'not-destination-member'],
            'atUsersInfo': [
              {'atUserID': 'not-destination-member', 'groupNickname': '原成员'},
            ],
            'quoteMessage': {
              'clientMsgID': 'quoted',
              'contentType': 101,
              'textElem': {'content': '引用原文'},
              'cloudCustomData': 'nested-extension',
            },
          },
        });
        await h.chat.refresh();
        h.selectAll();
        final destination = await target(group: group);
        final sent = await destination.forwardMergedSelection(
          source: h.source,
          intent: h.selection.capture(),
        );
        final args =
            h.sdk.calls
                    .singleWhere((c) => c.method == 'createMergerMessage')
                    .arguments
                as Map;
        final children = args['messageList'] as List;
        expect(children.map((m) => (m as Map)['clientMsgID']), [
          'm2',
          'm3',
          'm1',
        ]);
        final originalAt = children.last as Map;
        expect(originalAt['keyVersion'], 8);
        expect(originalAt['cloudCustomData'], 'extension-1');
        final at = originalAt['atTextElem'] as Map;
        expect(at['atUserList'], ['AtAllTag', 'not-destination-member']);
        expect(
          (at['quoteMessage'] as Map)['cloudCustomData'],
          'nested-extension',
        );
        final wire =
            (h.sdk.calls.singleWhere((c) => c.method == 'sendMessage').arguments
                    as Map)['message']
                as Map;
        expect(wire['contentType'], 107);
        expect(wire['atTextElem'], isNull);
        expect(sent.mergedMessages, hasLength(3));
        expect(sent.mergedMessages.last.sourceGroupId, 'original-group');
        expect(h.count('createTextAtMessage'), 0);
        expect(h.count('getGroupMembersInfo'), 0);
        expect(h.count('getGroupsInfo'), 0);
      },
    );
  }
  test('private source learned before confirmation prevents all native merge creation', () async {
    h.selectAll();
    final intent = h.selection.capture();
    final destination = await target();
    h.sdk.gateway.privacyLedger.observe('single-alice-bob', {
      'clientMsgID': 'm2',
      'attachedInfoElem': {'isPrivateChat': true},
    }, now: DateTime.now());
    await expectLater(
      destination.forwardMergedSelection(source: h.source, intent: intent),
      throwsA(isA<AppFailure>()),
    );
    expect(h.count('createMergerMessage'), 0);
    expect(h.count('sendMessage'), 0);
  });
  test('source becoming private during native creation cannot dispatch the prepared merge', () async {
    h.selectAll();
    final intent = h.selection.capture();
    final destination = await target();
    h.intercept = (call) async {
      final value = h.respond(call);
      if (call.method == 'createMergerMessage') {
        h.sdk.gateway.privacyLedger.observe('single-alice-bob', {
          'clientMsgID': 'm2',
          'attachedInfoElem': {'isPrivateChat': true},
        }, now: DateTime.now());
      }
      return value;
    };
    await expectLater(
      destination.forwardMergedSelection(source: h.source, intent: intent),
      throwsA(isA<AppFailure>()),
    );
    expect(h.count('createMergerMessage'), 1);
    expect(h.count('sendMessage'), 0);
  });
  test('source body changing after native merge creation fails its fresh fingerprint rather than forwarding an old snapshot', () async {
    h.selectAll();
    final intent = h.selection.capture();
    final destination = await target();
    h.intercept = (call) async {
      final value = h.respond(call);
      if (call.method == 'createMergerMessage') {
        h.sdk.messages[1]['textElem'] = {'content': '新的正文'};
      }
      return value;
    };
    await expectLater(
      destination.forwardMergedSelection(source: h.source, intent: intent),
      throwsA(isA<AppFailure>()),
    );
    expect(h.count('sendMessage'), 0);
  });
  test('a reused target CID whose recipient changes during create cannot receive the merge', () async {
    h.selectAll();
    final intent = h.selection.capture();
    final destination = await target();
    h.intercept = (call) async {
      final value = h.respond(call);
      if (call.method == 'createMergerMessage') {
        h.sdk.conversations.last['userID'] = 'replacement';
      }
      return value;
    };
    await expectLater(
      destination.forwardMergedSelection(source: h.source, intent: intent),
      throwsA(isA<AppFailure>()),
    );
    expect(h.count('sendMessage'), 0);
  });
  test('source clear while create waits invalidates the original selection across source and destination CIDs', () async {
    h.selectAll();
    final intent = h.selection.capture();
    final destination = await target();
    final entered = Completer<void>(), release = Completer<void>();
    h.intercept = (call) async {
      if (call.method == 'createMergerMessage') {
        entered.complete();
        await release.future;
      }
      return h.respond(call);
    };
    final rejected = expectLater(
      destination.forwardMergedSelection(source: h.source, intent: intent),
      throwsA(isA<AppFailure>()),
    );
    await entered.future;
    final clear = h.sdk.gateway.clearConversationMessages(
      h.sdk.session.state.generation,
      'single-alice-bob',
    );
    release.complete();
    await rejected;
    await clear;
    expect(h.count('sendMessage'), 0);
  });
  test('native merge dropping an extension fails closed before send instead of silently losing protocol data', () async {
    h.selectAll();
    final intent = h.selection.capture();
    final destination = await target();
    h.intercept = (call) async {
      final value = h.respond(call);
      if (call.method != 'createMergerMessage') return value;
      final wire = jsonDecode(value as String) as Map;
      final child =
          ((wire['mergeElem'] as Map)['multiMessage'] as List).first as Map;
      child.remove('cloudCustomData');
      return jsonEncode(wire);
    };
    await expectLater(
      destination.forwardMergedSelection(source: h.source, intent: intent),
      throwsA(isA<AppFailure>()),
    );
    expect(h.count('sendMessage'), 0);
  });
  test('retained child sanitizer strips local paths recursively but retains keyVersion quote and opaque extension values', () {
    final wire = <String, dynamic>{
      'clientMsgID': 'x',
      'keyVersion': 8,
      'cloudCustomData': 'opaque',
      'fileElem': {
        'sourceUrl': 'https://objects.test/file',
        'filePath': '/private/local',
      },
      'quoteElem': {
        'quoteMessage': {'clientMsgID': 'q', 'sourcePath': '/private/source'},
      },
    };
    final clean = retainedMergeChild(wire);
    expect((clean['fileElem'] as Map)['filePath'], isNull);
    expect(clean['keyVersion'], 8);
    expect(clean['cloudCustomData'], 'opaque');
    expect(
      retainedMergeFingerprint(clean),
      retainedMergeFingerprint(
        Map.fromEntries(clean.entries.toList().reversed),
      ),
    );
  });
}
