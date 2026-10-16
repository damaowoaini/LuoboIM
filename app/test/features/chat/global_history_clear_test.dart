import 'dart:async';
import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/presentation/chat_controller.dart';
import 'package:imapp/features/chat/presentation/chat_search_controller.dart';
import 'package:imapp/features/conversation/data/conversation_repository.dart';
import 'package:imapp/features/conversation/presentation/conversations_controller.dart';

import '../../core/im/sdk_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness harness;
  late ChatController chat;
  late ChatSearchController search;
  late ConversationsController inbox;
  late ChatRepository repository;

  setUp(() async {
    harness = SdkHarness(operationTimeout: const Duration(milliseconds: 300));
    harness.conversations.add(SdkHarness.conversation());
    harness.messages.add(SdkHarness.message(id: 'source', text: 'cached body'));
    await harness.start();
    repository = ChatRepository(
      gateway: harness.gateway,
      session: harness.session,
      conversationId: 'single-alice-bob',
    );
    chat = ChatController(repository);
    search = ChatSearchController(repository);
    inbox = ConversationsController(
      ConversationRepository(harness.gateway, harness.session),
    );
    await chat.start();
  });
  tearDown(() async {
    search.dispose();
    inbox.dispose();
    chat.dispose();
    await harness.dispose();
  });

  int count(String method) =>
      harness.calls.where((call) => call.method == method).length;
  int getGeneration() => harness.session.state.generation;
  Future<void> flush() => Future<void>.delayed(Duration.zero);
  Matcher failure(FailureKind kind) =>
      isA<AppFailure>().having((error) => error.kind, 'safe kind', kind);

  test('duplicate clear joins one dispatch, invalidates previews/search/raw cache and leaves unrelated profile work available', () async {
    final entered = Completer<void>();
    final release = Completer<void>();
    harness.overrideCall = (call) async {
      if (call.method == 'deleteAllMsgFromLocalAndSvr') {
        entered.complete();
        await release.future;
      }
      if (call.method == 'searchLocalMessages') {
        return jsonEncode({
          'totalCount': 1,
          'searchResultItems': [
            {
              'conversationID': repository.conversationId,
              'messageList': harness.messages,
            },
          ],
        });
      }
      return harness.respond(call);
    };
    await inbox.refresh();
    await search.search(query: 'cached');
    expect(inbox.items, isNotEmpty);
    expect(search.messages, hasLength(1));
    chat.reply(chat.messages.single);
    final revision = harness.gateway.historyRevision;
    final first = harness.gateway.clearAllMessages(getGeneration());
    final duplicate = harness.gateway.clearAllMessages(getGeneration());
    expect(identical(first, duplicate), isTrue);
    expect(harness.gateway.historyRevision, revision + 1);
    await entered.future;
    expect(chat.messages, isEmpty);
    expect(chat.quote, isNull);
    expect(search.messages, isEmpty);
    expect(inbox.items, isEmpty);
    await expectLater(
      repository.history(),
      throwsA(failure(FailureKind.cancelled)),
    );
    final profile = await harness.gateway.runForSession(
      getGeneration(),
      (manager) => manager.userManager.getSelfUserInfo(),
    );
    expect(profile.userID, 'alice');
    release.complete();
    await first;
    await duplicate;
    expect(chat.conversation!.preview, isEmpty);
    expect(count('deleteAllMsgFromLocalAndSvr'), 1);
    await expectLater(
      repository.favoriteSnapshot('source'),
      throwsA(isA<AppFailure>()),
    );
  });

  test('clear waits for old native history and never commits its late cache/UI response', () async {
    final entered = Completer<void>();
    final release = Completer<String>();
    final old = jsonEncode({'messageList': harness.messages, 'isEnd': true});
    harness.overrideCall = (call) async {
      if (call.method == 'getAdvancedHistoryMessageList') {
        entered.complete();
        return release.future;
      }
      return harness.respond(call);
    };
    final pending = chat.refresh();
    await entered.future;
    final clear = harness.gateway.clearAllMessages(getGeneration());
    await flush();
    expect(count('deleteAllMsgFromLocalAndSvr'), 0);
    release.complete(old);
    await pending;
    await clear;
    expect(chat.messages, isEmpty);
    final before = count('createQuoteMessage');
    await expectLater(
      repository.prepareText('reply', quoteId: 'source'),
      throwsA(isA<AppFailure>()),
    );
    expect(count('createQuoteMessage'), before);
  });

  test('an old immutable lease remains invalid after the clear ACK and a new lease can explicitly reload', () async {
    final lease = harness.gateway.historyAccess(getGeneration());
    await harness.gateway.clearAllMessages(getGeneration());
    expect(lease.check, throwsA(failure(FailureKind.cancelled)));
    expect(
      () => harness.gateway.historyAccess(getGeneration()).check(),
      returnsNormally,
    );
    await chat.refresh();
    expect(chat.messages, isEmpty);
  });

  test('text creation already in native settles before clear and cannot dispatch its queued send', () async {
    final entered = Completer<void>();
    final release = Completer<String>();
    harness.overrideCall = (call) async {
      if (call.method == 'createTextMessage') {
        entered.complete();
        return release.future;
      }
      return harness.respond(call);
    };
    final pending = chat.sendText('draft before clear');
    await entered.future;
    final clear = harness.gateway.clearAllMessages(getGeneration());
    await flush();
    expect(count('deleteAllMsgFromLocalAndSvr'), 0);
    release.complete(
      jsonEncode(
        SdkHarness.message(
          id: 'created',
          text: 'draft before clear',
          sender: 'alice',
          status: 1,
        ),
      ),
    );
    expect(await pending, isFalse);
    await clear;
    expect(count('sendMessage'), 0);
    expect(chat.messages, isEmpty);
  });

  test('forward creation cannot renew its lease and send an old snapshot after clear', () async {
    final entered = Completer<void>();
    final release = Completer<String>();
    final original = jsonEncode(harness.messages.single);
    harness.overrideCall = (call) async {
      if (call.method == 'createForwardMessage') {
        entered.complete();
        return release.future;
      }
      return harness.respond(call);
    };
    final pending = expectLater(
      repository.forwardMessageJson(original),
      throwsA(failure(FailureKind.cancelled)),
    );
    await entered.future;
    final clear = harness.gateway.clearAllMessages(getGeneration());
    release.complete(
      jsonEncode(
        SdkHarness.message(
          id: 'forward',
          text: 'old snapshot',
          sender: 'alice',
          status: 1,
        ),
      ),
    );
    await pending;
    await clear;
    expect(count('sendMessage'), 0);
  });

  test('drafts already queued before clear cannot become new SDK writes after the head settles', () async {
    final entered = Completer<void>();
    final release = Completer<void>();
    harness.overrideCall = (call) async {
      if (call.method == 'setConversationDraft') {
        entered.complete();
        await release.future;
      }
      return harness.respond(call);
    };
    chat.updateDraft('first');
    final first = chat.flushDraft();
    await entered.future;
    chat.updateDraft('second');
    final second = chat.flushDraft();
    final clear = harness.gateway.clearAllMessages(getGeneration());
    release.complete();
    await Future.wait([first, second, clear]);
    expect(count('setConversationDraft'), 1);
    expect(count('deleteAllMsgFromLocalAndSvr'), 1);
  });

  test('partial failure retains invalidation, does not auto reload and never replays the destructive request', () async {
    harness.overrideCall = (call) async {
      if (call.method == 'deleteAllMsgFromLocalAndSvr') {
        throw PlatformException(
          code: '10006',
          message: 'local message clear incomplete',
        );
      }
      return harness.respond(call);
    };
    final before = count('getAdvancedHistoryMessageList');
    await expectLater(
      harness.gateway.clearAllMessages(getGeneration()),
      throwsA(isA<AppFailure>()),
    );
    await flush();
    expect(chat.messages, isEmpty);
    expect(chat.failure, isA<AppFailure>());
    await Future<void>.delayed(const Duration(milliseconds: 130));
    expect(count('getAdvancedHistoryMessageList'), before);
    expect(count('deleteAllMsgFromLocalAndSvr'), 1);
  });

  test(
    'read metadata already in native cannot refill the raw cache after clear',
    () async {
      final entered = Completer<void>();
      final release = Completer<String>();
      final old = jsonEncode({
        'findResultItems': [
          {
            'conversationID': repository.conversationId,
            'messageList': harness.messages,
          },
        ],
      });
      harness.overrideCall = (call) async {
        if (call.method == 'findMessageList') {
          entered.complete();
          return release.future;
        }
        return harness.respond(call);
      };
      final pending = expectLater(
        repository.refreshReadMetadata(['source']),
        throwsA(failure(FailureKind.cancelled)),
      );
      await entered.future;
      final clear = harness.gateway.clearAllMessages(getGeneration());
      release.complete(old);
      await pending;
      await clear;
      await expectLater(
        repository.favoriteSnapshot('source'),
        throwsA(isA<AppFailure>()),
      );
    },
  );

  test(
    'late search and inbox pages cannot bring back message text or previews',
    () async {
      final searchEntered = Completer<void>();
      final inboxEntered = Completer<void>();
      final searchRelease = Completer<String>();
      final inboxRelease = Completer<String>();
      final oldSearch = jsonEncode({
        'searchResultItems': [
          {
            'conversationID': repository.conversationId,
            'messageList': harness.messages,
          },
        ],
      });
      final oldInbox = jsonEncode(harness.conversations);
      harness.overrideCall = (call) async {
        if (call.method == 'searchLocalMessages') {
          searchEntered.complete();
          return searchRelease.future;
        }
        if (call.method == 'getConversationListSplit') {
          inboxEntered.complete();
          return inboxRelease.future;
        }
        return harness.respond(call);
      };
      final pendingSearch = search.search(query: 'cached');
      final pendingInbox = inbox.refresh();
      await Future.wait([searchEntered.future, inboxEntered.future]);
      final clear = harness.gateway.clearAllMessages(getGeneration());
      searchRelease.complete(oldSearch);
      inboxRelease.complete(oldInbox);
      await Future.wait([pendingSearch, pendingInbox, clear]);
      expect(search.messages, isEmpty);
      expect(inbox.items, isEmpty);
    },
  );

  test('unsettled native work times out without dispatching clear or pretending it was cancelled', () async {
    final entered = Completer<void>();
    final release = Completer<String>();
    harness.overrideCall = (call) async {
      if (call.method == 'getAdvancedHistoryMessageList') {
        entered.complete();
        return release.future;
      }
      return harness.respond(call);
    };
    final pending = chat.refresh();
    await entered.future;
    await expectLater(
      harness.gateway.clearAllMessages(getGeneration()),
      throwsA(failure(FailureKind.timeout)),
    );
    expect(count('deleteAllMsgFromLocalAndSvr'), 0);
    release.complete(jsonEncode({'messageList': [], 'isEnd': true}));
    await pending;
    expect(chat.messages, isEmpty);
  });
}
