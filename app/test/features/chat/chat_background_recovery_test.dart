import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_background_store.dart';
import 'package:imapp/features/chat/presentation/chat_background_page.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import 'chat_background_fixture.dart';

Future<List<File>> copies(BackgroundFixture f) async {
  final result = <File>[];
  await for (final entry in f.support.list(recursive: true, followLinks: false)) {
    if (entry is File && entry.uri.pathSegments.last.startsWith('image_')) result.add(entry);
  }
  return result;
}

Future<void> reusableChat(WidgetTester tester, BackgroundFixture f,
    ValueNotifier<String> cid, Future<void> Function() beforeWidget) async {
  await tester.runAsync(f.start);
  await tester.runAsync(beforeWidget);
  final theme = await captureTheme(tester);
  await tester.pumpWidget(ProviderScope(overrides: [
    appRuntimeProvider.overrideWith((_) => f.font.sdk.runtime),
    imGatewayProvider.overrideWithValue(f.font.sdk.gateway),
    chatReadingStoreProvider.overrideWithValue(f.font.store),
    chatBackgroundStoreProvider.overrideWithValue(f.store),
    chatBackgroundPickerProvider.overrideWithValue(f.pick),
  ], child: MaterialApp(theme: theme, home: ValueListenableBuilder<String>(
    valueListenable: cid, builder: (_, value, _) => ChatPage(
      key: const ValueKey('deliberately-reused-chat-state'), conversationId: value)))));
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test('chat background repeated completed rename failures do not accumulate owned image copies', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      final saved = await f.seed(); f.failReplace = true;
      for (var attempt = 0; attempt < 5; ++attempt) {
        await expectLater(f.preferences.save(saved, f.source(), sourceCurrent: () => true), throwsA(isA<AppFailure>()));
        expect((await f.preferences.read()).revision, saved.revision);
        expect(await copies(f), hasLength(1));
      }
      expect(await saved.image!.exists(), isTrue); expect(await f.original.exists(), isTrue);
      f.failReplace = false;
      final retry = await f.preferences.save(saved, f.source(), sourceCurrent: () => true);
      expect(retry.revision, isNot(saved.revision)); expect(await copies(f), hasLength(1));
    } finally { await f.closeUnit(); }
  });
  test('chat background unknown ACK after actual rename never deletes the image now owned by its pointer', () async {
    final f = BackgroundFixture(); await f.start();
    final unknown = ChatBackgroundStore(supportDirectory: () async => f.support,
      replacePointer: (temporary, destination) async {
        await temporary.rename(destination.path);
        throw const FileSystemException('synthetic lost replacement ACK');
      });
    try {
      final saved = await f.seed();
      final editor = ChatBackgroundPreferences(unknown, f.font.sdk.session, f.preferences.conversationId);
      await expectLater(editor.save(saved, f.source(), sourceCurrent: () => true), throwsA(isA<AppFailure>()));
      final authoritative = await f.preferences.read();
      expect(authoritative.revision, isNot(saved.revision));
      expect(await authoritative.image!.exists(), isTrue); expect(await saved.image!.exists(), isTrue);
      expect(await copies(f), hasLength(2));
      await expectLater(editor.save(saved, f.source(), sourceCurrent: () => true),
        throwsA(isA<AppFailure>().having((e) => e.kind, 'kind', FailureKind.dataChanged)));
      expect(await authoritative.image!.exists(), isTrue); expect(await copies(f), hasLength(2));
    } finally { await unknown.flush(); unknown.dispose(); await f.closeUnit(); }
  });
  test('chat background malformed bytes allow a distinct default-only intent without enabling ordinary image save', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      final saved = await f.seed(); final pointer = File('${saved.image!.parent.path}/background.json');
      await pointer.writeAsString('{broken');
      await expectLater(f.preferences.read(), throwsA(isA<AppFailure>()));
      final damaged = await f.preferences.readForEditor();
      expect(damaged.unavailable, isTrue); expect(damaged.requiresDefaultRestore, isTrue);
      await expectLater(f.preferences.save(damaged, f.source(), sourceCurrent: () => true), throwsA(isA<AppFailure>()));
      expect(await pointer.readAsString(), '{broken'); expect(f.replacements, 1);
      final reset = await f.preferences.restoreDefault(damaged, sourceCurrent: () => true);
      expect(reset.image, isNull); expect(reset.requiresDefaultRestore, isFalse);
      expect((await f.preferences.read()).revision, reset.revision);
      // Unknown malformed metadata never grants permission to guess/delete its old referenced image.
      expect(await saved.image!.exists(), isTrue); expect(await f.original.exists(), isTrue);
    } finally { await f.closeUnit(); }
  });
  test('chat background raw-byte repair ticket refuses a changed malformed record until a new explicit read', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      final saved = await f.seed(); final pointer = File('${saved.image!.parent.path}/background.json');
      await pointer.writeAsString('broken-A'); final ticket = await f.preferences.readForEditor();
      await pointer.writeAsString('broken-B');
      await expectLater(f.preferences.restoreDefault(ticket, sourceCurrent: () => true),
        throwsA(isA<AppFailure>().having((e) => e.kind, 'kind', FailureKind.dataChanged)));
      expect(await pointer.readAsString(), 'broken-B'); expect(f.replacements, 1);
      final latest = await f.preferences.readForEditor();
      await f.preferences.restoreDefault(latest, sourceCurrent: () => true);
      expect((await f.preferences.read()).image, isNull); expect(f.replacements, 2);
    } finally { await f.closeUnit(); }
  });
  test('chat background repair ticket cannot be borrowed by another CID or a newly logged in owner', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      final saved = await f.seed(); final pointer = File('${saved.image!.parent.path}/background.json');
      await pointer.writeAsString('broken'); final ticket = await f.preferences.readForEditor();
      final otherCid = ChatBackgroundPreferences(f.store, f.font.sdk.session, 'another-cid');
      await expectLater(otherCid.restoreDefault(ticket, sourceCurrent: () => true), throwsA(isA<AppFailure>()));
      await f.font.changeOwner();
      final otherOwner = ChatBackgroundPreferences(f.store, f.font.sdk.session, f.preferences.conversationId);
      await expectLater(otherOwner.restoreDefault(ticket, sourceCurrent: () => true), throwsA(isA<AppFailure>()));
      expect(await pointer.readAsString(), 'broken'); expect(f.replacements, 1);
      expect(await saved.image!.exists(), isTrue);
    } finally { await f.closeUnit(); }
  });
  test('chat background oversized malformed record is recoverable by exact streamed raw digest without assuming empty', () async {
    final f = BackgroundFixture(); await f.start();
    try {
      final saved = await f.seed(); final pointer = File('${saved.image!.parent.path}/background.json');
      await pointer.writeAsString('x' * 8192);
      final ticket = await f.preferences.readForEditor();
      expect(ticket.unavailable, isTrue); expect(ticket.requiresDefaultRestore, isTrue);
      await f.preferences.restoreDefault(ticket, sourceCurrent: () => true);
      expect((await f.preferences.read()).image, isNull); expect(await saved.image!.exists(), isTrue);
    } finally { await f.closeUnit(); }
  });
  testWidgets('chat background damaged metadata editor disables gallery and saves default only after explicit user choice', (tester) async {
    final f = BackgroundFixture(); File? oldImage;
    try {
      await f.mount(tester, beforeBuild: () async {
        final saved = await f.seed(); oldImage = saved.image;
        await File('${saved.image!.parent.path}/background.json').writeAsString('{broken');
      });
      expect(find.textContaining('背景记录已损坏'), findsOneWidget);
      expect(tester.widget<OutlinedButton>(find.widgetWithText(OutlinedButton, '从相册选择背景')).onPressed, isNull);
      expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton, '保存背景')).onPressed, isNull);
      await f.tapDefault(tester); await f.tapSave(tester);
      await f.until(tester, () => find.byType(ChatBackgroundPage).evaluate().isEmpty);
      expect((await f.actual(tester, f.preferences.read)).image, isNull);
      expect(await f.actual(tester, oldImage!.exists), isTrue); expect(f.pickerCalls, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('chat background repair UI cannot overwrite bytes that changed after its read but a new explicit read works', (tester) async {
    final f = BackgroundFixture(); File? pointer;
    try {
      await f.mount(tester, beforeBuild: () async {
        final saved = await f.seed(); pointer = File('${saved.image!.parent.path}/background.json');
        await pointer!.writeAsString('broken-A');
      });
      await f.tapDefault(tester);
      await f.actual(tester, () => pointer!.writeAsString('broken-B')); await f.tapSave(tester);
      await f.until(tester, () => find.textContaining('背景未确认保存').evaluate().length == 1);
      expect(await f.actual(tester, pointer!.readAsString), 'broken-B'); expect(f.replacements, 1);
      await f.tapReread(tester);
      await f.until(tester, () => find.textContaining('背景记录已损坏').evaluate().length == 1 &&
        find.byType(LinearProgressIndicator).evaluate().isEmpty);
      expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton, '保存背景')).onPressed, isNull);
      await f.tapDefault(tester); await f.tapSave(tester);
      await f.until(tester, () => find.byType(ChatBackgroundPage).evaluate().isEmpty);
      expect((await f.actual(tester, f.preferences.read)).image, isNull); expect(f.replacements, 2);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('actual reused ChatPage State immediately removes the former CID background and never renews it after CID ABA', (tester) async {
    final f = BackgroundFixture(); final cid = ValueNotifier('single-alice-bob');
    try {
      await reusableChat(tester, f, cid, () async { await f.seed(); });
      await f.until(tester, () => find.byKey(const ValueKey('chat-background-image')).evaluate().length == 1);
      final originalState = tester.state(find.byType(ChatPage));
      cid.value = 'another-cid'; await f.frame(tester);
      expect(identical(tester.state(find.byType(ChatPage)), originalState), isTrue);
      expect(find.byKey(const ValueKey('chat-background-image')), findsNothing);
      cid.value = 'single-alice-bob'; await f.frame(tester);
      final previous = await f.actual(tester, f.preferences.read);
      await f.actual(tester, () => f.preferences.save(previous, f.source(), sourceCurrent: () => true));
      await f.frame(tester);
      expect(identical(tester.state(find.byType(ChatPage)), originalState), isTrue);
      expect(find.byKey(const ValueKey('chat-background-image')), findsNothing);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); cid.dispose(); }
  });
  testWidgets('actual reused ChatPage State rejects an old CID read that finishes after widget CID replacement', (tester) async {
    final f = BackgroundFixture(); final cid = ValueNotifier('single-alice-bob');
    try {
      await reusableChat(tester, f, cid, () async {
        await f.seed(); f.supportReply = Completer<void>(); f.supportEntered = Completer<void>();
      });
      await f.until(tester, () => f.supportEntered!.isCompleted);
      final originalState = tester.state(find.byType(ChatPage));
      cid.value = 'another-cid'; await f.frame(tester);
      f.supportReply!.complete(); await f.actual(tester, f.store.flush); await f.frame(tester);
      expect(identical(tester.state(find.byType(ChatPage)), originalState), isTrue);
      expect(find.byKey(const ValueKey('chat-background-image')), findsNothing);
      expect(f.replacements, 1);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); cid.dispose(); }
  });
}
