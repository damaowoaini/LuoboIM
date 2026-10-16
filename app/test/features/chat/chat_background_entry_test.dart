import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:image_picker/image_picker.dart';
import 'package:imapp/features/chat/data/chat_background_store.dart';
import 'package:imapp/features/chat/presentation/chat_background_page.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';

import '../../core/im/sdk_harness.dart';
import 'chat_background_fixture.dart';

void main() {
  testWidgets('actual chat settings opens gallery background preview and ACK changes only the real message viewport', (tester) async {
    final f = BackgroundFixture();
    try {
      await f.mount(tester, chat: true);
      await tester.enterText(find.byType(TextField), '不改变的聊天草稿');
      final oldMessages = f.font.sdk.messages.length;
      await f.actual(tester, () => f.font.preferences.save(1.5, sourceCurrent: () => true));
      await f.frame(tester);
      await f.openFromChat(tester);
      final preview = tester.renderObject<RenderParagraph>(find.text('这是一条聊天背景预览消息。'));
      expect(preview.textScaler.scale(16), 24);
      await f.select(tester); await f.tapSave(tester);
      await f.until(tester, () => find.byType(ChatBackgroundPage).evaluate().isEmpty);
      final surface = find.descendant(of: find.byType(ChatPage), matching: find.byKey(const ValueKey('chat-background-image')));
      await f.until(tester, () => surface.evaluate().length == 1);
      expect(surface, findsOneWidget);
      final viewport = find.ancestor(of: surface, matching: find.byType(ChatBackgroundSurface));
      expect(viewport, findsOneWidget);
      expect(find.descendant(of: viewport, matching: find.byType(TextField)), findsNothing);
      expect(tester.widget<TextField>(find.byType(TextField)).controller!.text, '不改变的聊天草稿');
      expect(f.font.sdk.messages, hasLength(oldMessages));
      expect(f.font.sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
      final saved = await f.actual(tester, f.preferences.read);
      expect((tester.widget<Image>(surface).image as FileImage).file.path, saved.image!.path);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('actual chat background settings sheet covered and returned cannot revive its retained row callback', (tester) async {
    final f = BackgroundFixture();
    try {
      await f.mount(tester, chat: true);
      await tester.tap(find.byTooltip('聊天设置').hitTestable());
      await f.ready(tester, find.text('聊天背景'));
      final row = find.widgetWithText(ListTile, '聊天背景');
      final retained = tester.widget<ListTile>(row).onTap!;
      final sheet = ModalRoute.of(tester.element(row))!;
      final navigator = sheet.navigator!;
      final foreign = MaterialPageRoute<void>(builder: (_) => const Scaffold(body: Text('覆盖背景设置')));
      unawaited(navigator.push(foreign)); await f.ready(tester, find.text('覆盖背景设置'));
      navigator.pop(); var complete = false;
      unawaited(foreign.completed.then<void>((_) { complete = true; }));
      await f.until(tester, () => complete);
      retained(); await f.frame(tester);
      expect(find.byType(ChatBackgroundPage), findsNothing); expect(sheet.isCurrent, isTrue);
      expect(f.pickerCalls, 0); expect(f.replacements, 0);
      navigator.pop(); var removed = false;
      unawaited(sheet.completed.then<void>((_) { removed = true; }));
      await f.until(tester, () => removed);
      await f.openFromChat(tester);
      expect(find.byType(ChatBackgroundPage), findsOneWidget);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('actual chat switching to another exact CID while gallery is open cannot write either conversation or pop the new chat', (tester) async {
    final f = BackgroundFixture()..pickerReply = Completer<XFile?>();
    try {
      await f.mount(tester, chat: true); await f.openFromChat(tester);
      await tester.tap(find.text('从相册选择背景').hitTestable());
      await f.until(tester, () => f.pickerCalls == 1);
      f.font.sdk.conversations.add(SdkHarness.conversation(id: 'another-exact-cid', title: '另一个会话'));
      final navigator = Navigator.of(tester.element(find.byType(ChatBackgroundPage)));
      final next = MaterialPageRoute<void>(builder: (_) => const ChatPage(conversationId: 'another-exact-cid'));
      unawaited(navigator.push(next));
      final incoming = find.byWidgetPredicate((w) => w is ChatPage && w.conversationId == 'another-exact-cid');
      await f.until(tester, () => incoming.evaluate().length == 1);
      await f.ready(tester, incoming);
      await f.until(tester, () => next.isCurrent && next.animation?.status == AnimationStatus.completed &&
        incoming.evaluate().length == 1 && identical(ModalRoute.of(tester.element(incoming)), next));
      f.pickerReply!.complete(XFile(f.original.path)); await f.frame(tester);
      expect(find.byWidgetPredicate((w) => w is ChatPage && w.conversationId == 'another-exact-cid'), findsOneWidget);
      expect(next.isCurrent, isTrue); expect(f.replacements, 0);
      expect((await f.actual(tester, f.preferences.read)).revision, 'absent');
      final other = ChatBackgroundPreferences(f.store, f.font.sdk.session, 'another-exact-cid');
      expect((await f.actual(tester, other.read)).revision, 'absent');
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('actual chat immediately hides a saved background when its original account is invalidated', (tester) async {
    final f = BackgroundFixture();
    try {
      await f.mount(tester, chat: true, beforeBuild: () async { await f.seed(); });
      await f.until(tester, () => find.byKey(const ValueKey('chat-background-image')).evaluate().length == 1);
      expect(find.byKey(const ValueKey('chat-background-image')), findsOneWidget);
      await f.actual(tester, f.font.sdk.session.signOut); await f.frame(tester);
      expect(find.byKey(const ValueKey('chat-background-image')), findsNothing);
      expect(await f.actual(tester, f.original.exists), isTrue);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
}
