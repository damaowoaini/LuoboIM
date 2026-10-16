import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:image_picker/image_picker.dart';
import 'package:imapp/core/error/app_failure.dart';

import 'group_avatar_fixture.dart';

void main() {
  late AvatarFixture f;
  setUp(() => f = AvatarFixture());

  testWidgets(
    'confirmed gallery selection uses real signed upload then exact group metadata',
    (tester) async {
      await f.mount(tester);
      await f.approve(tester);
      await until(
        tester,
        () => f.writes.length == 1 && find.text('群头像已更新').evaluate().isNotEmpty,
      );
      expect(f.sources, [ImageSource.gallery]);
      expect(f.writes.single['groupID'], 'group-a');
      expect(
        f.writes.single['faceURL'],
        'https://objects.example.com/upload-1.png',
      );
      expect(f.writes.single['groupName'], isNull);
      expect(
        f.providerRequests.single.headers.keys.map((e) => e.toLowerCase()),
        isNot(
          anyOf(
            contains('token'),
            contains('authorization'),
            contains('cookie'),
          ),
        ),
      );
      final start = f.metadata.requests.firstWhere(
        (r) => r.path.endsWith('initiate_form_data'),
      );
      expect(start.data['group'], 'avatar');
      expect(start.data['name'], matches(r'^alice/avatar/[a-f0-9]{32}\.png$'));
      expect(start.data['contentType'], 'image/png');
      expect(await tester.runAsync(f.original.exists), isTrue);
      expect(tester.takeException(), isNull);
      await f.close(tester);
    },
  );

  testWidgets('source sheet cancellation never invokes OS picker or upload', (
    tester,
  ) async {
    await f.mount(tester);
    await tester.tap(f.edit);
    await routeFrame(tester);
    f.navigator.currentState!.pop();
    await routeFrame(tester);
    expect(f.sources, isEmpty);
    expect(f.metadata.requests, isEmpty);
    expect(f.writes, isEmpty);
    expect(tester.widget<OutlinedButton>(f.edit).onPressed, isNotNull);
    await f.close(tester);
  });

  testWidgets('cancelled OS picker preserves metadata and releases pending', (
    tester,
  ) async {
    await f.mount(tester);
    f.picker = (_) async => null;
    await f.select(tester, camera: true);
    await routeFrame(tester);
    expect(f.sources, [ImageSource.camera]);
    expect(f.metadata.requests, isEmpty);
    expect(f.writes, isEmpty);
    expect(tester.widget<OutlinedButton>(f.edit).onPressed, isNotNull);
    await f.close(tester);
  });

  testWidgets(
    'preview cancellation cleans staging but preserves picker original',
    (tester) async {
      await f.mount(tester);
      await f.preview(tester);
      await tester.tap(find.widgetWithText(TextButton, '取消'));
      await until(
        tester,
        () =>
            find.byType(AlertDialog).evaluate().isEmpty &&
            tester.widget<OutlinedButton>(f.edit).onPressed != null,
      );
      expect(f.metadata.requests, isEmpty);
      expect(f.writes, isEmpty);
      expect(await tester.runAsync(f.original.exists), isTrue);
      final files = await tester.runAsync(() async {
        final dir = await f.sdk.gateway.mediaDirectory(
          f.sdk.session.state.generation,
        );
        return dir.list().toList();
      });
      expect(files, isEmpty);
      await f.close(tester);
    },
  );

  testWidgets(
    'invalid image fails before signed upload and can be explicitly reselected',
    (tester) async {
      await f.mount(tester);
      await tester.runAsync(
        () async => f.original.writeAsString('invalid-image'),
      );
      await f.select(tester);
      await until(
        tester,
        () => find.textContaining('请刷新核对后重新选择').evaluate().isNotEmpty,
      );
      expect(f.metadata.requests, isEmpty);
      expect(f.writes, isEmpty);
      expect(tester.widget<OutlinedButton>(f.edit).onPressed, isNotNull);
      await f.close(tester);
    },
  );

  testWidgets(
    'provider failure keeps safe retry feedback without automatic resubmission',
    (tester) async {
      await f.mount(tester);
      f.providerReply = (_) async =>
          throw const AppFailure(FailureKind.network);
      await f.approve(tester);
      await until(
        tester,
        () => find.textContaining('请刷新核对后重新选择').evaluate().isNotEmpty,
      );
      expect(f.providerRequests.length, 1);
      expect(f.writes, isEmpty);
      await routeFrame(tester);
      expect(f.providerRequests.length, 1);
      f.providerReply = null;
      await f.approve(tester);
      await until(tester, () => f.writes.length == 1);
      expect(f.providerRequests.length, 2);
      await f.close(tester);
    },
  );

  for (final scope in ['owner', 'tenant', 'issuer']) {
    testWidgets('pending picker cannot continue after $scope replacement', (
      tester,
    ) async {
      await f.mount(tester);
      final reply = Completer<XFile?>();
      f.picker = (_) => reply.future;
      await f.select(tester);
      expect(f.sources.length, 1);
      await tester.runAsync(
        () => f.signInOther(
          user: scope == 'owner' ? 'bob' : 'alice',
          tenant: scope == 'tenant' ? 'tenant-b' : 'fixture-tenant',
          issuer: scope == 'issuer'
              ? 'https://route-b.example.com/'
              : 'https://im.example.com/',
        ),
      );
      reply.complete(XFile(f.original.path));
      await routeFrame(tester);
      expect(f.metadata.requests, isEmpty);
      expect(f.writes, isEmpty);
      expect(find.byType(AlertDialog), findsNothing);
      await f.close(tester);
    });
  }

  testWidgets(
    'leaving the page while picker waits prevents staging and upload',
    (tester) async {
      await f.mount(tester);
      final reply = Completer<XFile?>();
      f.picker = (_) => reply.future;
      await f.select(tester);
      f.body.value = '';
      await tester.pump();
      reply.complete(XFile(f.original.path));
      await routeFrame(tester);
      expect(f.metadata.requests, isEmpty);
      expect(f.writes, isEmpty);
      expect(tester.takeException(), isNull);
      await f.close(tester);
    },
  );

  testWidgets('same mounted page switching group rejects old picker result', (
    tester,
  ) async {
    await f.mount(tester);
    final reply = Completer<XFile?>();
    f.picker = (_) => reply.future;
    await f.select(tester);
    f.body.value = 'group-b';
    await routeFrame(tester);
    reply.complete(XFile(f.original.path));
    await routeFrame(tester);
    expect(find.text('群 ID：group-b'), findsOneWidget);
    expect(f.metadata.requests, isEmpty);
    expect(f.writes, isEmpty);
    await f.close(tester);
  });

  testWidgets('fresh role withdrawal after preview starts no upload', (
    tester,
  ) async {
    await f.mount(tester);
    await f.preview(tester);
    f.role = 20;
    await tester.tap(find.widgetWithText(FilledButton, '使用'));
    await until(
      tester,
      () => find.textContaining('权限不足').evaluate().isNotEmpty,
    );
    expect(f.providerRequests, isEmpty);
    expect(f.writes, isEmpty);
    await f.close(tester);
  });

  testWidgets(
    'role withdrawn during an already started upload prevents metadata',
    (tester) async {
      await f.mount(tester);
      final reply = Completer<void>();
      f.providerReply = (_) => reply.future;
      await f.approve(tester);
      await until(tester, () => f.providerRequests.length == 1);
      f.role = 20;
      reply.complete();
      await until(
        tester,
        () => find.textContaining('权限不足').evaluate().isNotEmpty,
      );
      expect(f.providerRequests.length, 1);
      expect(f.writes, isEmpty);
      await f.close(tester);
    },
  );

  testWidgets(
    'navigation away and back cannot revive an in-flight avatar intent',
    (tester) async {
      await f.mount(tester);
      final reply = Completer<void>();
      f.providerReply = (_) => reply.future;
      await f.approve(tester);
      await until(tester, () => f.providerRequests.length == 1);
      unawaited(
        f.navigator.currentState!.push<void>(
          MaterialPageRoute(
            builder: (_) => const Scaffold(body: Text('另一个页面')),
          ),
        ),
      );
      await routeFrame(tester);
      f.navigator.currentState!.pop();
      await routeFrame(tester);
      reply.complete();
      await routeFrame(tester);
      expect(f.writes, isEmpty);
      expect(find.text('群头像已更新'), findsNothing);
      await f.close(tester);
    },
  );

  for (final oldFails in [false, true]) {
    testWidgets(
      'latest accepted upload wins before metadata; old fails=$oldFails',
      (tester) async {
        await f.mount(tester);
        final first = Completer<void>();
        f.providerReply = (n) => n == 1 ? first.future : Future<void>.value();
        await f.approve(tester);
        await until(tester, () => f.providerRequests.length == 1);
        await f.approve(tester);
        await until(
          tester,
          () =>
              f.writes.length == 1 && find.text('群头像已更新').evaluate().isNotEmpty,
        );
        expect(
          f.writes.single['faceURL'],
          'https://objects.example.com/upload-2.png',
        );
        if (oldFails) {
          first.completeError(const AppFailure(FailureKind.network));
        } else {
          first.complete();
        }
        await routeFrame(tester);
        expect(f.writes.length, 1);
        expect(find.textContaining('请刷新核对后重新选择'), findsNothing);
        await f.close(tester);
      },
    );
  }

  testWidgets(
    'metadata pending admits no new picker then permits explicit selection after ACK',
    (tester) async {
      await f.mount(tester);
      final ack = Completer<void>();
      f.metadataReply = () => ack.future;
      await f.approve(tester);
      await until(tester, () => f.writes.length == 1);
      expect(tester.widget<OutlinedButton>(f.edit).onPressed, isNull);
      expect(f.sources.length, 1);
      expect(f.providerRequests.length, 1);
      ack.complete();
      await until(
        tester,
        () => tester.widget<OutlinedButton>(f.edit).onPressed != null,
      );
      f.metadataReply = null;
      await f.approve(tester);
      await until(tester, () => f.writes.length == 2);
      expect(f.sources.length, 2);
      await f.close(tester);
    },
  );

  testWidgets(
    'metadata error releases admission and never auto-replays unknown result',
    (tester) async {
      await f.mount(tester);
      f.metadataReply = () async => throw const AppFailure(FailureKind.timeout);
      await f.approve(tester);
      await until(
        tester,
        () => find.textContaining('请刷新核对后重新选择').evaluate().isNotEmpty,
      );
      expect(f.writes.length, 1);
      // Error feedback precedes asynchronous private-stage cleanup. Observe
      // actual admission release instead of treating the error text as ACK.
      await until(
        tester,
        () => tester.widget<OutlinedButton>(f.edit).onPressed != null,
      );
      expect(tester.widget<OutlinedButton>(f.edit).onPressed, isNotNull);
      await routeFrame(tester);
      expect(f.writes.length, 1);
      await f.close(tester);
    },
  );
  for (final stage in ['source', 'preview']) {
    testWidgets(
      'foreign route covering own $stage modal invalidates old confirmation after ABA',
      (tester) async {
        await f.mount(tester);
        if (stage == 'source') {
          await tester.tap(f.edit);
          await routeFrame(tester);
        } else {
          await f.preview(tester);
        }
        final approve = stage == 'source'
          ? find.text('从相册选择')
          : find.widgetWithText(FilledButton, '使用');
        final owned = ModalRoute.of(tester.element(approve))!;
        final foreign = MaterialPageRoute<void>(
          builder: (_) => const Scaffold(body: Text('外来页面')),
        );
        unawaited(f.navigator.currentState!.push<void>(foreign));
        await until(tester, () => foreign.isCurrent &&
          foreign.animation?.status == AnimationStatus.completed);
        var foreignRemoved = false;
        unawaited(foreign.completed.then<void>((_) { foreignRemoved = true; }));
        f.navigator.currentState!.pop();
        await until(tester, () => foreignRemoved && owned.isCurrent &&
          approve.hitTestable().evaluate().length == 1);
        var ownedRemoved = false;
        unawaited(owned.completed.then<void>((_) { ownedRemoved = true; }));
        await tester.tap(approve.hitTestable());
        // The source/preview must actually consume this click and leave;
        // only then are the unchanged no-picker/no-upload assertions meaningful.
        await until(tester, () => ownedRemoved);
        await routeFrame(tester);
        expect(f.providerRequests, isEmpty);
        expect(f.writes, isEmpty);
        if (stage == 'source') expect(f.sources, isEmpty);
        expect(tester.takeException(), isNull);
        await f.close(tester);
      },
    );
  }
}
