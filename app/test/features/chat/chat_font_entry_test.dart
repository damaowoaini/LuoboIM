import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/presentation/chat_font_size_page.dart';

import 'chat_font_fixture.dart';

void main() {
  testWidgets('actual chat settings opens font preview and acknowledged save changes real message geometry', (tester) async {
    final f = ChatFontFixture();
    try {
      await f.mount(tester, chat: true, small: true, scale: 1.5);
      await tester.tap(find.byTooltip('聊天设置').hitTestable());
      await f.ready(tester, find.text('联系人资料'));
      expect(find.text('聊天字号').hitTestable(), findsOneWidget);
      await tester.tap(find.text('聊天字号').hitTestable());
      await f.ready(tester, find.byType(Slider));
      await f.until(tester, () => find.byType(LinearProgressIndicator).evaluate().isEmpty);
      tester.widget<Slider>(find.byType(Slider)).onChanged!(1.5);
      await f.frame(tester);
      await tester.tap(find.widgetWithText(FilledButton, '保存字号').hitTestable());
      await f.until(tester, () => find.byType(ChatFontSizePage).evaluate().isEmpty);
      final paragraph = tester.renderObject<RenderParagraph>(find.text('实际聊天文字用于字号验证'));
      expect(paragraph.textScaler.scale(16), 36);
      expect(f.writes, hasLength(1));
      expect(f.sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('actual chat font cancel preserves both message scale and the composer draft', (tester) async {
    final f = ChatFontFixture();
    try {
      await f.mount(tester, chat: true);
      await tester.enterText(find.byType(TextField), '仍在编辑的草稿');
      await f.openFromChat(tester);
      tester.widget<Slider>(find.byType(Slider)).onChanged!(1.8);
      await f.frame(tester);
      await tester.tap(find.byTooltip('取消字号设置').hitTestable());
      await f.until(tester, () => find.byType(ChatFontSizePage).evaluate().isEmpty);
      final paragraph = tester.renderObject<RenderParagraph>(find.text('实际聊天文字用于字号验证'));
      expect(paragraph.textScaler.scale(16), 16);
      expect(tester.widget<TextField>(find.byType(TextField)).controller!.text, '仍在编辑的草稿');
      expect(f.writes, isEmpty);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('actual chat acknowledged same owner preference changes its message scale without changing messages', (tester) async {
    final f = ChatFontFixture();
    try {
      await f.mount(tester, chat: true);
      final originalMessageCount = f.sdk.messages.length;
      await f.actual(tester, () => f.preferences.save(1.6, sourceCurrent: () => true));
      await f.frame(tester);
      final paragraph = tester.renderObject<RenderParagraph>(find.text('实际聊天文字用于字号验证'));
      expect(paragraph.textScaler.scale(16), closeTo(25.6, .001));
      expect(f.sdk.messages, hasLength(originalMessageCount));
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('actual chat font settings sheet foreign coverage retires its retained row callback', (tester) async {
    final f = ChatFontFixture();
    try {
      await f.mount(tester, chat: true);
      await tester.tap(find.byTooltip('聊天设置').hitTestable());
      await f.ready(tester, find.text('聊天字号'));
      final tile = find.widgetWithText(ListTile, '聊天字号');
      final retained = tester.widget<ListTile>(tile).onTap!;
      final sheet = ModalRoute.of(tester.element(tile))!;
      final navigator = sheet.navigator!;
      final foreign = MaterialPageRoute<void>(builder: (_) => const Scaffold(body: Text('外部覆盖设置')));
      unawaited(navigator.push(foreign)); await f.ready(tester, find.text('外部覆盖设置'));
      navigator.pop(); var removed = false;
      unawaited(foreign.completed.then<void>((_) { removed = true; }));
      await f.until(tester, () => removed);
      retained(); await f.frame(tester);
      expect(find.byType(ChatFontSizePage), findsNothing);
      expect(sheet.isCurrent, isTrue);
      expect(f.writes, isEmpty);
      navigator.pop(); var closed = false;
      unawaited(sheet.completed.then<void>((_) { closed = true; }));
      await f.until(tester, () => closed);
      await f.openFromChat(tester);
      expect(find.byType(ChatFontSizePage), findsOneWidget);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
}
