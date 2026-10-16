import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/presentation/chat_font_size_page.dart';

import 'chat_font_fixture.dart';
import '../../core/im/capture_theme.dart';

class _NonlinearScale extends TextScaler {
  const _NonlinearScale();
  @override double scale(double fontSize) => fontSize + 10;
  @override double get textScaleFactor => 2;
}

void main() {
  testWidgets('chat font composes nonlinear OS scaling once even for a nested quoted body', (tester) async {
    final theme = await captureTheme(tester);
    await tester.pumpWidget(MaterialApp(theme: theme, home: MediaQuery(
      data: const MediaQueryData(textScaler: _NonlinearScale()),
      child: const ChatReadingTextScale(factor: 1.5,
        child: ChatReadingTextScale(factor: 1.5,
          child: Text('嵌套引用文字', style: TextStyle(fontSize: 16)))))));
    final text = tester.renderObject<RenderParagraph>(find.text('嵌套引用文字'));
    expect(text.textScaler.scale(16), 39);
    expect(tester.takeException(), isNull);
  });
  testWidgets('chat font actual editor cancel preserves its stored factor without an OS write', (tester) async {
    final f = ChatFontFixture();
    try {
      await f.mount(tester);
      tester.widget<Slider>(find.byType(Slider)).onChanged!(1.7);
      await f.frame(tester);
      await tester.tap(find.byTooltip('取消字号设置').hitTestable());
      await f.ready(tester, find.text('打开字号'));
      expect(f.writes, isEmpty);
      expect(f.values, isEmpty);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('chat font save rejects synchronous double taps and retains a failed draft for an explicit retry', (tester) async {
    final f = ChatFontFixture();
    try {
      await f.mount(tester);
      tester.widget<Slider>(find.byType(Slider)).onChanged!(1.75);
      await f.frame(tester);
      f.failWrite = true; f.writeReply = Completer<void>(); f.writeEntered = Completer<void>();
      final save = tester.widget<FilledButton>(find.widgetWithText(FilledButton, '保存字号')).onPressed!;
      save(); save();
      await f.until(tester, () => f.writeEntered!.isCompleted);
      expect(f.writes, hasLength(1));
      f.writeReply!.complete();
      await f.until(tester, () => find.text('字号保存失败，已保留本次选择，请重试。').evaluate().length == 1);
      expect(tester.widget<Slider>(find.byType(Slider)).value, 1.75);
      f.failWrite = false;
      await tester.tap(find.widgetWithText(FilledButton, '保存字号').hitTestable());
      await f.until(tester, () => find.byType(ChatFontSizePage).evaluate().isEmpty);
      expect(f.writes, hasLength(2));
      expect(f.values.values.single, 1.75);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('chat font initial read failure stays an error until an explicit read succeeds', (tester) async {
    final f = ChatFontFixture()..failRead = true;
    try {
      await f.mount(tester);
      expect(find.text('字号读取失败，请重试或明确恢复默认。'), findsOneWidget);
      expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton, '保存字号')).onPressed, isNull);
      f.failRead = false;
      await tester.tap(find.text('重新读取').hitTestable());
      await f.until(tester, () => tester.widget<Slider>(find.byType(Slider)).onChanged != null);
      expect(f.writes, isEmpty);
      expect(tester.widget<Slider>(find.byType(Slider)).value, 1.0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  for (final dimension in ['issuer', 'tenant']) {
    testWidgets('chat font visible editor and retained save retire on a real $dimension owner replacement', (tester) async {
      final f = ChatFontFixture();
      try {
        await f.mount(tester);
        final save = tester.widget<FilledButton>(find.widgetWithText(FilledButton, '保存字号')).onPressed!;
        final owner = f.sdk.session.state.credentials!;
        await f.actual(tester, () => f.changeOwner(
          issuer: dimension == 'issuer' ? 'https://changed.example.com/' : owner.issuer,
          tenant: dimension == 'tenant' ? 'changed-tenant' : owner.tenantId));
        await f.frame(tester); save(); await f.frame(tester);
        expect(find.text('会话已变化，请返回后重新打开。'), findsOneWidget);
        expect(find.byType(Slider), findsNothing);
        expect(f.writes, isEmpty);
        expect(tester.takeException(), isNull);
      } finally { await f.close(tester); }
    });
  }
  testWidgets('chat font foreign route ABA never renews the old save but a new explicit editor works', (tester) async {
    final f = ChatFontFixture();
    try {
      await f.mount(tester);
      final save = tester.widget<FilledButton>(find.widgetWithText(FilledButton, '保存字号')).onPressed!;
      await f.coverAndReturn(tester);
      save(); await f.frame(tester);
      expect(f.writes, isEmpty);
      expect(find.text('会话已变化，请返回后重新打开。'), findsOneWidget);
      await tester.tap(find.byTooltip('取消字号设置').hitTestable());
      await f.ready(tester, find.text('打开字号'));
      await tester.tap(find.text('打开字号').hitTestable());
      await f.ready(tester, find.byType(Slider));
      await f.until(tester, () => tester.widget<Slider>(find.byType(Slider)).onChanged != null);
      await tester.tap(find.widgetWithText(FilledButton, '保存字号').hitTestable());
      await f.until(tester, () => find.byType(ChatFontSizePage).evaluate().isEmpty);
      expect(f.writes, hasLength(1));
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('chat font 360 wide editor at large OS and app size keeps preview and save usable', (tester) async {
    final f = ChatFontFixture();
    try {
      await f.mount(tester, small: true, scale: 1.6);
      tester.widget<Slider>(find.byType(Slider)).onChanged!(2.0);
      await f.frame(tester);
      final save = find.widgetWithText(FilledButton, '保存字号').hitTestable();
      expect(save, findsOneWidget);
      expect(tester.getSize(save).height, greaterThanOrEqualTo(44));
      final paragraph = tester.renderObject<RenderParagraph>(find.byKey(const ValueKey('chat-font-preview')));
      expect(paragraph.textScaler.scale(16), closeTo(51.2, .001));
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
}
