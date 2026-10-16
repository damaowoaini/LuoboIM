import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:image_picker/image_picker.dart';
import 'package:imapp/features/chat/data/chat_background_store.dart';
import 'package:imapp/features/chat/presentation/chat_background_page.dart';

import 'chat_background_fixture.dart';

void main() {
  testWidgets('chat background actual gallery selection stages a preview but cancel never commits or deletes the picker original', (tester) async {
    final f = BackgroundFixture();
    try {
      await f.mount(tester); await f.select(tester);
      final image = tester.widget<Image>(find.byKey(const ValueKey('chat-background-image')));
      final staged = (image.image as FileImage).file;
      expect(staged.path, isNot(f.original.path)); expect(f.replacements, 0);
      await tester.tap(find.byTooltip('取消背景设置').hitTestable());
      await f.until(tester, () => find.byType(ChatBackgroundPage).evaluate().isEmpty);
      await f.until(tester, () => !staged.existsSync());
      expect((await f.actual(tester, f.preferences.read)).revision, 'absent');
      expect(await f.actual(tester, f.original.exists), isTrue);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('chat background gallery cancellation keeps the acknowledged previous image unchanged', (tester) async {
    final f = BackgroundFixture(); ChatBackgroundSnapshot? seed;
    try {
      await f.mount(tester, beforeBuild: () async { seed = await f.seed(); });
      f.pickerReplies.add(() async => null); await f.select(tester);
      final preview = tester.widget<Image>(find.byKey(const ValueKey('chat-background-image')));
      expect((preview.image as FileImage).file.path, seed!.image!.path);
      expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton, '保存背景')).onPressed, isNull);
      expect(f.replacements, 1);
      expect(await f.actual(tester, seed!.image!.exists), isTrue);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('chat background explicit save ACK makes a durable copy and closes exactly its owned editor', (tester) async {
    final f = BackgroundFixture();
    try {
      await f.mount(tester); await f.select(tester); await f.tapSave(tester);
      await f.until(tester, () => find.byType(ChatBackgroundPage).evaluate().isEmpty);
      final saved = await f.actual(tester, f.preferences.read);
      expect(saved.image, isNotNull); expect(saved.image!.path, isNot(f.original.path));
      expect(await f.actual(tester, saved.image!.exists), isTrue); expect(f.replacements, 1);
      await f.open(tester);
      expect((tester.widget<Image>(find.byKey(const ValueKey('chat-background-image'))).image as FileImage).file.path,
        saved.image!.path);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('chat background default preview cancel preserves the previous persisted image', (tester) async {
    final f = BackgroundFixture(); ChatBackgroundSnapshot? seed;
    try {
      await f.mount(tester, beforeBuild: () async { seed = await f.seed(); });
      await f.tapDefault(tester); await f.frame(tester);
      expect(find.byKey(const ValueKey('chat-background-image')), findsNothing);
      await tester.tap(find.byTooltip('取消背景设置').hitTestable());
      await f.until(tester, () => find.byType(ChatBackgroundPage).evaluate().isEmpty);
      expect((await f.actual(tester, f.preferences.read)).revision, seed!.revision);
      expect(await f.actual(tester, seed!.image!.exists), isTrue); expect(f.replacements, 1);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('chat background failed default save retains the old image until an explicit acknowledged retry', (tester) async {
    final f = BackgroundFixture(); ChatBackgroundSnapshot? seed;
    try {
      await f.mount(tester, beforeBuild: () async { seed = await f.seed(); });
      await f.tapDefault(tester); f.failReplace = true;
      await f.tapSave(tester);
      await f.until(tester, () => find.textContaining('背景未确认保存').evaluate().length == 1);
      expect((await f.actual(tester, f.preferences.read)).revision, seed!.revision);
      expect(await f.actual(tester, seed!.image!.exists), isTrue);
      f.failReplace = false; await f.tapSave(tester);
      await f.until(tester, () => find.byType(ChatBackgroundPage).evaluate().isEmpty);
      expect((await f.actual(tester, f.preferences.read)).image, isNull);
      expect(await f.actual(tester, seed!.image!.exists), isFalse);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('chat background invalid gallery bytes show a retryable error without losing the old preview', (tester) async {
    final f = BackgroundFixture(); ChatBackgroundSnapshot? seed;
    try {
      await f.mount(tester, beforeBuild: () async { seed = await f.seed(); });
      final invalid = File('${f.support.path}/invalid.png');
      await f.actual(tester, () => invalid.writeAsBytes([0, 1, 2]));
      f.pickerReplies.add(() async => XFile(invalid.path)); await f.select(tester);
      expect(find.textContaining('图片未能读取'), findsOneWidget);
      expect((tester.widget<Image>(find.byKey(const ValueKey('chat-background-image'))).image as FileImage).file.path,
        seed!.image!.path);
      expect(f.replacements, 1);
      await f.select(tester); expect(find.textContaining('图片未能读取'), findsNothing);
      await f.tapSave(tester); await f.until(tester, () => find.byType(ChatBackgroundPage).evaluate().isEmpty);
      expect((await f.actual(tester, f.preferences.read)).revision, isNot(seed!.revision));
      expect(await f.actual(tester, invalid.exists), isTrue);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('chat background closing during the OS picker prevents later staging metadata or foreign route pop', (tester) async {
    final f = BackgroundFixture()..pickerReply = Completer<XFile?>();
    try {
      await f.mount(tester);
      await tester.tap(find.text('从相册选择背景').hitTestable());
      await f.until(tester, () => f.pickerCalls == 1);
      await tester.tap(find.byTooltip('取消背景设置').hitTestable());
      await f.until(tester, () => find.byType(ChatBackgroundPage).evaluate().isEmpty);
      f.pickerReply!.complete(XFile(f.original.path)); await f.frame(tester);
      expect(find.text('打开背景'), findsOneWidget); expect(f.replacements, 0);
      expect((await f.actual(tester, f.preferences.read)).image, isNull);
      expect(await f.actual(tester, f.original.exists), isTrue);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  for (final dimension in ['issuer', 'tenant']) {
    testWidgets('chat background a real $dimension replacement hides the editor and rejects a late gallery result', (tester) async {
      final f = BackgroundFixture()..pickerReply = Completer<XFile?>();
      try {
        await f.mount(tester); await tester.tap(find.text('从相册选择背景').hitTestable());
        await f.until(tester, () => f.pickerCalls == 1);
        final owner = f.font.sdk.session.state.credentials!;
        await f.actual(tester, () => f.font.changeOwner(
          issuer: dimension == 'issuer' ? 'https://new-owner.example.com/' : owner.issuer,
          tenant: dimension == 'tenant' ? 'new-tenant' : owner.tenantId));
        f.pickerReply!.complete(XFile(f.original.path)); await f.frame(tester);
        expect(find.text('会话已变化，请返回后重新打开。'), findsOneWidget);
        expect(find.byKey(const ValueKey('chat-background-image')), findsNothing);
        expect(f.replacements, 0);
        expect(await f.actual(tester, f.original.exists), isTrue);
        expect(tester.takeException(), isNull);
      } finally { await f.close(tester); }
    });
  }
  testWidgets('chat background foreign route ABA retires an existing save but a fresh explicit editor remains usable', (tester) async {
    final f = BackgroundFixture();
    try {
      await f.mount(tester); await f.select(tester);
      final retained = tester.widget<FilledButton>(find.widgetWithText(FilledButton, '保存背景')).onPressed!;
      await f.coverAndReturn(tester); retained(); await f.frame(tester);
      expect(f.replacements, 0); expect(find.text('会话已变化，请返回后重新打开。'), findsOneWidget);
      await tester.tap(find.byTooltip('取消背景设置').hitTestable());
      await f.until(tester, () => find.byType(ChatBackgroundPage).evaluate().isEmpty);
      await f.open(tester); await f.select(tester); await f.tapSave(tester);
      await f.until(tester, () => find.byType(ChatBackgroundPage).evaluate().isEmpty);
      expect(f.replacements, 1); expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('chat background synchronous save double taps dispatch only one physical pointer replacement', (tester) async {
    final f = BackgroundFixture()..replaceReply = Completer<void>()..replaceEntered = Completer<void>();
    try {
      await f.mount(tester); await f.select(tester);
      final save = tester.widget<FilledButton>(find.widgetWithText(FilledButton, '保存背景')).onPressed!;
      save(); save();
      await f.until(tester, () => f.replaceEntered!.isCompleted);
      expect(f.replacements, 1);
      expect(find.byType(ChatBackgroundPage), findsOneWidget);
      f.replaceReply!.complete();
      await f.until(tester, () => find.byType(ChatBackgroundPage).evaluate().isEmpty);
      expect(f.replacements, 1); expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('chat background missing persistent image stays an explicit error and default requires a successful commit', (tester) async {
    final f = BackgroundFixture();
    try {
      await f.mount(tester, beforeBuild: () async { final saved = await f.seed(); await saved.image!.delete(); });
      expect(find.textContaining('背景图片已不可用'), findsOneWidget);
      expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton, '保存背景')).onPressed, isNull);
      await f.tapDefault(tester);
      await f.tapSave(tester); await f.until(tester, () => find.byType(ChatBackgroundPage).evaluate().isEmpty);
      final value = await f.actual(tester, f.preferences.read);
      expect(value.unavailable, isFalse); expect(value.image, isNull); expect(f.replacements, 2);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('chat background ACK after foreign coverage never pops the covering route or applies a new account target', (tester) async {
    final f = BackgroundFixture()..replaceReply = Completer<void>()..replaceEntered = Completer<void>();
    try {
      await f.mount(tester); await f.select(tester); await f.tapSave(tester);
      await f.until(tester, () => f.replaceEntered!.isCompleted);
      final navigator = Navigator.of(tester.element(find.byType(ChatBackgroundPage)));
      final foreign = MaterialPageRoute<void>(builder: (_) => const Scaffold(body: Text('保存时外来页面')));
      unawaited(navigator.push(foreign)); await f.ready(tester, find.text('保存时外来页面'));
      f.replaceReply!.complete(); await f.actual(tester, f.store.flush); await f.frame(tester);
      expect(foreign.isCurrent, isTrue); expect(find.text('保存时外来页面'), findsOneWidget);
      expect(f.replacements, 1); expect((await f.actual(tester, f.preferences.read)).image, isNotNull);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('chat background 360 wide at large text keeps gallery default and save reachable without overflow', (tester) async {
    final f = BackgroundFixture();
    try {
      await f.mount(tester, small: true);
      await tester.ensureVisible(find.text('从相册选择背景'));
      expect(find.text('从相册选择背景').hitTestable(), findsOneWidget);
      await f.select(tester);
      await tester.ensureVisible(find.text('恢复默认背景'));
      expect(find.text('恢复默认背景').hitTestable(), findsOneWidget);
      final save = find.widgetWithText(FilledButton, '保存背景').hitTestable();
      expect(save, findsOneWidget); expect(tester.getSize(save).height, greaterThanOrEqualTo(44));
      await f.tapSave(tester); await f.until(tester, () => find.byType(ChatBackgroundPage).evaluate().isEmpty);
      expect(f.replacements, 1); expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
}
