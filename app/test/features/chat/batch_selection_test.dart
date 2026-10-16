import 'dart:async';
import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';

import '../../core/im/sdk_harness.dart';
import 'batch_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late BatchHarness h;
  setUp(() async {
    h = BatchHarness();
    await h.start();
  });
  tearDown(() => h.dispose());
  test('selection is bounded20, ID-only and cancel invalidates its original confirmation', () async {
    for (var i = 4; i <= 21; i++) {
      h.sdk.messages.add(SdkHarness.message(id: 'm$i', text: 'body$i'));
    }
    await h.chat.refresh();
    h.selection.begin(h.chat.messages.first);
    for (final item in h.chat.messages.skip(1).take(19)) {
      expect(h.selection.toggle(item), true);
    }
    expect(h.selection.count, 20);
    expect(h.selection.toggle(h.chat.messages.last), false);
    final intent = h.selection.capture();
    expect(intent.messageIds, hasLength(20));
    expect(intent.toString(), 'ChatBatchIntent');
    h.selection.cancel();
    expect(h.selection.selectedIds, isEmpty);
    expect(intent.current, false);
    expect(h.count('deleteMessageFromLocalAndSvr'), 0);
  });
  for (final revoked in [false, true]) {
    test(
      '${revoked ? 'revoke' : 'delete'} withdraws selected IDs and old confirmation permanently',
      () async {
        h.selectAll();
        final intent = h.selection.capture();
        final raw = h.sdk.messages.first;
        h.sdk.messages.removeAt(0);
        await h.sdk.callback(ListenerType.advancedMsgListener, {
          'type': revoked ? 'onNewRecvMessageRevoked' : 'onMsgDeleted',
          'data': revoked
              ? {
                  'messageRevoked': jsonEncode({
                    'clientMsgID': raw['clientMsgID'],
                  }),
                }
              : {'message': jsonEncode(raw)},
        });
        await Future<void>.delayed(Duration.zero);
        expect(h.selection.selectedIds, isNot(contains(raw['clientMsgID'])));
        expect(intent.current, false);
      },
    );
  }
  test('new private facts remove selection immediately instead of retaining a static body snapshot', () async {
    h.selectAll();
    final intent = h.selection.capture();
    h.sdk.gateway.privacyLedger.observe('single-alice-bob', {
      'clientMsgID': 'm2',
      'attachedInfoElem': {'isPrivateChat': true},
    }, now: DateTime.now());
    await Future<void>.delayed(Duration.zero);
    expect(h.selection.selectedIds, isNot(contains('m2')));
    expect(intent.current, false);
    expect(h.selection.count, 2);
  });
  test('per-CID clear invalidates old selection and cannot be renewed by a later reload', () async {
    h.selectAll();
    final intent = h.selection.capture();
    await h.sdk.gateway.clearConversationMessages(
      h.sdk.session.state.generation,
      'single-alice-bob',
    );
    await Future<void>.delayed(Duration.zero);
    expect(h.selection.count, 0);
    expect(h.selection.active, false);
    await expectLater(
      h.selection.deleteConfirmed(intent),
      throwsA(isA<AppFailure>()),
    );
    expect(h.count('deleteMessageFromLocalAndSvr'), 0);
  });
  test('sequential batch stops after one ACK then one unknown and never uses local-only fallback or submits the third', () async {
    h.selectAll();
    final intent = h.selection.capture();
    h.intercept = (call) async {
      if (call.method == 'deleteMessageFromLocalAndSvr' &&
          (call.arguments as Map)['clientMsgID'] == 'm2') {
        throw PlatformException(code: '10005');
      }
      return h.respond(call);
    };
    final result = await h.selection.deleteConfirmed(intent);
    expect(result.acknowledged, ['m1']);
    expect(result.unconfirmedId, 'm2');
    expect(result.notAttempted, ['m3']);
    expect(result.complete, false);
    expect(result.notice, contains('结果未确认'));
    expect(h.count('deleteMessageFromLocalAndSvr'), 2);
    expect(h.count('deleteMessageFromLocalStorage'), 0);
    expect(h.chat.messages.map((m) => m.id), isNot(contains('m1')));
  });
  test('cancelling a busy batch invalidates the original lease and prevents all requests after the in-flight ACK', () async {
    h.selectAll();
    final intent = h.selection.capture();
    final entered = Completer<void>(), release = Completer<void>();
    h.intercept = (call) async {
      if (call.method == 'deleteMessageFromLocalAndSvr') {
        entered.complete();
        await release.future;
      }
      return h.respond(call);
    };
    final pending = h.selection.deleteConfirmed(intent);
    await entered.future;
    h.selection.cancel();
    expect(h.selection.busy, true);
    expect(h.selection.selectedIds, isEmpty);
    release.complete();
    final result = await pending;
    expect(result.acknowledged, ['m1']);
    expect(result.notAttempted, ['m2', 'm3']);
    expect(h.count('deleteMessageFromLocalAndSvr'), 1);
    expect(h.selection.busy, false);
    expect(h.selection.active, false);
  });
  test('all acknowledged requests remove only their own timeline IDs and report requests rather than physical erasure', () async {
    h.selectAll();
    final ownAcksWereAlreadyUnselected = <bool>[];
    final changes = h.sdk.gateway.privacyLedger.changes.listen((_) {
      for (final id in ['m1', 'm2', 'm3']) {
        if (h.source.isRemoved(id)) {
          ownAcksWereAlreadyUnselected.add(!h.selection.selected(id));
        }
      }
    });
    final result = await h.selection.deleteConfirmed(h.selection.capture());
    await Future<void>.delayed(Duration.zero);
    await changes.cancel();
    expect(ownAcksWereAlreadyUnselected, isNotEmpty);
    expect(ownAcksWereAlreadyUnselected, everyElement(true));
    expect(result.complete, true);
    expect(result.acknowledged, hasLength(3));
    expect(result.notice, contains('删除请求'));
    expect(result.notice, contains('刷新核对'));
    expect(h.selection.active, false);
    expect(h.chat.messages, isEmpty);
  });
  test('a private second source learned during first native ACK stops all remaining deletion dispatch', () async {
    h.selectAll();
    final intent = h.selection.capture();
    h.intercept = (call) async {
      final value = h.respond(call);
      if (call.method == 'deleteMessageFromLocalAndSvr') {
        h.sdk.gateway.privacyLedger.observe('single-alice-bob', {
          'clientMsgID': 'm2',
          'attachedInfoElem': {'isPrivateChat': true},
        }, now: DateTime.now());
      }
      return value;
    };
    final result = await h.selection.deleteConfirmed(intent);
    expect(result.acknowledged, ['m1']);
    expect(result.stopped, true);
    expect(result.notAttempted, ['m2', 'm3']);
    expect(h.count('deleteMessageFromLocalAndSvr'), 1);
  });
  test('clear while source lookup waits prevents the first native delete and unknown IDs cannot be submitted afterward', () async {
    h.selectAll();
    final entered = Completer<void>(), release = Completer<void>();
    h.intercept = (call) async {
      if (call.method == 'findMessageList') {
        entered.complete();
        await release.future;
      }
      return h.respond(call);
    };
    final pending = h.selection.deleteConfirmed(h.selection.capture());
    await entered.future;
    final clearing = h.sdk.gateway.clearConversationMessages(
      h.sdk.session.state.generation,
      'single-alice-bob',
    );
    release.complete();
    final result = await pending;
    await clearing;
    expect(result.acknowledged, isEmpty);
    expect(result.notAttempted, hasLength(3));
    expect(h.count('deleteMessageFromLocalAndSvr'), 0);
  });
  test('old confirmation cannot dispatch against a new owner even when the visible conversation ID is reused', () async {
    h.selectAll();
    final old = h.selection.capture();
    await h.sdk.session.signOut();
    h.sdk.userId = 'new-owner';
    await h.sdk.session.signIn((_) async => h.sdk.credentials);
    await expectLater(
      h.selection.deleteConfirmed(old),
      throwsA(isA<AppFailure>()),
    );
    expect(h.count('deleteMessageFromLocalAndSvr'), 0);
  });
}
