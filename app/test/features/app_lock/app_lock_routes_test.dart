import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/app.dart';
import 'package:imapp/core/access/app_access.dart';
import 'package:imapp/core/app_runtime.dart';
import 'package:imapp/core/config/app_config.dart';
import 'package:imapp/core/config/route_config_loader.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/features/moments/moments_providers.dart';
import 'package:imapp/providers.dart';

import '../../core/access/app_access_test.dart' show AccessMemory, NativeProbe;
import '../../core/im/sdk_harness.dart';
import '../../core/privacy_bootstrap_test.dart'
    show ConsentMemory, StoreProbe, ImProbe;

void main() {
  for (final consent in [false, true]) {
    testWidgets(
      'app-lock deep link before ${consent ? 'login' : 'consent'} never accesses local policy or biometric platform',
      (tester) async {
        final local = AccessMemory(), native = NativeProbe();
        final access = AppAccessCoordinator(store: local, native: native);
        final im = ImProbe(), store = StoreProbe();
        final session = SessionCoordinator(
          store: store,
          im: im,
          beforeRestore: access.authorizeRestore,
          authenticated: access.authenticated,
        );
        access.attach(session);
        final config = AppConfig(
          privacyUrl: 'https://policy.test/privacy',
          termsUrl: 'https://policy.test/terms',
          fallbackRoutes: [Uri.parse('https://route.test')],
        );
        final runtime = AppRuntime(
          config: config,
          consent: ConsentMemory()..granted = consent,
          session: session,
          routes: RouteConfigLoader(config: config),
          auth: (_, _) async => throw StateError('no HTTP in route fixture'),
        );
        await runtime.start();
        final container = ProviderContainer(
          overrides: [
            appRuntimeProvider.overrideWith((_) => runtime),
            appAccessProvider.overrideWith((_) => access),
          ],
        );
        container.read(routerProvider).go('/mine/app-lock');
        await tester.pumpWidget(
          UncontrolledProviderScope(
            container: container,
            child: const LuoboApp(),
          ),
        );
        await tester.pumpAndSettle();
        expect(
          container
              .read(routerProvider)
              .routeInformationProvider
              .value
              .uri
              .path,
          consent ? '/login' : '/privacy',
        );
        expect(local.reads, 0);
        expect(native.calls, 0);
        expect(im.connects, 0);
        expect(find.text('设置解锁密码'), findsNothing);
        await tester.pumpWidget(const SizedBox.shrink());
        container.dispose();
        await tester.pump();
        await tester.runAsync(session.dispose);
      },
    );
  }
  testWidgets(
    'real Mine entry opens lock settings and locking retires its old PIN modal before fresh navigation',
    (tester) async {
      late SdkHarness sdk;
      sdk = SdkHarness(
        interactiveRevision: () => sdk.session.interactiveRevision,
        hasInteractiveAccess: () => sdk.session.interactiveAllowed,
      );
      await tester.runAsync(sdk.start);
      final local = AccessMemory(), native = NativeProbe();
      final access = AppAccessCoordinator(store: local, native: native)
        ..attach(sdk.session);
      await tester.runAsync(() async {
        await sdk.runtime.start();
        await access.authenticated(
          sdk.credentials,
          sdk.session.state.generation,
        );
      });
      final container = ProviderContainer(
        overrides: [
          appRuntimeProvider.overrideWith((_) => sdk.runtime),
          appAccessProvider.overrideWith((_) => access),
          imGatewayProvider.overrideWithValue(sdk.gateway),
          momentUnreadProvider.overrideWith((_) async => 0),
        ],
      );
      addTearDown(() async {
        await tester.pumpWidget(const SizedBox.shrink());
        container.dispose();
        await tester.pump();
        await tester.runAsync(sdk.dispose);
      });
      await tester.pumpWidget(
        UncontrolledProviderScope(
          container: container,
          child: const LuoboApp(),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.text('我的').last);
      await tester.pumpAndSettle();
      await tester.tap(find.text('屏幕锁设置'));
      await tester.pumpAndSettle();
      expect(find.text('设置解锁密码'), findsOneWidget);
      await tester.runAsync(() => access.setPin('123456'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('修改解锁密码'));
      await tester.pumpAndSettle();
      expect(find.byType(AlertDialog), findsOneWidget);
      access.lock();
      await tester.pumpAndSettle();
      expect(find.byType(AlertDialog), findsNothing);
      expect(find.text('当前解锁密码'), findsNothing);
      await tester.enterText(find.byType(TextField), '123456');
      await tester.tap(find.text('解锁'));
      await tester.pumpAndSettle();
      expect(
        container.read(routerProvider).routeInformationProvider.value.uri.path,
        '/home',
      );
      expect(find.byType(AlertDialog), findsNothing);
      await tester.tap(find.text('我的').last);
      await tester.pumpAndSettle();
      await tester.tap(find.text('屏幕锁设置'));
      await tester.pumpAndSettle();
      expect(find.text('修改解锁密码'), findsOneWidget);
      expect(tester.takeException(), isNull);
    },
  );
  testWidgets(
    'the real lock screen keeps logout clickable during slow native PIN verification',
    (tester) async {
      late SdkHarness sdk;
      sdk = SdkHarness(
        interactiveRevision: () => sdk.session.interactiveRevision,
        hasInteractiveAccess: () => sdk.session.interactiveAllowed,
      );
      await tester.runAsync(sdk.start);
      final native = NativeProbe();
      final access = AppAccessCoordinator(store: AccessMemory(), native: native)
        ..attach(sdk.session);
      final config = AppConfig(
        privacyUrl: 'https://policy.test/privacy',
        termsUrl: 'https://policy.test/terms',
        fallbackRoutes: [Uri.parse('https://route.test')],
      );
      final runtime = AppRuntime(
        config: config,
        consent: ConsentMemory()..granted = true,
        session: sdk.session,
        routes: RouteConfigLoader(config: config),
        auth: (_, _) async =>
            throw StateError('no HTTP auth in logout fixture'),
      );
      await tester.runAsync(() async {
        await runtime.start();
        await access.authenticated(
          sdk.credentials,
          sdk.session.state.generation,
        );
        await access.setPin('123456');
      });
      final container = ProviderContainer(
        overrides: [
          appRuntimeProvider.overrideWith((_) => runtime),
          appAccessProvider.overrideWith((_) => access),
          imGatewayProvider.overrideWithValue(sdk.gateway),
          momentUnreadProvider.overrideWith((_) async => 0),
        ],
      );
      addTearDown(() async {
        await tester.pumpWidget(const SizedBox.shrink());
        container.dispose();
        await tester.pump();
        await tester.runAsync(sdk.dispose);
      });
      await tester.pumpWidget(
        UncontrolledProviderScope(
          container: container,
          child: const LuoboApp(),
        ),
      );
      access.lock();
      await tester.pumpAndSettle();
      native.delayedPin = Completer<String>();
      await tester.enterText(find.byType(TextField), '123456');
      await tester.tap(find.text('解锁'));
      await tester.pump();
      await tester.pump();
      expect(access.busy, true);
      final logout = find.widgetWithText(TextButton, '退出账号');
      expect(tester.widget<TextButton>(logout).onPressed, isNotNull);
      await tester.tap(logout);
      await tester.runAsync(() async {
        for (var i = 0; i < 12; i++) {
          await Future<void>.delayed(Duration.zero);
        }
      });
      await tester.pumpAndSettle();
      expect(
        container.read(routerProvider).routeInformationProvider.value.uri.path,
        '/login',
      );
      native.delayedPin!.complete(base64Encode(List.filled(32, 7)));
      await tester.pumpAndSettle();
      expect(sdk.session.state.phase, SessionPhase.signedOut);
      expect(tester.takeException(), isNull);
    },
  );
}
