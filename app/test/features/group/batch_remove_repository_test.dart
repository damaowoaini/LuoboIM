import 'dart:async';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/group/data/group_repository.dart';

import 'batch_remove_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late BatchFixture f;
  setUp(() async {
    f = BatchFixture();
    await f.start();
  });
  tearDown(() async {
    f.api.close();
    await f.sdk.dispose();
  });

  test(
    'frozen ordered IDs each get exactly one singleton kick and individual ACK',
    () async {
      final report = await f.repository.removeSelected('group-a', [
        'bob',
        'carol',
      ], isCurrent: () => true);
      expect(f.kickCalls.map((call) => call['userIDList']), [
        ['bob'],
        ['carol'],
      ]);
      expect(
        report.items.map((item) => item.state),
        everyElement(GroupRemovalState.acknowledged),
      );
    },
  );

  test('admin role rejects peer admin before dispatch but continues ordinary target', () async {
    f.roles['alice'] = 60;
    final report = await f.repository.removeSelected('group-a', [
      'dave',
      'bob',
    ], isCurrent: () => true);
    expect(report.items.map((item) => item.state), [
      GroupRemovalState.rejected,
      GroupRemovalState.acknowledged,
    ]);
    expect(f.kickCalls.single['userIDList'], ['bob']);
  });

  test(
    'muted owner can remove another admin without canSend admission',
    () async {
      f.status = 3;
      final report = await f.repository.removeSelected('group-a', [
        'dave',
      ], isCurrent: () => true);
      expect(report.items.single.state, GroupRemovalState.acknowledged);
    },
  );

  test('muted administrator can remove an ordinary target', () async {
    f.roles['alice'] = 60;
    f.status = 3;
    final report = await f.repository.removeSelected('group-a', [
      'bob',
    ], isCurrent: () => true);
    expect(report.items.single.state, GroupRemovalState.acknowledged);
    expect(f.kickCalls.single['userIDList'], ['bob']);
  });

  test(
    'fresh actor and target roles after awaited lookup override stale UI',
    () async {
      f.roles['alice'] = 60;
      f.beforeRoles = (ids) async {
        if (ids.length == 2 && ids.contains('bob')) f.roles['bob'] = 60;
      };
      final report = await f.repository.removeSelected('group-a', [
        'bob',
        'carol',
      ], isCurrent: () => true);
      expect(report.items.map((item) => item.state), [
        GroupRemovalState.rejected,
        GroupRemovalState.acknowledged,
      ]);
      expect(f.kickCalls.single['userIDList'], ['carol']);
    },
  );

  test('view lost during role read prevents native dispatch and remaining requests', () async {
    final entered = Completer<void>(), reply = Completer<void>();
    f.beforeRoles = (ids) {
      if (ids.length == 2) {
        entered.complete();
        return reply.future;
      }
      return Future.value();
    };
    var current = true;
    final pending = f.repository.removeSelected('group-a', [
      'bob',
      'carol',
    ], isCurrent: () => current);
    await entered.future;
    current = false;
    reply.complete();
    final report = await pending;
    expect(f.kickCalls, isEmpty);
    expect(
      report.items.map((item) => item.state),
      everyElement(GroupRemovalState.notAttempted),
    );
  });

  for (final code in ['1002', '10005']) {
    test(
      'post-dispatch native error $code is unknown, not rollback proof or batch retry',
      () async {
        f.kickReply = (_, _) async => throw PlatformException(code: code);
        final report = await f.repository.removeSelected('group-a', [
          'bob',
          'carol',
        ], isCurrent: () => true);
        expect(report.items.map((item) => item.state), [
          GroupRemovalState.unconfirmed,
          GroupRemovalState.notAttempted,
        ]);
        expect(f.kickCalls.length, 1);
      },
    );
  }

  test(
    'unrecognized enterprise per-user DTO is not fabricated success',
    () async {
      f.kickReply = (_, _) async => '[{"userID":"foreign","result":0}]';
      final report = await f.repository.removeSelected('group-a', [
        'bob',
        'carol',
      ], isCurrent: () => true);
      expect(report.items.first.state, GroupRemovalState.unconfirmed);
      expect(report.items.last.state, GroupRemovalState.notAttempted);
      expect(f.kickCalls.length, 1);
    },
  );

  test(
    'actor withdraws management before dispatch and no target gets kicked',
    () async {
      f.beforeRoles = (ids) async {
        if (ids.length == 2) f.roles['alice'] = 20;
      };
      final report = await f.repository.removeSelected('group-a', [
        'bob',
        'carol',
      ], isCurrent: () => true);
      expect(f.kickCalls, isEmpty);
      expect(
        report.items.map((item) => item.state),
        everyElement(GroupRemovalState.rejected),
      );
    },
  );

  test('duplicate selection rejects before any kick', () async {
    await expectLater(
      f.repository.removeSelected('group-a', [
        'bob',
        'bob',
      ], isCurrent: () => true),
      throwsA(isA<AppFailure>()),
    );
    expect(f.kickCalls, isEmpty);
  });

  test(
    'old999 selection bound is preserved without dispatching a stale view',
    () async {
      final report = await f.repository.removeSelected(
        'group-a',
        List.generate(999, (i) => 'id$i'),
        isCurrent: () => false,
      );
      expect(report.items.length, 999);
      expect(f.kickCalls, isEmpty);
      await expectLater(
        f.repository.removeSelected(
          'group-a',
          List.generate(1000, (i) => 'id$i'),
          isCurrent: () => true,
        ),
        throwsA(isA<AppFailure>()),
      );
    },
  );

  test('owner switch after first dispatch cannot process second or write progress to new view', () async {
    final entered = Completer<void>(), ack = Completer<Object?>();
    f.kickReply = (_, _) {
      entered.complete();
      return ack.future;
    };
    final progress = <GroupRemovalItem>[];
    final pending = f.repository.removeSelected(
      'group-a',
      ['bob', 'carol'],
      isCurrent: () => f.repository.access.isCurrent,
      onResult: progress.add,
    );
    await entered.future;
    final changing = f.switchOwner(settled: pending.then<void>((_) {}));
    await Future<void>.delayed(Duration.zero);
    ack.complete('[]');
    await changing;
    final report = await pending;
    expect(f.kickCalls.length, 1);
    expect(report.items.last.state, GroupRemovalState.notAttempted);
    expect(progress, isEmpty);
  });

  test('progress after first ACK arrives before the second ACK and never duplicates dispatch', () async {
    final second = Completer<Object?>(), entered = Completer<void>();
    final progress = <GroupRemovalItem>[];
    f.kickReply = (_, index) {
      if (index == 2) {
        entered.complete();
        return second.future;
      }
      return Future.value('[]');
    };
    final pending = f.repository.removeSelected(
      'group-a',
      ['bob', 'carol'],
      isCurrent: () => true,
      onResult: progress.add,
    );
    await entered.future;
    expect(progress.single.userId, 'bob');
    expect(progress.single.state, GroupRemovalState.acknowledged);
    second.complete('[]');
    await pending;
    expect(progress.length, 2);
    expect(f.kickCalls.length, 2);
  });

  test('original single-user removal API remains callable', () async {
    await f.repository.remove('group-a', ['bob']);
    expect(f.kickCalls.single['userIDList'], ['bob']);
  });
}
