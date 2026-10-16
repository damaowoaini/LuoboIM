import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';

import '../im/sdk_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness h;
  var revision = 0, allowed = true;
  setUp(() async {
    revision = 0;
    allowed = true;
    h = SdkHarness(
      interactiveRevision: () => revision,
      hasInteractiveAccess: () => allowed,
    );
    await h.start();
  });
  tearDown(() => h.dispose());
  test('a late native response is discarded across lock and unlock, while a fresh explicit operation works', () async {
    final entered = Completer<void>(), release = Completer<void>();
    h.overrideCall = (call) async {
      if (call.method == 'getSelfUserInfo') {
        entered.complete();
        await release.future;
      }
      return h.respond(call);
    };
    final old = h.gateway.runForSession(
      h.session.state.generation,
      (manager) => manager.userManager.getSelfUserInfo(),
    );
    final rejected = expectLater(old, throwsA(isA<AppFailure>()));
    await entered.future;
    allowed = false;
    revision++;
    allowed = true;
    revision++;
    release.complete();
    await rejected;
    h.overrideCall = null;
    final fresh = await h.gateway.runForSession(
      h.session.state.generation,
      (manager) => manager.userManager.getSelfUserInfo(),
    );
    expect(fresh.userID, 'alice');
  });
  test('a staged history operation must check its original access lease before a later write', () async {
    final entered = Completer<void>(), release = Completer<void>();
    final old = h.gateway.runForHistory(h.session.state.generation, (
      manager,
      access,
    ) async {
      entered.complete();
      await release.future;
      access.check();
      await manager.conversationManager.hideConversation(
        conversationID: 'synthetic-conversation',
      );
    });
    final rejected = expectLater(old, throwsA(isA<AppFailure>()));
    await entered.future;
    allowed = false;
    revision++;
    allowed = true;
    revision++;
    release.complete();
    await rejected;
    expect(h.calls.where((c) => c.method == 'hideConversation'), isEmpty);
  });
  test('locked user work is refused while the explicit background-safe signaling path remains usable', () async {
    allowed = false;
    revision++;
    await expectLater(
      h.gateway.runForSession(
        h.session.state.generation,
        (manager) => manager.userManager.getSelfUserInfo(),
      ),
      throwsA(isA<AppFailure>()),
    );
    final self = await h.gateway.runForSession(
      h.session.state.generation,
      (manager) => manager.userManager.getSelfUserInfo(),
      allowWhileLocked: true,
    );
    expect(self.userID, 'alice');
  });
  test('a clear waiting for an already dispatched operation never dispatches after its access lease locks', () async {
    final entered = Completer<void>(), release = Completer<void>();
    final read = h.gateway.runForSession(h.session.state.generation, (_) async {
      entered.complete();
      await release.future;
    });
    final readRejected = expectLater(read, throwsA(isA<AppFailure>()));
    await entered.future;
    final clear = h.gateway.clearAllMessages(h.session.state.generation);
    final clearRejected = expectLater(clear, throwsA(isA<AppFailure>()));
    allowed = false;
    revision++;
    release.complete();
    await readRejected;
    await clearRejected;
    expect(
      h.calls.where((c) => c.method == 'deleteAllMsgFromLocalAndSvr'),
      isEmpty,
    );
  });
}
