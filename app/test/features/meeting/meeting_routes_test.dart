import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/app.dart';
import 'package:imapp/core/app_runtime.dart';
import 'package:imapp/core/config/app_config.dart';
import 'package:imapp/core/config/route_config_loader.dart';
import 'package:imapp/core/im/open_im_gateway.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/providers.dart';

import '../../core/privacy_bootstrap_test.dart'
    show ConsentMemory, ImProbe, StoreProbe;
import 'meeting_harness.dart';

const _unavailableCopy = '会议通话暂不可用，当前可预约和管理会议。本版本尚未开放加入会议。';

AppRuntime _runtime(SessionCoordinator session, ConsentMemory consent) {
  final config = AppConfig(
    privacyUrl: 'https://policy.test/privacy',
    termsUrl: 'https://policy.test/terms',
    fallbackRoutes: [Uri.parse('https://route.test/')],
  );
  return AppRuntime(
    config: config,
    consent: consent,
    session: session,
    routes: RouteConfigLoader(config: config),
    auth: (_, _) async => throw StateError('No HTTP auth in route fixture'),
  );
}

Future<void> _mount(WidgetTester tester, ProviderContainer container) async {
  await tester.pumpWidget(
    UncontrolledProviderScope(container: container, child: const LuoboApp()),
  );
  await tester.pumpAndSettle();
}

Future<void> _close(
  WidgetTester tester,
  ProviderContainer container,
  Future<void> Function() dispose,
) async {
  await tester.pumpWidget(const SizedBox.shrink());
  container.dispose();
  // Flush widget-zone stream cancellations before real async cleanup.
  await tester.pump();
  Object? failure;
  StackTrace? stack;
  await tester.runAsync(() async {
    try {
      await dispose().timeout(const Duration(seconds: 2));
    } catch (error, trace) {
      failure = error;
      stack = trace;
    }
  });
  if (failure != null) {
    Error.throwWithStackTrace(failure!, stack!);
  }
}

Future<ProviderContainer> _online(
  WidgetTester tester,
  MeetingHarness harness,
) async {
  await tester.runAsync(harness.start);
  final runtime = _runtime(
    harness.sdk.session,
    ConsentMemory()..granted = true,
  );
  await tester.runAsync(runtime.start);
  final container = ProviderContainer(
    overrides: [
      appRuntimeProvider.overrideWith((_) => runtime),
      imGatewayProvider.overrideWithValue(harness.sdk.gateway),
    ],
  );
  addTearDown(() => _close(tester, container, harness.dispose));
  await _mount(tester, container);
  return container;
}

