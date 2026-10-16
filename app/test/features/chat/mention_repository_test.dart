import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/domain/chat_mention.dart';
import 'package:imapp/features/chat/presentation/mention_text_controller.dart';
import 'package:imapp/features/conversation/data/conversation_repository.dart';

import 'mention_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late MentionHarness h;
  late ChatRepository repository;
  setUp(() async {
    h = MentionHarness();
    await h.sdk.start();
    repository = ChatRepository(
      gateway: h.sdk.gateway,
      session: h.sdk.session,
      conversationId: 'single-alice-bob',
      api: h.api,
    );
    await repository.conversation();
    await repository.history();
  });
  tearDown(() => h.dispose());
  MentionDraft draft([String id = 'bob']) {
    final input = MentionTextController();
    input.insert(MentionIdentity(id, id == mentionAllId ? '所有人' : '选择时的旧昵称'));
    final result = input.freeze();
    input.dispose();
    return result;
  }

  Future<String> prepare([String id = 'bob']) async {
    final d = draft(id);
    return (await repository.prepareText(d.text, mentionDraft: d)).id;
  }

  test('selected mention draft remains readable exact plain text in SDK storage and inbox preview', () async {
    final input = MentionTextController()
      ..insert(const MentionIdentity('bob', '同名成员'));
    addTearDown(input.dispose);
    await repository.saveDraft(input.storedDraft);
    expect(h.sdk.conversations.single['draftText'], '@同名成员 ');
    final inbox = await ConversationRepository(
      h.sdk.gateway,
      h.sdk.session,
    ).list();
    expect(inbox.single.draft, '@同名成员 ');
    expect(inbox.single.draft, isNot(contains('luobo-mention-draft')));
  });

  test('historical at-sign UID survives authoritative membership and native codec through final send', () async {
    h.interceptHttp = (request) async {
      final result = h.respondHttp(request) as Map;
      if (request.uri.path.endsWith('get_group_members_info')) {
        return {
          'members': [
            h.members.first,
            {
              'userID': 'legacy@example.test',
              'groupID': 'group-a',
              'roleLevel': 20,
              'nickname': '历史账号',
            },
          ],
        };
      }
      return result;
    };
    final id = await prepare('legacy@example.test');
    await repository.send(id);
    final args =
        h.sdk.calls
                .singleWhere((c) => c.method == 'createTextAtMessage')
                .arguments
            as Map;
    expect(args['text'], '@legacy@example.test ');
    expect(args['atUserIDList'], ['legacy@example.test']);
    expect(h.sdk.calls.where((c) => c.method == 'sendMessage'), hasLength(1));
  });
  for (final role in [20, 60, 100]) {
    test(
      'personal mute uses milliseconds and follows current owner exemption role$role',
      () async {
        h.role = role;
        h.muteEndTime = DateTime.now()
            .add(const Duration(minutes: 1))
            .millisecondsSinceEpoch;
        if (role == 100) {
          final id = await prepare();
          await repository.send(id);
          expect(
            h.sdk.calls.where((c) => c.method == 'sendMessage'),
            hasLength(1),
          );
        } else {
          await expectLater(prepare(), throwsA(isA<AppFailure>()));
          expect(
            h.sdk.calls.where((c) => c.method == 'createTextAtMessage'),
            isEmpty,
          );
        }
      },
    );
  }
  test('past millisecond personal mute no longer denies an ordinary member mention', () async {
    h.muteEndTime = DateTime.now()
        .subtract(const Duration(seconds: 1))
        .millisecondsSinceEpoch;
    final id = await prepare();
    await repository.send(id);
    expect(h.sdk.calls.where((c) => c.method == 'sendMessage'), hasLength(1));
  });
  test('personal mute at the exact server millisecond boundary still rejects before native creation', () async {
    final time = DateTime.utc(2026, 10, 16);
    h.muteEndTime = time.millisecondsSinceEpoch;
    final exactClock = ChatRepository(
      gateway: h.sdk.gateway,
      session: h.sdk.session,
      conversationId: 'single-alice-bob',
      api: h.api,
      now: () => time,
    );
    await exactClock.conversation();
    final d = draft();
    await expectLater(
      exactClock.prepareText(d.text, mentionDraft: d),
      throwsA(isA<AppFailure>()),
    );
    expect(
      h.sdk.calls.where((c) => c.method == 'createTextAtMessage'),
      isEmpty,
    );
  });

  test('explicit selection uses stable UID, current authoritative nickname and exact native AtText contract through final send', () async {
    final id = await prepare();
    await repository.send(id);
    final create = h.sdk.calls.singleWhere(
      (c) => c.method == 'createTextAtMessage',
    );
    final args = create.arguments as Map;
    expect(args['atUserIDList'], ['bob']);
    expect(args['text'], '@bob ');
    expect((args['atUserInfoList'] as List).single, {
      'atUserID': 'bob',
      'groupNickname': '同名成员',
    });
    expect(
      h.http.requests.where(
        (r) => r.uri.path.endsWith('/group/get_group_members_info'),
      ),
      hasLength(3),
    );
    expect(h.sdk.calls.where((c) => c.method == 'sendMessage'), hasLength(1));
  });
  test(
    'handwritten @ nickname never creates native mention metadata',
    () async {
      final item = await repository.prepareText('@同名成员 @bob ');
      await repository.send(item.id);
      expect(
        h.sdk.calls.where((c) => c.method == 'createTextAtMessage'),
        isEmpty,
      );
      expect(
        h.sdk.calls.where((c) => c.method == 'createTextMessage'),
        hasLength(1),
      );
    },
  );
  for (final role in [20, 60, 100]) {
    test('at all requires current owner or admin role$role', () async {
      h.role = role;
      if (role == 20) {
        await expectLater(prepare(mentionAllId), throwsA(isA<AppFailure>()));
        expect(
          h.sdk.calls.where((c) => c.method == 'createTextAtMessage'),
          isEmpty,
        );
      } else {
        final id = await prepare(mentionAllId);
        await repository.send(id);
        expect(
          h.sdk.calls.where((c) => c.method == 'sendMessage'),
          hasLength(1),
        );
      }
    });
  }
  test('a selected member leaving during creation rejects the result before it can be sent', () async {
    h.intercept = (call) async {
      final response = h.respond(call);
      if (call.method == 'createTextAtMessage') h.bobJoined = false;
      return response;
    };
    await expectLater(prepare(), throwsA(isA<AppFailure>()));
    expect(
      h.sdk.calls.where((c) => c.method == 'createTextAtMessage'),
      hasLength(1),
    );
    expect(h.sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
  });
  test(
    'role loss after creation and before final send also blocks every retry',
    () async {
      h.role = 100;
      final id = await prepare(mentionAllId);
      h.role = 20;
      await expectLater(repository.send(id), throwsA(isA<AppFailure>()));
      await expectLater(repository.send(id), throwsA(isA<AppFailure>()));
      expect(h.sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
    },
  );
  test('quote becoming private while the member check is pending blocks native creation', () async {
    h.interceptHttp = (request) async {
      if (request.uri.path.endsWith('/group/get_group_members_info')) {
        h.sdk.gateway.privacyLedger.observe('single-alice-bob', {
          'clientMsgID': 'source',
          'attachedInfoElem': {'isPrivateChat': true},
        }, now: DateTime.now());
      }
      return h.respondHttp(request);
    };
    final d = draft();
    await expectLater(
      repository.prepareText(d.text, quoteId: 'source', mentionDraft: d),
      throwsA(isA<AppFailure>()),
    );
    expect(
      h.sdk.calls.where((c) => c.method == 'createTextAtMessage'),
      isEmpty,
    );
  });
  test('history clear invalidates a delayed member check before create and settles it before native clear', () async {
    final entered = Completer<void>(), release = Completer<void>();
    h.interceptHttp = (request) async {
      if (request.uri.path.endsWith('/group/get_group_members_info') &&
          !entered.isCompleted) {
        entered.complete();
        await release.future;
      }
      return h.respondHttp(request);
    };
    final operation = prepare();
    final rejected = expectLater(operation, throwsA(isA<AppFailure>()));
    await entered.future;
    final clear = h.sdk.gateway.clearConversationMessages(
      h.sdk.session.state.generation,
      'single-alice-bob',
    );
    expect(
      h.sdk.calls.where((c) => c.method == 'clearConversationAndDeleteAllMsg'),
      isEmpty,
    );
    release.complete();
    await rejected;
    await clear;
    expect(
      h.sdk.calls.where((c) => c.method == 'createTextAtMessage'),
      isEmpty,
    );
  });
  test('logout while checking members cannot borrow a later session to create or send', () async {
    final entered = Completer<void>(), release = Completer<void>();
    h.interceptHttp = (request) async {
      if (request.uri.path.endsWith('/group/get_group_members_info') &&
          !entered.isCompleted) {
        entered.complete();
        await release.future;
      }
      return h.respondHttp(request);
    };
    final rejected = expectLater(prepare(), throwsA(isA<AppFailure>()));
    await entered.future;
    final exit = h.sdk.session.signOut();
    release.complete();
    await rejected;
    await exit;
    expect(
      h.sdk.calls.where((c) => c.method == 'createTextAtMessage'),
      isEmpty,
    );
  });
  test('a new private root at-message without a private quote remains a lawful new send', () async {
    h.intercept = (call) async {
      final response = h.respond(call);
      if (call.method != 'createTextAtMessage') return response;
      final message = jsonDecode(response as String) as Map<String, dynamic>;
      message['attachedInfoElem'] = {'isPrivateChat': true};
      return jsonEncode(message);
    };
    final id = await prepare();
    await repository.send(id);
    expect(h.sdk.calls.where((c) => c.method == 'sendMessage'), hasLength(1));
  });
  test('missing HTTP authority refuses real mentions but keeps ordinary text available', () async {
    final withoutApi = ChatRepository(
      gateway: h.sdk.gateway,
      session: h.sdk.session,
      conversationId: 'single-alice-bob',
    );
    await withoutApi.conversation();
    final d = draft();
    await expectLater(
      withoutApi.prepareText(d.text, mentionDraft: d),
      throwsA(isA<AppFailure>()),
    );
    final plain = await withoutApi.prepareText('普通文字 @同名');
    await withoutApi.send(plain.id);
    expect(
      h.sdk.calls.where((c) => c.method == 'createTextAtMessage'),
      isEmpty,
    );
    expect(h.sdk.calls.where((c) => c.method == 'sendMessage'), hasLength(1));
  });
  test('current HTTP authority wins over stale SDK role data and every authorization read uses only frozen IM token', () async {
    h.intercept = (call) async {
      if (call.method == 'getGroupMembersInfo' ||
          call.method == 'getGroupsInfo') {
        throw StateError('SDK local cache cannot authorize mentions');
      }
      return h.respond(call);
    };
    await expectLater(prepare(mentionAllId), throwsA(isA<AppFailure>()));
    h.role = 60;
    final id = await prepare(mentionAllId);
    await repository.send(id);
    expect(h.sdk.calls.where((c) => c.method == 'sendMessage'), hasLength(1));
    for (final request in h.http.requests) {
      expect(request.headers['token'], 'fixture-im');
      expect(
        request.uri.path,
        anyOf(
          '/api/group/get_groups_info',
          '/api/group/get_group_members_info',
        ),
      );
      if (request.uri.path.endsWith('get_groups_info')) {
        expect(request.data, {
          'groupIDs': ['group-a'],
        });
      } else {
        expect(request.data, {
          'groupID': 'group-a',
          'userIDs': ['alice'],
        });
      }
    }
  });
  test('foreign group member IDs duplicate records missing roles or missing status never authorize a native ping', () async {
    for (final malformed in ['group', 'actor', 'duplicate', 'role', 'status']) {
      h.interceptHttp = (request) async {
        final result = jsonDecode(
          jsonEncode(h.respondHttp(request)),
        ) as Map<String, dynamic>;
        if (request.uri.path.endsWith('get_groups_info')) {
          final group = (result['groupInfos'] as List).single as Map;
          if (malformed == 'group') group['groupID'] = 'foreign-group';
          if (malformed == 'status') group.remove('status');
        } else {
          final members = result['members'] as List;
          final actor = members.first as Map;
          if (malformed == 'actor') actor['userID'] = 'foreign-actor';
          if (malformed == 'duplicate') members.add(Map.from(actor));
          if (malformed == 'role') actor.remove('roleLevel');
        }
        return result;
      };
      await expectLater(
        prepare(),
        throwsA(isA<AppFailure>()),
        reason: malformed,
      );
      expect(
        h.sdk.calls.where((c) => c.method == 'createTextAtMessage'),
        isEmpty,
        reason: malformed,
      );
    }
  });
}
