import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:image_picker/image_picker.dart';
import 'package:imapp/features/chat/presentation/saved_emoji_sheet.dart';
import 'package:imapp/features/chat/domain/saved_emoji.dart';

import '../../core/im/sdk_harness.dart';
import 'saved_emoji_fixture.dart';

void main() {
  testWidgets(
    'saved emoji actual gallery private staging upload ACK and list save keep picker original',
    (tester) async {
      final f = SavedEmojiFixture();
      try {
        await f.mount(tester);
        await f.savedTab(tester);
        await f.choose(tester);
        expect(f.uploads, isEmpty);
        expect(f.writes, 0);
        await f.tap(tester, find.text('保存到常用表情'));
        await f.font.until(
          tester,
          () => f.values.values.any((s) => s.contains('upload_ack')),
        );
        await f.font.until(
          tester,
          () => find.byType(LinearProgressIndicator).evaluate().isEmpty,
        );
        expect(f.uploads.length, 1);
        expect(f.bodies.single, isNotEmpty);
        final state = await f.actual(
          tester,
          () => f.store.read(f.access, current: () => true),
        );
        expect(state.entries.single.url.path, '/uploaded.png');
        expect(state.pendingUpload, isNull);
        expect(await tester.runAsync(f.original.exists), isTrue);
        expect(f.sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
        expect(
          f.uploads.single.headers.keys.any(
            (k) => ['token', 'authorization'].contains(k.toLowerCase()),
          ),
          isFalse,
        );
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'saved emoji gallery cancellation makes no library write upload or message',
    (tester) async {
      final f = SavedEmojiFixture();
      f.picker = () async => [];
      try {
        await f.mount(tester);
        await f.savedTab(tester);
        await f.tap(tester, find.text('相册添加'));
        await f.font.until(
          tester,
          () => find.byType(LinearProgressIndicator).evaluate().isEmpty,
        );
        expect(f.uploads, isEmpty);
        expect(f.writes, 0);
        expect(f.faces, 0);
        expect(find.bySemanticsLabel('待保存的常用表情预览'), findsNothing);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'saved emoji known upload ACK retries only preferences without a second physical POST',
    (tester) async {
      final f = SavedEmojiFixture();
      f.beforeComplete = () async {
        f.failWrite = true;
      };
      try {
        await f.mount(tester);
        await f.savedTab(tester);
        await f.choose(tester);
        await f.tap(tester, find.text('保存到常用表情'));
        await f.font.until(
          tester,
          () => find.text('重试保存已上传图片').evaluate().length == 1,
        );
        expect(f.uploads.length, 1);
        f.failWrite = false;
        await f.tap(tester, find.text('重试保存已上传图片'));
        await f.font.until(
          tester,
          () => f.values.values.any((s) => s.contains('upload_ack')),
        );
        expect(f.uploads.length, 1);
        expect(f.faces, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'saved emoji lost upload reply remains visible and reread never uploads again',
    (tester) async {
      final f = SavedEmojiFixture();
      f.beforeComplete = () async {
        throw StateError('synthetic reply loss');
      };
      try {
        await f.mount(tester);
        await f.savedTab(tester);
        await f.choose(tester);
        await f.tap(tester, find.text('保存到常用表情'));
        await f.font.until(
          tester,
          () => find.text('上传结果未确认，请先核对。不会自动再次上传。').evaluate().length == 1,
        );
        expect(f.uploads.length, 1);
        final persisted = await f.actual(
          tester,
          () => f.store.read(f.access, current: () => true),
        );
        expect(persisted.pendingUpload, isNotNull);
        expect(persisted.entries, isEmpty);
        await f.tap(tester, find.text('重新读取'));
        await f.font.until(
          tester,
          () => find.byType(LinearProgressIndicator).evaluate().isEmpty,
        );
        expect(f.uploads.length, 1);
        expect(f.faces, 0);
        await f.tap(tester, find.text('结束此上传记录'));
        await f.font.until(
          tester,
          () => find.text('结束此上传记录').evaluate().isEmpty,
        );
        expect(f.uploads.length, 1);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'saved emoji owner change during real picker rejects staging upload and late sheet projection',
    (tester) async {
      final f = SavedEmojiFixture();
      final picker = Completer<List<XFile>>();
      f.picker = () => picker.future;
      try {
        await f.mount(tester);
        await f.savedTab(tester);
        await f.tap(tester, find.text('相册添加'));
        await f.actual(tester, () => f.font.changeOwner());
        picker.complete([XFile(f.original.path)]);
        await f.font.frame(tester);
        expect(f.uploads, isEmpty);
        expect(f.writes, 0);
        expect(f.faces, 0);
        expect(find.text('本次表情选择已失效，请关闭后重新打开。'), findsOneWidget);
      } finally {
        if (!picker.isCompleted) picker.complete([]);
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'saved emoji actual ChatPage system picker resumes after legitimate inactive paused lifecycle and can save',
    (tester) async {
      final f = SavedEmojiFixture();
      final picker = Completer<List<XFile>>();
      final entered = Completer<void>();
      f.picker = () {
        entered.complete();
        return picker.future;
      };
      try {
        await f.mount(tester, chat: true);
        await f.tap(tester, find.byTooltip('插入表情'));
        await f.savedTab(tester);
        await f.tap(tester, find.text('相册添加'));
        await f.font.until(tester, () => entered.isCompleted);
        for (final phase in [
          AppLifecycleState.inactive,
          AppLifecycleState.hidden,
          AppLifecycleState.paused,
        ]) {
          tester.binding.handleAppLifecycleStateChanged(phase);
        }
        expect(f.uploads, isEmpty);
        expect(f.writes, 0);
        for (final phase in [
          AppLifecycleState.hidden,
          AppLifecycleState.inactive,
          AppLifecycleState.resumed,
        ]) {
          tester.binding.handleAppLifecycleStateChanged(phase);
        }
        picker.complete([XFile(f.original.path)]);
        await f.font.until(
          tester,
          () => find.bySemanticsLabel('待保存的常用表情预览').evaluate().length == 1,
        );
        await f.font.frame(tester);
        await f.tap(tester, find.text('保存到常用表情'));
        await f.font.until(
          tester,
          () => f.values.values.any((s) => s.contains('upload_ack')),
        );
        await f.font.until(
          tester,
          () => find.byType(LinearProgressIndicator).evaluate().isEmpty,
        );
        expect(f.uploads.length, 1);
        expect(f.faces, 0);
        expect(find.text('本次表情选择已失效，请关闭后重新打开。'), findsNothing);
      } finally {
        if (!picker.isCompleted) {
          picker.complete([]);
        }
        if (tester.binding.lifecycleState == AppLifecycleState.paused) {
          tester.binding.handleAppLifecycleStateChanged(
            AppLifecycleState.hidden,
          );
        }
        if (tester.binding.lifecycleState == AppLifecycleState.hidden) {
          tester.binding.handleAppLifecycleStateChanged(
            AppLifecycleState.inactive,
          );
        }
        if (tester.binding.lifecycleState == AppLifecycleState.inactive) {
          tester.binding.handleAppLifecycleStateChanged(
            AppLifecycleState.resumed,
          );
        }
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'saved emoji foreign route return cannot revive an already opened sheet callback',
    (tester) async {
      final f = SavedEmojiFixture();
      var picks = 0;
      f.picker = () async {
        picks++;
        return [];
      };
      try {
        await f.mount(tester);
        await f.savedTab(tester);
        final button = find.widgetWithText(TextButton, '相册添加');
        final old = tester.widget<TextButton>(button).onPressed;
        expect(old, isNotNull);
        expect(button.hitTestable(), findsOneWidget);
        final navigator = Navigator.of(
          tester.element(find.byType(SavedEmojiSheet)),
        );
        final foreign = MaterialPageRoute<void>(
          builder: (_) => const Scaffold(body: Text('表情外来页面')),
        );
        unawaited(navigator.push(foreign));
        await f.font.ready(tester, find.text('表情外来页面'));
        navigator.pop();
        var completed = false;
        unawaited(
          foreign.completed.then<void>((_) {
            completed = true;
          }),
        );
        await f.font.until(tester, () => completed);
        old!();
        await f.font.frame(tester);
        expect(picks, 0);
        expect(f.uploads, isEmpty);
        expect(f.faces, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'saved emoji selection deletion persists while unrelated items remain sendable on reopen',
    (tester) async {
      final f = SavedEmojiFixture();
      try {
        await tester.runAsync(f.start);
        await f.actual(tester, () => f.seed());
        final old = await f.actual(
          tester,
          () => f.store.read(f.access, current: () => true),
        );
        await f.actual(
          tester,
          () => f.store.add(
            f.access,
            old,
            f.uploadEntry('second'),
            current: () => true,
          ),
        );
        await f.mount(tester);
        await f.savedTab(tester);
        await f.tap(tester, find.text('管理'));
        final checks = find.byType(Checkbox);
        expect(checks, findsNWidgets(2));
        // Explicitly choose the item with known grid order; no generic first-page route lookup.
        await f.tap(tester, checks.at(0));
        await f.tap(tester, find.text('删除所选（1）'));
        await f.font.until(
          tester,
          () => find.byType(Checkbox).evaluate().length == 1,
        );
        final rows = await f.actual(
          tester,
          () => f.store.read(f.access, current: () => true),
        );
        expect(rows.entries.single.url.path, '/owned.png');
        expect(await tester.runAsync(f.original.exists), isTrue);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'saved emoji actual 360 large text library controls and grid remain reachable without overflow',
    (tester) async {
      final f = SavedEmojiFixture();
      try {
        await f.mount(tester, small: true);
        await f.savedTab(tester);
        await f.choose(tester);
        await f.tap(tester, find.text('保存到常用表情'));
        await f.font.until(
          tester,
          () => f.values.values.any((s) => s.contains('upload_ack')),
        );
        await f.font.until(
          tester,
          () => find.byType(LinearProgressIndicator).evaluate().isEmpty,
        );
        await f.tap(tester, find.text('管理'));
        expect(find.text('完成管理').hitTestable(), findsOneWidget);
        expect(tester.takeException(), isNull);
        final button = find.widgetWithText(TextButton, '完成管理');
        expect(tester.getSize(button).height, greaterThanOrEqualTo(44));
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'saved emoji clearing another source CID hides its existing image without renewing the original proof',
    (tester) async {
      final f = SavedEmojiFixture();
      try {
        await tester.runAsync(f.start);
        f.sdk.conversations.add(SdkHarness.conversation(id: 'other-cid'));
        f.sdk.messages.add({
          ...f.image(id: 'other-source'),
          'conversationID': 'other-cid',
        });
        final entry = SavedEmoji(
          url: Uri.parse('https://objects.example.com/source.png'),
          width: 1,
          height: 1,
          source: const SavedEmojiSource('other-cid', 'other-source'),
        );
        await f.actual(tester, () => f.seed(entry));
        await f.mount(tester);
        await f.savedTab(tester);
        final send = find.byWidgetPredicate(
          (widget) => widget is IconButton && widget.tooltip == '发送常用表情',
        );
        expect(send.hitTestable(), findsOneWidget);
        expect(tester.widget<IconButton>(send).onPressed, isNotNull);
        expect(find.text('原消息不可保留'), findsNothing);
        final reads = f.finds.length;
        await f.actual(
          tester,
          () => f.sdk.gateway.clearConversationMessages(
            f.repository.generation,
            'other-cid',
          ),
        );
        await f.font.until(
          tester,
          () => find.text('原消息不可保留').evaluate().length == 1,
        );
        expect(f.finds.length, reads);
        expect(f.faces, 0);
        expect(tester.widget<IconButton>(send).onPressed, isNull);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'saved emoji gallery uploads complete animated GIF bytes as image gif and renders a real animated provider',
    (tester) async {
      final f = SavedEmojiFixture();
      try {
        await tester.runAsync(f.start);
        final gif = File('${f.sdk.directory.path}/selected.gif');
        f.displayBytes = twoFrameEmojiGif();
        await tester.runAsync(() => gif.writeAsBytes(f.displayBytes!));
        f.picker = () async => [XFile(gif.path)];
        await f.mount(tester);
        await f.savedTab(tester);
        await f.choose(tester);
        await f.tap(tester, find.text('保存到常用表情'));
        await f.font.until(
          tester,
          () => f.values.values.any((s) => s.contains('upload_ack')),
        );
        await f.font.until(
          tester,
          () => find.byType(LinearProgressIndicator).evaluate().isEmpty,
        );
        final form = f.metadata.requests.singleWhere(
          (r) => r.path.endsWith('initiate_form_data'),
        );
        expect(form.data['contentType'], 'image/gif');
        final sent = f.bodies.single, bytes = f.displayBytes!;
        expect([
          for (var i = 0; i <= sent.length - bytes.length; i++)
            if (List.generate(
              bytes.length,
              (j) => sent[i + j] == bytes[j],
            ).every((v) => v))
              i,
        ], hasLength(1));
        final shown = find.descendant(
          of: find.byTooltip('发送常用表情'),
          matching: find.byType(Image),
        );
        expect(shown, findsOneWidget);
        final provider = tester.widget<Image>(shown).image;
        expect(provider, isA<MemoryImage>());
        final codec = await f.actual(
          tester,
          () => ui.instantiateImageCodec((provider as MemoryImage).bytes),
        );
        try {
          expect(codec.frameCount, 2);
        } finally {
          codec.dispose();
        }
        expect(await tester.runAsync(gif.readAsBytes), bytes);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'saved emoji known receipt retry cannot consume another upload nonce after concurrent list changes',
    (tester) async {
      final f = SavedEmojiFixture();
      f.beforeComplete = () async {
        f.failWrite = true;
      };
      try {
        await f.mount(tester);
        await f.savedTab(tester);
        await f.choose(tester);
        await f.tap(tester, find.text('保存到常用表情'));
        await f.font.until(
          tester,
          () => find.text('重试保存已上传图片').evaluate().length == 1,
        );
        final oldButton = tester
            .widget<FilledButton>(
              find.widgetWithText(FilledButton, '重试保存已上传图片'),
            )
            .onPressed;
        expect(oldButton, isNotNull);
        f.failWrite = false;
        final old = await f.actual(
          tester,
          () => f.store.read(f.access, current: () => true),
        );
        final ended = await f.actual(
          tester,
          () => f.store.endPending(f.access, old, current: () => true),
        );
        final other = await f.actual(
          tester,
          () => f.store.beginUpload(f.access, ended, current: () => true),
        );
        await f.font.until(
          tester,
          () => find.byType(LinearProgressIndicator).evaluate().isEmpty,
        );
        oldButton!();
        await f.font.frame(tester);
        final actual = await f.actual(
          tester,
          () => f.store.read(f.access, current: () => true),
        );
        expect(actual.pendingUpload, other.pendingUpload);
        expect(actual.entries, isEmpty);
        expect(f.uploads.length, 1);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'saved emoji gallery multi selection persists every acknowledged image in order without sending',
    (tester) async {
      final f = SavedEmojiFixture();
      try {
        await tester.runAsync(f.start);
        final second = File('${f.sdk.directory.path}/second.png');
        await tester.runAsync(() => second.writeAsBytes(f.png));
        f.picker = () async => [XFile(f.original.path), XFile(second.path)];
        await f.mount(tester);
        await f.savedTab(tester);
        await f.choose(tester);
        expect(find.text('还有 1 张图片尚未开始保存'), findsOneWidget);
        await f.tap(tester, find.text('保存到常用表情'));
        await f.font.until(
          tester,
          () => f.values.values.any(
            (v) => (jsonDecode(v)['items'] as List).length == 2,
          ),
        );
        await f.font.until(
          tester,
          () => find.byType(LinearProgressIndicator).evaluate().isEmpty,
        );
        expect(f.uploads.length, 2);
        expect(
          f.metadata.requests.map((r) => r.path.split('/').last).toList(),
          [
            'initiate_form_data',
            'complete_form_data',
            'initiate_form_data',
            'complete_form_data',
          ],
        );
        final rows = await f.actual(
          tester,
          () => f.store.read(f.access, current: () => true),
        );
        expect(rows.entries.map((e) => e.url.path).toList(), [
          '/uploaded2.png',
          '/uploaded.png',
        ]);
        expect(rows.pendingUpload, isNull);
        expect(f.faces, 0);
        expect(await tester.runAsync(second.exists), isTrue);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'saved emoji multi upload keeps confirmed first result and stops remaining images at unknown second ACK',
    (tester) async {
      final f = SavedEmojiFixture();
      var completed = 0;
      try {
        await tester.runAsync(f.start);
        final files = [
          f.original,
          File('${f.sdk.directory.path}/second.png'),
          File('${f.sdk.directory.path}/third.png'),
        ];
        for (final file in files.skip(1)) {
          await tester.runAsync(() => file.writeAsBytes(f.png));
        }
        f.picker = () async => files.map((file) => XFile(file.path)).toList();
        f.beforeComplete = () async {
          if (++completed == 2) {
            throw StateError('synthetic unknown second receipt');
          }
        };
        await f.mount(tester);
        await f.savedTab(tester);
        await f.choose(tester);
        await f.tap(tester, find.text('保存到常用表情'));
        await f.font.until(
          tester,
          () => find.text('上传结果未确认，请先核对。不会自动再次上传。').evaluate().length == 1,
        );
        final partial = await f.actual(
          tester,
          () => f.store.read(f.access, current: () => true),
        );
        expect(partial.entries.single.url.path, '/uploaded.png');
        expect(partial.pendingUpload, isNotNull);
        expect(f.uploads.length, 2);
        expect(f.faces, 0);
        await f.tap(tester, find.text('结束此上传记录'));
        expect(
          f.uploads.length,
          2,
        ); // Only the next explicit save can start the unattempted third image.
        await f.tap(tester, find.text('保存到常用表情'));
        await f.font.until(
          tester,
          () => f.values.values.any(
            (v) => (jsonDecode(v)['items'] as List).length == 2,
          ),
        );
        expect(f.uploads.length, 3);
        expect(f.faces, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
}