Future<void> _signOut(WidgetTester tester, MeetingHarness harness) async {
  await tester.runAsync(
    () => harness.sdk.session.signOut().timeout(const Duration(seconds: 2)),
  );
  await tester.pumpAndSettle();
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  for (final granted in [false, true]) {
    testWidgets(
      'meeting deep link cannot initialize SDK before ${granted ? 'login' : 'privacy consent'}',
      (tester) async {
        final consent = ConsentMemory()..granted = granted;
        final store = StoreProbe(), im = ImProbe();
        final session = SessionCoordinator(store: store, im: im);
        final runtime = _runtime(session, consent);
        var nativeInitializations = 0;
        final gateway = (await tester.runAsync(
          () async => OpenImGateway(
            hasPrivacyConsent: () => runtime.hasPrivacyConsent,
            managerFactory: () {
              ++nativeInitializations;
              throw StateError('Protected deep link must not initialize SDK');
            },
          ),
        ))!;
        final container = ProviderContainer(
          overrides: [
            appRuntimeProvider.overrideWith((_) => runtime),
            imGatewayProvider.overrideWithValue(gateway),
          ],
        );
        addTearDown(
          () => _close(tester, container, () async {
            await session.dispose();
            await gateway.dispose();
          }),
        );
        await runtime.start();
        final router = container.read(routerProvider)..go('/meetings');
        await _mount(tester, container);
        expect(
          router.routeInformationProvider.value.uri.path,
          granted ? '/login' : '/privacy',
        );
        expect(nativeInitializations, 0);
        expect(im.connects, 0);
        expect(store.reads, granted ? 1 : 0);
        expect(find.text('查找已加载的会议名称或会议号'), findsNothing);
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox.shrink());
      },
    );
  }

  testWidgets(
    'home menu reaches meeting management through the exact protected route and shows disabled media',
    (tester) async {
      final harness = MeetingHarness();
      harness.rows.add(harness.row());
      final container = await _online(tester, harness);
      await tester.tap(find.byTooltip('更多操作'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('会议（预约与管理）'));
      await tester.pumpAndSettle();
      final router = container.read(routerProvider);
      // GoRouter.push keeps the browser URL on the underlying/home page by
      // default. The rendered management page proves this imperative entry.
      expect(find.text('合成会议'), findsOneWidget);
      expect(find.text('查找已加载的会议名称或会议号'), findsOneWidget);
      await tester.tap(find.text('合成会议'));
      await tester.pumpAndSettle();
      expect(find.text(_unavailableCopy), findsOneWidget);
      expect(
        tester
            .widget<FilledButton>(
              find.widgetWithText(FilledButton, '音视频入会暂未开放'),
            )
            .onPressed,
        isNull,
      );
      expect(harness.calls('signalingJoinMeeting'), 0);
      router.go('/meetings/untrusted');
      await tester.pumpAndSettle();
      expect(router.routeInformationProvider.value.uri.path, '/home');
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'signout removes meeting list and discards an old native list response without returning from login',
    (tester) async {
      final harness = MeetingHarness();
      harness.rows.add(harness.row());
      final container = await _online(tester, harness);
      final router = container.read(routerProvider)..go('/meetings');
      await tester.pumpAndSettle();
      expect(find.text('合成会议'), findsOneWidget);
      final entered = Completer<void>(), release = Completer<String>();
      final old = jsonEncode({'meetingInfoList': harness.rows});
      harness.override = (call) async {
        if (call.method == 'signalingGetMeetings') {
          entered.complete();
          return release.future;
        }
        return harness.respond(call);
      };
      // Re-enter the page, starting a new actual-codec read that remains in flight.
      router.go('/home');
      await tester.pumpAndSettle();
      router.go('/meetings');
      await tester.pump();
      await tester.runAsync(
        () => entered.future.timeout(const Duration(seconds: 2)),
      );
      await _signOut(tester, harness);
      expect(router.routeInformationProvider.value.uri.path, '/login');
      release.complete(old);
      await tester.pumpAndSettle();
      expect(find.text('合成会议'), findsNothing);
      expect(find.text('查找已加载的会议名称或会议号'), findsNothing);
      expect(router.routeInformationProvider.value.uri.path, '/login');
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  for (final destination in ['editor', 'invite']) {
    testWidgets(
      'signout while meeting $destination is open clears the old route and cannot submit',
      (tester) async {
        final harness = MeetingHarness();
        harness.rows.add(harness.row());
        final container = await _online(tester, harness);
        final router = container.read(routerProvider)..go('/meetings');
        await tester.pumpAndSettle();
        if (destination == 'editor') {
          await tester.tap(find.byTooltip('预约会议'));
        } else {
          await tester.tap(find.text('合成会议'));
          await tester.pumpAndSettle();
          await tester.scrollUntilVisible(
            find.text('邀请参会者'),
            300,
            scrollable: find.byType(Scrollable).first,
          );
          await tester.ensureVisible(find.text('邀请参会者'));
          await tester.pumpAndSettle();
          await tester.tap(find.text('邀请参会者'));
        }
        await tester.pumpAndSettle();
        await tester.enterText(
          find.byType(TextField).first,
          'old-account-content',
        );
        await _signOut(tester, harness);
        expect(router.routeInformationProvider.value.uri.path, '/login');
        expect(find.text('old-account-content'), findsNothing);
        expect(find.text('提交邀请'), findsNothing);
        expect(find.text('保存会议'), findsNothing);
        expect(harness.calls('signalingCreateMeeting'), 0);
        expect(harness.calls('signalingUpdateMeetingInfo'), 0);
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox.shrink());
      },
    );
  }
}
