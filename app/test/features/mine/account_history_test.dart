import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/features/mine/account_providers.dart';
import 'package:imapp/features/mine/data/account_repository.dart';
import 'package:imapp/features/mine/presentation/account_settings_page.dart';

import '../../core/im/sdk_harness.dart';
import '../../support/http_adapter.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SdkHarness harness;
  late TenantApi api;
  late AccountRepository repository;
  var pendingSync = false;

  setUp(() async {
    harness = SdkHarness();
    await harness.start();
    pendingSync = false;
    final user = <String, Object?>{
      'userID': 'alice',
      'nickname': '测试账号',
      'birth': 0,
      'allowAddFriend': 1,
      'allowBeep': 1,
      'allowVibration': 1,
    };
    api = TenantApi(
      SessionAccess(harness.session),
      dio: Dio()
        ..httpClientAdapter = TestHttpAdapter((request) async {
          if (request.uri.path.endsWith('/update')) {
            user.addAll(
              Map<String, Object?>.from(request.data as Map)..remove('userID'),
            );
            return jsonResponse(
              jsonEncode({
                'errCode': 0,
                'data': {'imSyncPending': pendingSync},
              }),
            );
          }
          return jsonResponse(
            jsonEncode({
              'errCode': 0,
              'data': {
                'users': [user],
              },
            }),
          );
        }),
    );
    repository = AccountRepository(api, harness.gateway);
  });

  tearDown(() async {
    api.close();
    await harness.dispose();
  });

  int clears() => harness.calls
      .where((call) => call.method == 'deleteAllMsgFromLocalAndSvr')
      .length;

  Future<void> mount(WidgetTester tester) async {
    tester.view.physicalSize = const Size(480, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final router = GoRouter(
      initialLocation: '/mine/settings',
      routes: [
        GoRoute(
          path: '/mine/settings',
          builder: (_, _) => const AccountSettingsPage(),
        ),
        GoRoute(
          path: '/home',
          builder: (_, _) => const Scaffold(body: Text('测试会话列表')),
        ),
      ],
    );
    addTearDown(router.dispose);
    await tester.pumpWidget(
      ProviderScope(
        overrides: [accountRepositoryProvider.overrideWithValue(repository)],
        child: MaterialApp.router(routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();
  }

  Future<void> openConfirmation(WidgetTester tester) async {
    await tester.ensureVisible(find.text('清空全部聊天记录'));
    await tester.tap(find.text('清空全部聊天记录'));
    await tester.pumpAndSettle();
    expect(find.text('清空全部聊天记录？'), findsOneWidget);
  }

  test(
    'clear uses the original native session once and rejects concurrent replay',
    () async {
      final entered = Completer<void>(), release = Completer<void>();
      harness.overrideCall = (call) async {
        if (call.method == 'deleteAllMsgFromLocalAndSvr') {
          entered.complete();
          await release.future;
        }
        return harness.respond(call);
      };
      final first = repository.clearAllHistory();
      await entered.future;
      await expectLater(
        repository.clearAllHistory(),
        throwsA(isA<AppFailure>()),
      );
      expect(clears(), 1);
      release.complete();
      await first;
    },
  );

  test(
    'a superseded account never dispatches a clear for its replacement',
    () async {
      await harness.session.signOut();
      await expectLater(
        repository.clearAllHistory(),
        throwsA(isA<AppFailure>()),
      );
      expect(clears(), 0);
    },
  );

  testWidgets(
    'cancelling the explicit all-history confirmation dispatches nothing',
    (tester) async {
      await mount(tester);
      await openConfirmation(tester);
      await tester.tap(find.text('取消'));
      await tester.pumpAndSettle();
      expect(clears(), 0);
    },
  );

  testWidgets(
    'one confirmed request disables actions and reports ACK without physical-deletion claim',
    (tester) async {
      final entered = Completer<void>(), release = Completer<void>();
      harness.overrideCall = (call) async {
        if (call.method == 'deleteAllMsgFromLocalAndSvr') {
          entered.complete();
          await release.future;
        }
        return harness.respond(call);
      };
      await mount(tester);
      await openConfirmation(tester);
      await tester.tap(find.text('清空记录'));
      await tester.pump();
      await entered.future;
      final tile = tester.widget<ListTile>(
        find.ancestor(
          of: find.text('清空全部聊天记录'),
          matching: find.byType(ListTile),
        ),
      );
      expect(tile.onTap, isNull);
      expect(clears(), 1);
      release.complete();
      await tester.pumpAndSettle();
      expect(find.text('清理请求已完成，请返回会话列表核对。'), findsOneWidget);
      expect(find.text('全部聊天记录已清空'), findsNothing);
      expect(clears(), 1);
    },
  );

  testWidgets(
    'unknown native result remains honest and is never automatically replayed',
    (tester) async {
      harness.overrideCall = (call) async {
        if (call.method == 'deleteAllMsgFromLocalAndSvr') {
          throw PlatformException(code: '10005', message: 'synthetic failure');
        }
        return harness.respond(call);
      };
      await mount(tester);
      await openConfirmation(tester);
      await tester.tap(find.text('清空记录'));
      await tester.pumpAndSettle();
      expect(find.textContaining('清理结果尚未确认'), findsOneWidget);
      expect(find.text('清理请求已完成，请返回会话列表核对。'), findsNothing);
      await tester.pump(const Duration(seconds: 2));
      expect(clears(), 1);
      await tester.ensureVisible(find.text('查看会话'));
      await tester.tap(find.text('查看会话'));
      await tester.pumpAndSettle();
      expect(find.text('测试会话列表'), findsOneWidget);
      expect(clears(), 1);
    },
  );

  testWidgets(
    'a confirmation from an old account cannot clear after sign-out',
    (tester) async {
      await mount(tester);
      await openConfirmation(tester);
      await tester.runAsync(
        () => harness.session.signOut().timeout(const Duration(seconds: 2)),
      );
      await tester.tap(find.text('清空记录'));
      await tester.pumpAndSettle();
      expect(clears(), 0);
    },
  );

  testWidgets('settings preserves a pending profile projection result', (
    tester,
  ) async {
    pendingSync = true;
    await mount(tester);
    await tester.tap(find.text('声音提醒'));
    await tester.pumpAndSettle();
    expect(find.text('设置已保存，部分资料仍在同步，可稍后刷新核对'), findsOneWidget);
  });
}
