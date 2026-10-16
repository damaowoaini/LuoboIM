import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/domain/chat_search.dart';

import '../../core/im/sdk_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness harness;
  setUp(() async {
    harness = SdkHarness()
      ..conversations.add(SdkHarness.conversation())
      ..conversations.add(SdkHarness.conversation(id: 'other-conversation'));
    await harness.start();
  });
  tearDown(() => harness.dispose());
  ChatRepository repo([String id = 'single-alice-bob']) => ChatRepository(
    gateway: harness.gateway,
    session: harness.session,
    conversationId: id,
  );
  test('a shared conversation clear waits for unseen history from another repository and rejects its late cache', () async {
    final waiting = Completer<dynamic>();
    harness.overrideCall = (call) async =>
        call.method == 'getAdvancedHistoryMessageList'
        ? waiting.future
        : harness.respond(call);
    final old = repo();
    final pending = old.history();
    var rejected = false;
    final finished = pending.then<void>(
      (_) {},
      onError: (Object error) {
        rejected = error is AppFailure;
      },
    );
    await Future<void>.delayed(Duration.zero);
    final clearing = repo().clearHistory();
    await Future<void>.delayed(Duration.zero);
    final before = harness.calls
        .where((c) => c.method == 'clearConversationAndDeleteAllMsg')
        .length;
    waiting.complete(
      jsonEncode({
        'messageList': [
          SdkHarness.message(id: 'unseen-before-clear', text: 'late body'),
        ],
        'isEnd': true,
        'errCode': 0,
      }),
    );
    await finished;
    await clearing;
    expect(before, 0);
    expect(rejected, true);
    await expectLater(
      old.favoriteSnapshot('unseen-before-clear'),
      throwsA(isA<AppFailure>()),
    );
  });
  test('queued send creation settles before same-conversation clear while another conversation remains usable', () async {
    final source = repo();
    final other = repo('other-conversation');
    await source.conversation();
    await other.conversation();
    final waiting = Completer<dynamic>();
    var held = true;
    harness.overrideCall = (call) async {
      if (call.method == 'createTextMessage' && held) {
        held = false;
        return waiting.future;
      }
      return harness.respond(call);
    };
    final pending = source
        .prepareText('old pending')
        .then((m) => source.send(m.id));
    var rejected = false;
    final finished = pending.then<void>(
      (_) {},
      onError: (Object error) {
        rejected = error is AppFailure;
      },
    );
    await Future<void>.delayed(Duration.zero);
    final clearing = repo().clearHistory();
    await Future<void>.delayed(Duration.zero);
    final before = harness.calls
        .where((c) => c.method == 'clearConversationAndDeleteAllMsg')
        .length;
    final fresh = await other.prepareText('other still allowed');
    await other.send(fresh.id);
    waiting.complete(
      jsonEncode(
        SdkHarness.message(
          id: 'old-created',
          text: 'old pending',
          sender: 'alice',
          status: 1,
        ),
      ),
    );
    await finished;
    await clearing;
    expect(before, 0);
    expect(rejected, true);
    expect(
      harness.calls.where(
        (c) =>
            c.method == 'sendMessage' &&
            (c.arguments as Map)['message']['clientMsgID'] == 'old-created',
      ),
      isEmpty,
    );
    expect(
      harness.calls.where(
        (c) =>
            c.method == 'sendMessage' &&
            (c.arguments as Map)['message']['clientMsgID'] == fresh.id,
      ),
      hasLength(1),
    );
  });
  test('search in a separately mounted repository cannot retain unseen results crossing the same conversation clear', () async {
    final waiting = Completer<dynamic>();
    harness.overrideCall = (call) async => call.method == 'searchLocalMessages'
        ? waiting.future
        : harness.respond(call);
    final old = repo();
    final pending = old.search(
      query: 'late',
      filter: ChatHistoryFilter.text,
      timePosition: 1700000000,
    );
    var rejected = false;
    final finished = pending.then<void>(
      (_) {},
      onError: (Object e) {
        rejected = e is AppFailure;
      },
    );
    await Future<void>.delayed(Duration.zero);
    final clearing = repo().clearHistory();
    await Future<void>.delayed(Duration.zero);
    waiting.complete(
      jsonEncode({
        'searchResultItems': [
          {
            'conversationID': 'single-alice-bob',
            'messageList': [
              SdkHarness.message(id: 'late-search', text: 'late body'),
            ],
          },
        ],
        'totalCount': 1,
      }),
    );
    await finished;
    await clearing;
    expect(rejected, true);
    await expectLater(
      old.favoriteSnapshot('late-search'),
      throwsA(isA<AppFailure>()),
    );
  });
}
