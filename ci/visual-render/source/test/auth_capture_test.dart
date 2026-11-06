import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/app_runtime.dart';
import 'package:imapp/core/config/app_config.dart';
import 'package:imapp/core/config/route_config_loader.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/features/auth/presentation/auth_page.dart';
import 'package:imapp/providers.dart';

import '../core/im/capture_theme.dart';
import '../core/privacy_bootstrap_test.dart' show ConsentMemory, StoreProbe, ImProbe;

// CI-only overlay onto app/test/visual/. This renders the existing product
// AuthPage with an explicit initial UI state. It neither authenticates nor
// claims that bootstrap, native SDK, backend configuration or MFA succeeded.
void main() {
  testWidgets('visual evidence renders actual AuthPage at375 without authentication', (tester) async {
    const output = String.fromEnvironment('TEST_CAPTURE_DIR');
    expect(output, isNotEmpty);
    final config = AppConfig(routeSources: const []);
    final store = StoreProbe();
    final im = ImProbe();
    final session = SessionCoordinator(store: store, im: im);
    addTearDown(session.dispose);
    var routeRequests = 0;
    var authRequests = 0;
    final runtime = AppRuntime(
      config: config,
      consent: ConsentMemory()..granted = true,
      session: session,
      routes: RouteConfigLoader(config: config, fetch: (_, _) async {
        routeRequests++;
        throw StateError('capture has no backend');
      }),
      auth: (_, _) async {
        authRequests++;
        throw StateError('capture does not authenticate');
      },
    )..phase = AppPhase.login;
    tester.view.physicalSize = const Size(375, 812);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final key = GlobalKey();
    final theme = await captureTheme(tester);
    await tester.pumpWidget(ProviderScope(
      overrides: [appRuntimeProvider.overrideWith((_) => runtime)],
      child: RepaintBoundary(key: key, child: MaterialApp(
        theme: theme,
        debugShowCheckedModeBanner: false,
        builder: (context, child) => MediaQuery(
          data: MediaQuery.of(context).copyWith(textScaler: TextScaler.noScaling),
          child: child!,
        ),
        home: const AuthPage(),
      )),
    ));
    await tester.pumpAndSettle();
    expect(find.text(Brand.name), findsOneWidget);
    expect(find.widgetWithText(TextFormField, '手机号或邮箱'), findsOneWidget);
    expect(find.widgetWithText(TextFormField, '密码'), findsOneWidget);
    expect(find.widgetWithText(FilledButton, '登录'), findsOneWidget);
    expect(tester.takeException(), isNull);
    expect(routeRequests, 0);
    expect(authRequests, 0);
    expect(im.connects, 0);
    expect(store.reads, 0);
    await tester.runAsync(() async {
      final boundary = key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
      final image = await boundary.toImage(pixelRatio: 1);
      try {
        final data = (await image.toByteData(format: ui.ImageByteFormat.png))!;
        expect(data.lengthInBytes, lessThanOrEqualTo(4 << 20));
        final file = File('$output/03-app-auth-375.png');
        expect(await file.exists(), isFalse);
        await file.writeAsBytes(data.buffer.asUint8List(), flush: true);
      } finally {
        image.dispose();
      }
    });
    await tester.pumpWidget(const SizedBox.shrink());
    expect(tester.takeException(), isNull);
  });
}
