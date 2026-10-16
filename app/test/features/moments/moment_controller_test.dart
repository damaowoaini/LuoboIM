import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/moments/presentation/moment_list_controller.dart';
import 'package:imapp/features/moments/presentation/moment_interactions_page.dart';

import 'moment_harness.dart';

void main() {
  late MomentHarness fixture;
  setUp(() async {
    fixture = MomentHarness();
    await fixture.start();
  });
  tearDown(() => fixture.dispose());
  test('separate likes and comments on the same post remain separate interaction records', () async {
    final controller = MomentInteractionsController(fixture.repository);
    addTearDown(controller.dispose);
    fixture.overrideRequest = (request) async {
      if (!request.uri.path.endsWith('/logs')) return fixture.respond(request);
      return {
        'workMoments': [
          {
            ...MomentHarness.item('same'),
            'type': 1,
            'comments': [],
            'likeUsers': [
              {'userID': 'bob', 'nickname': '小满', 'likeTime': '1700000000000'},
            ],
          },
          {
            ...MomentHarness.item('same'),
            'type': 1,
            'comments': [],
            'likeUsers': [
              {
                'userID': 'carol',
                'nickname': '小林',
                'likeTime': '1700000000001',
              },
            ],
          },
          {...MomentHarness.item('same'), 'type': 3, 'likeUsers': []},
        ],
      };
    };
    await controller.refresh();
    expect(controller.items.length, 3);
    expect(controller.items.map((item) => item.key).toSet().length, 3);
    expect(controller.items[1].actor.id, 'carol');
    expect(
      controller.items[0].createdAt?.millisecondsSinceEpoch,
      1700000000000,
    );
  });
  test('latest refresh wins, pages deduplicate and a failed refresh retries page one', () async {
    final controller = MomentListController(fixture.repository);
    addTearDown(controller.dispose);
    final old = Completer<Object?>(), entered = Completer<void>();
    fixture.overrideRequest = (_) {
      entered.complete();
      return old.future;
    };
    final first = controller.refresh();
    await entered.future;
    fixture.overrideRequest = (_) async => {
      'workMoments': [MomentHarness.item('new')],
    };
    await controller.refresh();
    old.complete({
      'workMoments': [MomentHarness.item('stale')],
    });
    await first;
    expect(controller.items.single.id, 'new');
    fixture.overrideRequest = (request) async => throw DioException(
      requestOptions: request,
      type: DioExceptionType.connectionError,
    );
    await controller.refresh();
    expect(controller.items.single.id, 'new');
    expect(controller.failedPage, 1);
    fixture.overrideRequest = (request) async {
      expect(request.data['pagination']['pageNumber'], 1);
      return {
        'workMoments': [MomentHarness.item('recovered')],
      };
    };
    await controller.retry();
    expect(controller.items.single.id, 'recovered');
  });
  test('twenty-item pages continue once and no-progress pages stop with an explicit error', () async {
    final controller = MomentListController(fixture.repository);
    addTearDown(controller.dispose);
    fixture.items
      ..clear()
      ..addAll(List.generate(21, (index) => MomentHarness.item('item-$index')));
    await controller.refresh();
    expect(controller.items.length, 20);
    expect(controller.more, isTrue);
    await controller.next();
    expect(controller.items.length, 21);
    expect(controller.more, isFalse);
    final count = fixture.http.requests.length;
    await controller.next();
    expect(fixture.http.requests.length, count);
    fixture.overrideRequest = (_) async => {
      'workMoments': List.generate(
        20,
        (index) => MomentHarness.item('item-$index'),
      ),
    };
    await controller.refresh();
    await controller.next();
    expect(controller.failure, isNotNull);
    expect(controller.items.length, 20);
  });
  test('in-flight mutation is single-flight and cannot repopulate state after logout', () async {
    final controller = MomentListController(fixture.repository);
    addTearDown(controller.dispose);
    await controller.refresh();
    final gate = Completer<void>();
    var calls = 0;
    final pending = controller.update('one', () async {
      calls++;
      await gate.future;
      return controller.repository.detail('one');
    });
    await controller.update('one', () async {
      calls++;
      return controller.repository.detail('one');
    });
    expect(calls, 1);
    final assertion = expectLater(pending, throwsA(anything));
    await fixture.session.signOut();
    gate.complete();
    await assertion;
    expect(controller.items, isEmpty);
  });
  test('interaction count read preserves list, explicit clear sends both and cannot race an active page', () async {
    final controller = MomentInteractionsController(fixture.repository);
    addTearDown(controller.dispose);
    await controller.refresh();
    expect(controller.items.length, 1);
    expect(fixture.unread, 0);
    expect(fixture.http.requests.last.data['type'], 1);
    await controller.clear();
    expect(controller.items, isEmpty);
    expect(fixture.http.requests.last.data['type'], 3);
    await fixture.session.signOut();
    await controller.refresh();
    expect(controller.items, isEmpty);
  });
}
