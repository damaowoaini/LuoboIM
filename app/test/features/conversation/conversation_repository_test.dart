import 'dart:async';

import 'package:flutter/services.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/conversation/data/conversation_repository.dart';
import 'package:imapp/features/conversation/presentation/conversations_controller.dart';

import '../../core/im/sdk_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness harness;
  late ConversationRepository repository;
  setUp(() async {
    harness = SdkHarness();
    harness.conversations.add(SdkHarness.conversation(draft: '未发出的内容'));
    await harness.start();
    repository = ConversationRepository(harness.gateway, harness.session);
  });
  tearDown(() => harness.dispose());

  test('SDK paging, drafts, read and preference mutations preserve the exact conversation target', () async {
    final items = await repository.list(offset: 0, count: 15);
    expect(items.single.draft, '未发出的内容');
    expect(items.single.preview, '收到的真实通道测试消息');
    expect(items.single.unread, 3);
    expect(items.single.isGroup, isFalse);
    await repository.pin(items.single);
    await repository.mute(items.single);
    await repository.markRead(items.single.id);
    final updated = (await repository.list()).single;
    expect(updated.pinned, isTrue);
    expect(updated.muted, isTrue);
    expect(updated.unread, 0);
    final paging = harness.calls.firstWhere(
      (call) => call.method == 'getConversationListSplit',
    );
    expect(paging.arguments['count'], 15);
    expect(paging.arguments['offset'], 0);
    final mutation = harness.calls.firstWhere(
      (call) => call.method == 'pinConversation',
    );
    expect(mutation.arguments['conversationID'], 'single-alice-bob');
  });

  test('removing a conversation keeps messages unless the destructive method is explicitly selected', () async {
    await repository.hide('single-alice-bob');
    expect(harness.calls.last.method, 'hideConversation');
    expect(
      harness.calls.any(
        (call) => call.method == 'deleteConversationAndDeleteAllMsg',
      ),
      isFalse,
    );
    harness.conversations.add(SdkHarness.conversation());
    await repository.deleteWithMessages('single-alice-bob');
    expect(harness.calls.last.method, 'deleteConversationAndDeleteAllMsg');
  });

  test('session invalidation clears visible conversations and rejects a late response', () async {
    final controller = ConversationsController(repository);
    await controller.refresh();
    expect(controller.items, hasLength(1));
    final response = Completer<Object?>();
    final started = Completer<void>();
    harness.overrideCall = (call) async {
      if (call.method == 'getConversationListSplit') {
        started.complete();
        return response.future;
      }
      return harness.respond(call);
    };
    final refreshing = controller.refresh();
    await started.future;
    final signingOut = harness.session.signOut();
    expect(controller.items, isEmpty);
    response.complete(
      '[{"conversationID":"private-old-account","unreadCount":1}]',
    );
    await refreshing;
    await signingOut;
    expect(controller.items, isEmpty);
    controller.dispose();
  });

  test('native payload failures are mapped to safe errors and progress does not reload the inbox', () async {
    final controller = ConversationsController(repository);
    await controller.refresh();
    final before = harness.calls
        .where((call) => call.method == 'getConversationListSplit')
        .length;
    await harness.callback(ListenerType.msgSendProgressListener, {
      'type': 'onProgress',
      'data': {'clientMsgID': 'upload-elsewhere', 'progress': 5},
    });
    await Future<void>.delayed(const Duration(milliseconds: 160));
    expect(
      harness.calls.where((call) => call.method == 'getConversationListSplit'),
      hasLength(before),
    );
    harness.overrideCall = (call) async {
      if (call.method == 'getConversationListSplit') {
        throw PlatformException(code: '999', message: 'private server body');
      }
      return harness.respond(call);
    };
    await controller.refresh();
    expect(controller.failure, isA<AppFailure>());
    expect(controller.failure.toString(), isNot(contains('private')));
    controller.dispose();
  });
  test('a repository and item from the previous account cannot mutate a newly logged-in account', () async {
    final oldItem = (await repository.list()).single;
    await harness.session.signOut();
    harness.userId = 'carol';
    await harness.session.signIn((_) async => harness.credentials);
    final before = harness.calls.length;
    expect(() => repository.pin(oldItem), throwsA(isA<AppFailure>()));
    expect(() => repository.list(), throwsA(isA<AppFailure>()));
    expect(harness.calls.length, before);
    final fresh = ConversationRepository(harness.gateway, harness.session);
    expect(await fresh.list(), hasLength(1));
  });
}
