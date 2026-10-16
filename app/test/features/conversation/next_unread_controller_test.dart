import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/conversation/presentation/conversations_controller.dart';

import 'next_unread_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('next unread cycles the complete ordered inbox including muted rows and wraps by CID', () async {
    final f = NextUnreadFixture(); await f.start(); final c = ConversationsController(f.repository);
    try {
      await c.refresh(); expect(c.items, hasLength(40));
      final first = await c.nextUnread(visibleAnchor: 'inbox-0', sourceCurrent: () => true);
      expect(first.id, 'inbox-84'); expect(c.items, hasLength(125)); expect(c.hasMore, isFalse);
      c.completeUnread(first, reached: true);
      final second = await c.nextUnread(sourceCurrent: () => true);
      expect(second.id, 'inbox-124'); c.completeUnread(second, reached: true);
      final wrapped = await c.nextUnread(sourceCurrent: () => true);
      expect(wrapped.id, 'inbox-84'); c.completeUnread(wrapped, reached: true);
      expect(c.lastUnreadId, 'inbox-84'); expect(f.writes, 0);
    } finally { c.dispose(); await f.closeUnit(); }
  });

  test('next unread does not advance its CID anchor until actual row reveal succeeds', () async {
    final f = NextUnreadFixture(); await f.start(); final c = ConversationsController(f.repository);
    try {
      final first = await c.nextUnread(sourceCurrent: () => true);
      c.completeUnread(first, reached: false); expect(c.lastUnreadId, isNull);
      final again = await c.nextUnread(sourceCurrent: () => true);
      expect(again.id, first.id); c.completeUnread(again, reached: true);
    } finally { c.dispose(); await f.closeUnit(); }
  });

  test('next unread handles a single unread and a complete all-read inbox without changing counts', () async {
    final f = NextUnreadFixture(); await f.start(count: 2, unread: const {1});
    final c = ConversationsController(f.repository);
    try {
      final first = await c.nextUnread(sourceCurrent: () => true); expect(first.id, 'inbox-1'); c.completeUnread(first, reached: true);
      final again = await c.nextUnread(sourceCurrent: () => true); expect(again.id, 'inbox-1'); c.completeUnread(again, reached: true);
      f.sdk.conversations[1]['unreadCount'] = 0;
      final empty = await c.nextUnread(sourceCurrent: () => true); expect(empty.id, isNull); c.completeUnread(empty, reached: false);
      expect(f.writes, 0);
    } finally { c.dispose(); await f.closeUnit(); }
  });

  test('next unread prevents concurrent complete reads while allowing a later explicit new intent', () async {
    final f = NextUnreadFixture(); await f.start(); final c = ConversationsController(f.repository);
    try {
      f.allReply = Completer<dynamic>(); f.allEntered = Completer<void>();
      final first = c.nextUnread(sourceCurrent: () => true); await f.allEntered!.future;
      await expectLater(c.nextUnread(sourceCurrent: () => true), throwsA(isA<AppFailure>())); expect(f.reads, 1);
      f.allReply!.complete(jsonEncode(f.sdk.conversations)); final location = await first;
      c.completeUnread(location, reached: false); f.allReply = null;
      final retry = await c.nextUnread(sourceCurrent: () => true); expect(f.reads, 2); c.completeUnread(retry, reached: true);
    } finally { c.dispose(); await f.closeUnit(); }
  });

  test('SDK unread changes retire a pending next-unread request before refresh debounce', () async {
    final f = NextUnreadFixture(); await f.start(); final c = ConversationsController(f.repository);
    try {
      f.allReply = Completer<dynamic>(); f.allEntered = Completer<void>();
      final loading = c.nextUnread(sourceCurrent: () => true);
      final assertion = expectLater(loading, throwsA(isA<AppFailure>())); await f.allEntered!.future;
      await f.changed(); expect(c.locatingUnread, isFalse);
      f.allReply!.complete(jsonEncode(f.sdk.conversations)); await assertion;
      expect(c.lastUnreadId, isNull);
    } finally { c.dispose(); await f.closeUnit(); }
  });

  test('next unread replaces an older paginated response without allowing its late append to corrupt the snapshot', () async {
    final f = NextUnreadFixture(); await f.start(); final c = ConversationsController(f.repository);
    final reply = Completer<dynamic>(), entered = Completer<void>();
    try {
      await c.refresh(); final normal = f.sdk.overrideCall!;
      f.sdk.overrideCall = (call) async {
        if (call.method == 'getConversationListSplit' && call.arguments['offset'] == 40) {
          entered.complete(); return reply.future;
        }
        return normal(call);
      };
      final oldPage = c.loadMore(); await entered.future;
      final target = await c.nextUnread(sourceCurrent: () => true);
      reply.complete(jsonEncode([{'conversationID': 'late-wrong', 'unreadCount': 2}])); await oldPage;
      expect(c.items, hasLength(125)); expect(c.items.any((item) => item.id == 'late-wrong'), isFalse);
      expect(target.id, 'inbox-84'); c.completeUnread(target, reached: true);
    } finally { if (!reply.isCompleted) reply.complete('[]'); c.dispose(); await f.closeUnit(); }
  });

  test('deleting the previous anchor CID uses the real current viewport anchor on a new explicit cycle', () async {
    final f = NextUnreadFixture(); await f.start(); final c = ConversationsController(f.repository);
    try {
      final first = await c.nextUnread(sourceCurrent: () => true); c.completeUnread(first, reached: true);
      f.sdk.conversations.removeWhere((row) => row['conversationID'] == 'inbox-84');
      final next = await c.nextUnread(visibleAnchor: 'inbox-100', sourceCurrent: () => true);
      expect(next.id, 'inbox-124'); c.completeUnread(next, reached: true);
      expect(f.writes, 0);
    } finally { c.dispose(); await f.closeUnit(); }
  });
}
