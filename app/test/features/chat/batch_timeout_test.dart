import 'dart:async';

import 'package:flutter_test/flutter_test.dart';

import 'batch_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test('a late native ACK after an actual operation timeout cannot mutate the frozen unknown receipt or resume the remaining batch', () async {
    final h = BatchHarness(timeout: const Duration(milliseconds: 80));
    await h.start();
    final entered = Completer<void>(), release = Completer<void>();
    try {
      h.selectAll();
      h.intercept = (call) async {
        if (call.method == 'deleteMessageFromLocalAndSvr') {
          entered.complete();
          await release.future;
        }
        return h.respond(call);
      };
      final pending = h.selection.deleteConfirmed(h.selection.capture());
      await entered.future;
      final result = await pending;
      expect(result.unconfirmedId, 'm1');
      expect(result.acknowledged, isEmpty);
      expect(result.notAttempted, ['m2', 'm3']);
      release.complete();
      await Future<void>.delayed(const Duration(milliseconds: 10));
      expect(result.acknowledged, isEmpty);
      expect(h.count('deleteMessageFromLocalAndSvr'), 1);
      expect(h.count('deleteMessageFromLocalStorage'), 0);
    } finally {
      if (!release.isCompleted) release.complete();
      await h.dispose();
    }
  });
}
