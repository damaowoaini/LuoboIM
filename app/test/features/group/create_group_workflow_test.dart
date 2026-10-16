import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:image_picker/image_picker.dart';
import 'package:imapp/core/error/app_failure.dart';

import 'create_group_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late CreationFixture f;
  setUp(() => f = CreationFixture());

  testWidgets('optional avatar keeps ordinary explicit creation and opens the accepted group', (tester) async {
    try {
      await f.mount(tester); await f.tap(tester, '创建群聊');
      await f.until(tester, () => find.text('chat:created-cid').evaluate().isNotEmpty);
      expect(f.creates, hasLength(1)); expect(f.providerBodies, isEmpty);
      expect((f.creates.single['groupInfo'] as Map)['groupType'], 2);
      expect(f.creates.single['memberUserIDs'], ['bob', 'carol']);
    } finally { await f.close(tester); }
  });

  testWidgets('real PNG selection and signed upload reaches exact create faceURL', (tester) async {
    try {
      await f.mount(tester); await f.upload(tester);
      await f.avatarReady(tester);
      await f.tap(tester, '创建群聊');
      await f.until(tester, () => f.creates.length == 1);
      expect((f.creates.single['groupInfo'] as Map)['faceURL'], 'https://objects.example.com/upload-1.png');
      expect(f.providerBodies.single, containsAllInOrder([137, 80, 78, 71]));
      expect(f.providerRequests.single.headers.keys.map((k) => k.toLowerCase()),
        isNot(anyOf(contains('token'), contains('authorization'), contains('cookie'))));
      final start = f.base.metadata.requests.firstWhere((r) => r.path.endsWith('initiate_form_data'));
      expect(start.data['group'], 'avatar'); expect(start.data['contentType'], 'image/png');
      expect(await tester.runAsync(f.base.original.exists), isTrue);
    } finally { await f.close(tester); }
  });

  testWidgets('cancelling source sheet invokes neither picker nor storage and preserves draft', (tester) async {
    try {
      await f.mount(tester);
      await tester.tap(find.byKey(const ValueKey('create-group-avatar'))); await f.frame(tester);
      Navigator.of(tester.element(find.text('从相册选择'))).pop(); await f.frame(tester);
      expect(f.base.sources, isEmpty); expect(f.providerBodies, isEmpty); expect(f.creates, isEmpty);
      expect(tester.widget<OutlinedButton>(find.byKey(const ValueKey('create-group-avatar'))).onPressed, isNotNull);
      expect(tester.widget<TextField>(find.byType(TextField)).controller!.text, '同名好友、同名好友');
    } finally { await f.close(tester); }
  });

  testWidgets('cancelled OS picker and cancelled preview permit explicit retry without upload', (tester) async {
    try {
      await f.mount(tester); f.base.picker = (_) async => null;
      await tester.tap(find.byKey(const ValueKey('create-group-avatar'))); await f.frame(tester);
      await tester.tap(find.text('拍照')); await f.frame(tester);
      expect(f.base.sources, [ImageSource.camera]); expect(f.providerBodies, isEmpty);
      f.base.picker = null; await f.preview(tester);
      await tester.tap(find.widgetWithText(TextButton, '取消')); await f.frame(tester);
      await f.until(tester, () => tester.widget<OutlinedButton>(find.byKey(const ValueKey('create-group-avatar'))).onPressed != null);
      expect(f.providerBodies, isEmpty); expect(f.creates, isEmpty);
      expect(await tester.runAsync(f.base.original.exists), isTrue);
    } finally { await f.close(tester); }
  });

  testWidgets('invalid image fails in real decoder before signed storage and leaves creation draft', (tester) async {
    try {
      await f.mount(tester); await tester.runAsync(() => f.base.original.writeAsString('invalid-image'));
      await tester.tap(find.byKey(const ValueKey('create-group-avatar'))); await f.frame(tester);
      await tester.tap(find.text('从相册选择')); await tester.pump();
      await f.until(tester, () => tester.widget<OutlinedButton>(find.byKey(const ValueKey('create-group-avatar'))).onPressed != null);
      expect(f.providerBodies, isEmpty); expect(f.creates, isEmpty);
      expect(f.base.metadata.requests, isEmpty); expect(find.byType(TextField), findsOneWidget);
    } finally { await f.close(tester); }
  });

  testWidgets('storage failure releases picker and permits a new explicit upload without automatic retry', (tester) async {
    try {
      await f.mount(tester); f.uploadReply = (_) async => throw const AppFailure(FailureKind.network);
      await f.upload(tester);
      await f.until(tester, () => tester.widget<OutlinedButton>(find.byKey(const ValueKey('create-group-avatar'))).onPressed != null);
      expect(f.providerBodies, hasLength(1)); expect(f.creates, isEmpty);
      await f.frame(tester); expect(f.providerBodies, hasLength(1));
      f.uploadReply = null; await f.upload(tester);
      await f.avatarReady(tester);
      expect(f.providerBodies, hasLength(2));
    } finally { await f.close(tester); }
  });

  for (final scope in ['owner', 'issuer', 'tenant']) {
    testWidgets('pending picker cannot continue in a replacement $scope', (tester) async {
      final result = Completer<XFile?>();
      try {
        await f.mount(tester); f.base.picker = (_) => result.future;
        await tester.tap(find.byKey(const ValueKey('create-group-avatar'))); await f.frame(tester);
        await tester.tap(find.text('从相册选择')); await f.frame(tester);
        expect(f.base.sources, hasLength(1));
        await tester.runAsync(() => f.base.signInOther(user: scope == 'owner' ? 'bob' : 'alice',
          issuer: scope == 'issuer' ? 'https://other.example.com/' : 'https://im.example.com/',
          tenant: scope == 'tenant' ? 'tenant-b' : 'fixture-tenant'));
        result.complete(XFile(f.base.original.path)); await f.frame(tester);
        expect(f.providerBodies, isEmpty); expect(f.base.metadata.requests, isEmpty); expect(f.creates, isEmpty);
        expect(find.text('使用这张群头像？'), findsNothing);
      } finally { if (!result.isCompleted) result.complete(null); await f.close(tester); }
    });
  }

  testWidgets('foreign navigation ABA cannot revive a pending picker but new selection works', (tester) async {
    final result = Completer<XFile?>();
    try {
      await f.mount(tester); f.base.picker = (_) => result.future;
      await tester.tap(find.byKey(const ValueKey('create-group-avatar'))); await f.frame(tester);
      await tester.tap(find.text('从相册选择')); await f.frame(tester);
      await f.coverAndReturn(tester); result.complete(XFile(f.base.original.path)); await f.frame(tester);
      expect(f.providerBodies, isEmpty); expect(f.creates, isEmpty);
      f.base.picker = null; await f.upload(tester);
      await f.avatarReady(tester);
      expect(f.providerBodies, hasLength(1));
    } finally { if (!result.isCompleted) result.complete(null); await f.close(tester); }
  });

  testWidgets('foreign route over owned preview permanently rejects its old approval', (tester) async {
    try {
      await f.mount(tester); await f.preview(tester); await f.coverAndReturn(tester);
      expect(find.text('本次选择已失效'), findsOneWidget); expect(find.text('使用'), findsNothing);
      await tester.tap(find.text('关闭')); await f.frame(tester);
      expect(f.providerBodies, isEmpty); expect(f.creates, isEmpty);
      await f.upload(tester); expect(f.providerBodies, hasLength(1));
    } finally { await f.close(tester); }
  });

  for (final oldFails in [false, true]) {
    testWidgets('latest accepted avatar selection wins before create; late old error=$oldFails', (tester) async {
      final first = Completer<void>();
      try {
        await f.mount(tester); f.uploadReply = (n) => n == 1 ? first.future : Future<void>.value();
        await f.upload(tester); await f.upload(tester);
        await f.avatarReady(tester);
        if (oldFails) { first.completeError(const AppFailure(FailureKind.network)); } else { first.complete(); }
        await f.frame(tester); await f.tap(tester, '创建群聊');
        await f.until(tester, () => f.creates.length == 1);
        expect((f.creates.single['groupInfo'] as Map)['faceURL'], 'https://objects.example.com/upload-2.png');
        expect(f.providerBodies, hasLength(2)); expect(find.textContaining('网络连接失败'), findsNothing);
      } finally { if (!first.isCompleted) first.complete(); await f.close(tester); }
    });
  }

  testWidgets('pending create admits no second submit or picker and retains one native intent', (tester) async {
    final done = Completer<void>();
    try {
      await f.mount(tester); f.createReply = () => done.future;
      await f.tap(tester, '创建群聊'); await f.until(tester, () => f.creates.length == 1);
      expect(tester.widget<OutlinedButton>(find.byKey(const ValueKey('create-group-avatar'))).onPressed, isNull);
      expect(tester.widget<TextField>(find.byType(TextField)).enabled, isFalse);
      expect(find.widgetWithText(FilledButton, '创建群聊'), findsNothing);
      await f.frame(tester); expect(f.creates, hasLength(1)); expect(f.base.sources, isEmpty);
      done.complete(); await f.until(tester, () => find.text('chat:created-cid').evaluate().isNotEmpty);
      expect(f.creates, hasLength(1));
    } finally { if (!done.isCompleted) done.complete(); await f.close(tester); }
  });

  testWidgets('unknown create never replays and offers only an explicit group-list check', (tester) async {
    try {
      await f.mount(tester); f.createReply = () async => throw const AppFailure(FailureKind.timeout);
      await f.tap(tester, '创建群聊');
      await f.until(tester, () => find.textContaining('群创建结果未确认').evaluate().isNotEmpty);
      expect(f.creates, hasLength(1)); expect(find.widgetWithText(FilledButton, '创建群聊'), findsNothing);
      expect(tester.widget<TextField>(find.byType(TextField)).enabled, isFalse);
      final context = tester.element(find.byType(TextField));
      await Navigator.of(context).maybePop(); await f.frame(tester); expect(f.creates, hasLength(1));
      await f.tap(tester, '返回群聊列表');
      await f.until(tester, () => find.text('authoritative-group-list').evaluate().isNotEmpty);
      expect(f.creates, hasLength(1)); expect(f.providerBodies, isEmpty);
    } finally { await f.close(tester); }
  });

  testWidgets('accepted create with conversation failure retries only its exact owned group read', (tester) async {
    try {
      await f.mount(tester); f.conversationReply = () async => throw const AppFailure(FailureKind.network);
      await f.tap(tester, '创建群聊');
      await f.until(tester, () => find.textContaining('群已创建，暂时无法打开会话').evaluate().isNotEmpty);
      expect(f.creates, hasLength(1));
      f.conversationReply = null; await f.tap(tester, '重试打开会话');
      await f.until(tester, () => find.text('chat:created-cid').evaluate().isNotEmpty);
      expect(f.creates, hasLength(1));
      final reads = f.base.sdk.calls.where((c) => c.method == 'getOneConversation').toList();
      expect(reads, hasLength(2));
      expect(reads.map((c) => (c.arguments as Map)['sourceID']), everyElement('created-group'));
    } finally { await f.close(tester); }
  });

  testWidgets('late create ACK after navigation ABA cannot start conversation or revive submit', (tester) async {
    final done = Completer<void>();
    try {
      await f.mount(tester); f.createReply = () => done.future;
      await f.tap(tester, '创建群聊'); await f.until(tester, () => f.creates.length == 1);
      await f.coverAndReturn(tester); done.complete();
      await f.until(tester, () => find.textContaining('群创建结果未确认').evaluate().isNotEmpty);
      expect(f.creates, hasLength(1));
      expect(f.base.sdk.calls.where((c) => c.method == 'getOneConversation'), isEmpty);
      expect(find.text('chat:created-cid'), findsNothing);
    } finally { if (!done.isCompleted) done.complete(); await f.close(tester); }
  });

  testWidgets('late conversation after navigation ABA cannot navigate but explicit retry is read only', (tester) async {
    final done = Completer<String>();
    try {
      await f.mount(tester); f.conversationReply = () => done.future;
      await f.tap(tester, '创建群聊');
      await f.until(tester, () => f.base.sdk.calls.any((c) => c.method == 'getOneConversation'));
      await f.coverAndReturn(tester);
      done.complete('{"conversationID":"created-cid","conversationType":3,"groupID":"created-group"}');
      await f.until(tester, () => find.widgetWithText(FilledButton, '重试打开会话').evaluate().isNotEmpty &&
        tester.widget<FilledButton>(find.widgetWithText(FilledButton, '重试打开会话')).onPressed != null);
      expect(find.text('chat:created-cid'), findsNothing); expect(f.creates, hasLength(1));
      f.conversationReply = null; await f.tap(tester, '重试打开会话');
      await f.until(tester, () => find.text('chat:created-cid').evaluate().isNotEmpty);
      expect(f.creates, hasLength(1));
    } finally {
      if (!done.isCompleted) done.complete('{"conversationID":"created-cid","conversationType":3,"groupID":"created-group"}');
      await f.close(tester);
    }
  });
  testWidgets('session invalidation after create dispatch hides its draft and permits safe exit', (tester) async {
    final done = Completer<void>();
    Future<void>? rotation;
    try {
      await f.mount(tester);
      // Give the actual creation route a predecessor without replacing its
      // implementation, so PopScope's safe-exit behavior is observable.
      f.router!.go('/'); await f.frame(tester);
      await tester.tap(find.text('发起聊天')); await f.frame(tester);
      await f.until(tester, () => find.byType(CheckboxListTile).evaluate().length == 2);
      await tester.tap(find.byType(CheckboxListTile).at(0)); await tester.pump();
      await tester.tap(find.byType(CheckboxListTile).at(1)); await tester.pump();
      await tester.tap(find.widgetWithText(TextButton, '确定 (2)')); await f.frame(tester);
      f.createReply = () => done.future;
      await f.tap(tester, '创建群聊'); await f.until(tester, () => f.creates.length == 1);
      rotation = f.base.sdk.session.signOut(revoke: false); await f.frame(tester);
      expect(find.text('会话已变化，请返回后重新选择成员'), findsOneWidget);
      expect(find.text('ID：bob'), findsNothing); expect(find.byType(TextField), findsNothing);
      final context = tester.element(find.text('会话已变化，请返回后重新选择成员'));
      expect(await Navigator.of(context).maybePop(), isTrue); await f.frame(tester);
      expect(find.text('会话已变化，请返回后重新选择成员'), findsNothing);
      done.complete(); await tester.runAsync(() => rotation!); await f.frame(tester);
      expect(f.creates, hasLength(1));
      expect(f.base.sdk.calls.where((c) => c.method == 'getOneConversation'), isEmpty);
    } finally {
      if (!done.isCompleted) done.complete();
      if (rotation != null) await tester.runAsync(() => rotation!);
      await f.close(tester);
    }
  });
  testWidgets('actual editor permits 128 Unicode graphemes accepted by real create repository', (tester) async {
    try {
      await f.mount(tester);
      final title = List.filled(128, '🧑🏽‍💻').join();
      await tester.enterText(find.byType(TextField), title); await tester.pump();
      expect(tester.widget<TextField>(find.byType(TextField)).controller!.text, title);
      await f.tap(tester, '创建群聊'); await f.until(tester, () => f.creates.length == 1);
      expect((f.creates.single['groupInfo'] as Map)['groupName'], title);
      expect(f.providerBodies, isEmpty);
    } finally { await f.close(tester); }
  });
}
