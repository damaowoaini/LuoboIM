import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/app_runtime.dart';
import 'package:imapp/core/config/app_config.dart';
import 'package:imapp/core/config/route_config_loader.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/features/app_lock/presentation/app_access_host.dart';
import 'package:imapp/features/app_lock/presentation/app_lock_settings_page.dart';
import 'package:imapp/providers.dart';

import '../../core/access/app_access_test.dart' show AccessHarness;
import '../../core/privacy_bootstrap_test.dart' show ConsentMemory;

Future<void> mountAccess(
  WidgetTester tester,
  AccessHarness h,
  Widget child,
) async {
  final config = AppConfig(
    privacyUrl: 'https://policy.test/privacy',
    termsUrl: 'https://policy.test/terms',
    fallbackRoutes: [Uri.parse('https://route.test')],
  );
  final runtime = AppRuntime(
    config: config,
    consent: ConsentMemory()..granted = true,
    routes: RouteConfigLoader(config: config),
    session: h.session,
    auth: (_, _) async => throw StateError('No HTTP auth in local lock tests'),
  );
  await tester.runAsync(() async {
    await h.start();
    await runtime.start();
  });
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        appRuntimeProvider.overrideWith((_) => runtime),
        appAccessProvider.overrideWith((_) => h.access),
      ],
      child: MaterialApp(
        builder: (_, content) => AppAccessHost(child: content!),
        home: child,
      ),
    ),
  );
  await tester.pumpAndSettle();
  addTearDown(() async {
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump();
    await tester.runAsync(h.dispose);
  });
}

void main() {
  testWidgets(
    'actual settings UI enables a confirmed PIN and cancelling another dialog changes nothing',
    (tester) async {
      final h = AccessHarness();
      await mountAccess(tester, h, const AppLockSettingsPage());
      await tester.tap(find.text('设置解锁密码'));
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextField).at(0), '123456');
      await tester.enterText(find.byType(TextField).at(1), '123456');
      await tester.tap(find.text('确认'));
      await tester.pumpAndSettle();
      expect(h.access.enabled, true);
      expect(find.text('屏幕锁设置已保存'), findsOneWidget);
      await tester.tap(find.text('关闭屏幕锁'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('取消'));
      await tester.pumpAndSettle();
      expect(h.access.enabled, true);
      expect(tester.takeException(), isNull);
    },
  );
  testWidgets(
    'locked content and its semantic label are removed; an incorrect PIN never restores them',
    (tester) async {
      final h = AccessHarness();
      await mountAccess(
        tester,
        h,
        Scaffold(
          body: Semantics(
            label: 'synthetic-protected-label',
            child: const Text('synthetic-private-content'),
          ),
        ),
      );
      await tester.runAsync(() => h.access.setPin('123456'));
      h.access.lock();
      await tester.pumpAndSettle();
      expect(find.text('synthetic-private-content'), findsNothing);
      expect(find.bySemanticsLabel('synthetic-protected-label'), findsNothing);
      await tester.enterText(find.byType(TextField), '999999');
      await tester.tap(find.text('解锁'));
      await tester.pumpAndSettle();
      expect(h.access.unlocked, false);
      expect(find.text('解锁密码不正确'), findsOneWidget);
      await tester.enterText(find.byType(TextField), '123456');
      await tester.tap(find.text('解锁'));
      await tester.pumpAndSettle();
      expect(find.text('synthetic-private-content'), findsOneWidget);
    },
  );
  testWidgets(
    'a real background transition locks and foreground does not silently restore protected content',
    (tester) async {
      final h = AccessHarness();
      await mountAccess(
        tester,
        h,
        const Scaffold(body: Text('synthetic-private-content')),
      );
      await tester.runAsync(() => h.access.setPin('123456'));
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
      await tester.pump();
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pumpAndSettle();
      expect(find.text('synthetic-private-content'), findsNothing);
      expect(find.text('验证后继续使用萝卜对话'), findsOneWidget);
      expect(h.session.state.credentials, isNotNull);
    },
  );
  testWidgets(
    'an old async action is rejected after unlock but a new explicit action gets a fresh lease',
    (tester) async {
      final h = AccessHarness();
      final release = Completer<void>();
      var rejected = 0, writes = 0;
      late Future<void> old;
      Widget page() => Scaffold(
        body: Builder(
          builder: (context) => Column(
            children: [
              TextButton(
                onPressed: () {
                  final access = SessionAccess(h.session);
                  old = () async {
                    await release.future;
                    if (access.isCurrent) {
                      writes++;
                    } else {
                      rejected++;
                    }
                  }();
                },
                child: const Text('begin old action'),
              ),
              TextButton(
                onPressed: () {
                  if (SessionAccess(h.session).isCurrent) writes++;
                },
                child: const Text('new explicit action'),
              ),
            ],
          ),
        ),
      );
      await mountAccess(tester, h, page());
      await tester.runAsync(() => h.access.setPin('123456'));
      await tester.tap(find.text('begin old action'));
      h.access.lock();
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextField), '123456');
      await tester.tap(find.text('解锁'));
      await tester.pumpAndSettle();
      release.complete();
      await old;
      expect(rejected, 1);
      expect(writes, 0);
      await tester.tap(find.text('new explicit action'));
      expect(writes, 1);
    },
  );
}
