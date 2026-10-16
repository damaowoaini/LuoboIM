import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:imapp/app.dart';
import 'package:imapp/core/app_runtime.dart';
import 'package:imapp/core/config/app_config.dart';
import 'package:imapp/core/config/route_config_loader.dart';
import 'package:imapp/core/network/api_client.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/design/tokens.dart';
import 'package:imapp/features/auth/data/auth_repository.dart';
import 'package:imapp/features/auth/presentation/auth_page.dart';
import 'package:imapp/providers.dart';

import '../../core/privacy_bootstrap_test.dart'
    show ConsentMemory, ImProbe, StoreProbe;
import '../../support/http_adapter.dart';

class AuthFixture {
  AuthFixture({
    bool consentGranted = true,
    bool invitationRequired = false,
    Future<ResponseBody> Function(RequestOptions)? respond,
  }) {
    consent.granted = consentGranted;
    adapter = TestHttpAdapter(
      respond ??
          (request) async => jsonResponse(
            jsonEncode({
              'errCode': 0,
              'data': request.path.endsWith('client_config/get')
                  ? {
                      'config': {
                        'needInvitationCodeRegister': invitationRequired,
                      },
                    }
                  : <String, Object?>{},
            }),
          ),
    );
    final config = AppConfig(
      privacyUrl: 'https://policy.test/privacy',
      termsUrl: 'https://policy.test/terms',
      fallbackRoutes: [Uri.parse('https://route.test/')],
    );
    session = SessionCoordinator(store: store, im: im);
    runtime = AppRuntime(
      config: config,
      consent: consent,
      session: session,
      routes: RouteConfigLoader(config: config),
      auth: (route, generation) async => AuthRepository(
        ApiClient(base: route, dio: Dio()..httpClientAdapter = adapter),
        const DeviceIdentity(
          id: 'fixture-installation',
          platform: 1,
          model: 'iOS',
        ),
      ),
    );
  }
  final consent = ConsentMemory();
  final store = StoreProbe();
  final im = ImProbe();
  late final TestHttpAdapter adapter;
  late final SessionCoordinator session;
  late final AppRuntime runtime;
}

Finder field(String label) => find.byWidgetPredicate(
  (widget) => widget is TextField && widget.decoration?.labelText == label,
);

Future<void> showAuth(
  WidgetTester tester,
  AuthFixture fixture, {
  String location = '/login',
  double textScale = 1,
}) async {
  await fixture.runtime.start();
  final router = GoRouter(
    initialLocation: location,
    routes: [
      GoRoute(path: '/login', builder: (_, _) => const AuthPage()),
      GoRoute(
        path: '/register',
        builder: (_, _) => const AuthPage(mode: AuthPageMode.register),
      ),
      GoRoute(
        path: '/reset',
        builder: (_, _) => const AuthPage(mode: AuthPageMode.reset),
      ),
    ],
  );
  addTearDown(router.dispose);
  await tester.pumpWidget(
    ProviderScope(
      overrides: [appRuntimeProvider.overrideWith((ref) => fixture.runtime)],
      child: MaterialApp.router(
        theme: buildAppTheme(),
        routerConfig: router,
        builder: (context, child) => MediaQuery(
          data: MediaQuery.of(context)
              .copyWith(textScaler: TextScaler.linear(textScale)),
          child: child!,
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

void main() {
  for (final responseCode in [1002, 500, 1001, null]) {
    testWidgets(
      'registration uncertain response $responseCode freezes draft and only explicitly logs in',
      (tester) async {
        var loginAttempts = 0;
        final fixture = AuthFixture(
          respond: (request) async {
            if (request.path.endsWith('client_config/get')) {
              return jsonResponse(
                jsonEncode({
                  'errCode': 0,
                  'data': {
                    'config': {'needVerificationCodeRegister': false},
                  },
                }),
              );
            }
            if (request.path.endsWith('auth/register')) {
              if (responseCode == null) {
                throw DioException(
                  requestOptions: request,
                  type: DioExceptionType.connectionError,
                );
              }
              return jsonResponse(
                jsonEncode({
                  'errCode': responseCode,
                  'errMsg': 'private diagnostics must not appear',
                  'data': null,
                }),
              );
            }
            loginAttempts++;
            if (responseCode == 500 && loginAttempts == 1) {
              return jsonResponse(jsonEncode({'errCode': 1002, 'data': null}));
            }
            return jsonResponse(
              jsonEncode({
                'errCode': 0,
                'data': {
                  'userID': 'fixture-user',
                  'chatToken': 'chat-fixture',
                  'imToken': 'im-fixture',
                  'serverInfo': {'serverName': '测试', 'domains': 'tenant.test'},
                },
              }),
            );
          },
        );
        await showAuth(tester, fixture, location: '/register');
        await tester.enterText(field('手机号或邮箱'), 'person@example.test');
        await tester.pumpAndSettle();
        await tester.enterText(field('昵称'), '原昵称');
        await tester.enterText(field('新密码（至少 8 位）'), 'original-password');
        await tester.ensureVisible(find.text('注册并登录'));
        await tester.tap(find.text('注册并登录'));
        await tester.pumpAndSettle();
        expect(
          fixture.adapter.requests
              .where((r) => r.path.endsWith('auth/register'))
              .length,
          1,
        );
        expect(
          fixture.adapter.requests.where((r) => r.path.endsWith('auth/login')),
          isEmpty,
        );
        expect(fixture.im.connects, 0);
        expect(find.textContaining('注册结果尚未确认'), findsOneWidget);
        expect(find.textContaining('private diagnostics'), findsNothing);
        expect(tester.widget<TextField>(field('手机号或邮箱')).enabled, isFalse);
        expect(tester.widget<TextField>(field('新密码（至少 8 位）')).enabled, isFalse);
        expect(
          tester
              .widget<OutlinedButton>(
                find.widgetWithText(OutlinedButton, '获取验证码'),
              )
              .onPressed,
          isNull,
        );
        expect(find.text('注册并登录'), findsNothing);
        await tester.ensureVisible(find.text('用原密码登录'));
        await tester.tap(find.text('用原密码登录'));
        await tester.pumpAndSettle();
        if (responseCode == 500) {
          expect(fixture.runtime.phase, AppPhase.login);
          expect(find.text('用原密码登录'), findsOneWidget);
          expect(tester.widget<TextField>(field('手机号或邮箱')).enabled, isFalse);
          await tester.ensureVisible(find.text('用原密码登录'));
          await tester.tap(find.text('用原密码登录'));
          await tester.pumpAndSettle();
        }
        expect(
          fixture.adapter.requests
              .where((r) => r.path.endsWith('auth/register'))
              .length,
          1,
        );
        final login =
            fixture.adapter.requests
                    .lastWhere((r) => r.path.endsWith('auth/login'))
                    .data
                as Map;
        expect(login['email'], 'person@example.test');
        expect(login['password'], '2b8a415c3490c16aa3b25dfdaa4412bb');
        expect(login.containsKey('verificationCode'), isFalse);
        expect(fixture.runtime.phase, AppPhase.online);
        expect(fixture.im.connects, 1);
        await tester.pumpWidget(const SizedBox.shrink());
        await fixture.session.dispose();
      },
    );
  }

  testWidgets(
    'registration preflight failure leaves draft editable without submitting',
    (tester) async {
      final fixture = AuthFixture(
        respond: (_) async =>
            jsonResponse(jsonEncode({'errCode': 500, 'data': null})),
      );
      await showAuth(tester, fixture, location: '/register');
      await tester.enterText(field('手机号或邮箱'), 'person@example.test');
      await tester.pumpAndSettle();
      await tester.enterText(field('昵称'), '原昵称');
      await tester.enterText(field('新密码（至少 8 位）'), 'original-password');
      await tester.ensureVisible(find.text('注册并登录'));
      await tester.tap(find.text('注册并登录'));
      await tester.pumpAndSettle();
      expect(
        fixture.adapter.requests.single.path,
        endsWith('client_config/get'),
      );
      expect(tester.widget<TextField>(field('手机号或邮箱')).enabled, isTrue);
      expect(find.text('注册并登录'), findsOneWidget);
      expect(find.text('用原密码登录'), findsNothing);
    },
  );

  testWidgets(
    'leaving registration during config request never creates an account later',
    (tester) async {
      final configReply = Completer<ResponseBody>();
      final fixture = AuthFixture(respond: (_) => configReply.future);
      await showAuth(tester, fixture, location: '/register');
      await tester.enterText(field('手机号或邮箱'), 'person@example.test');
      await tester.pumpAndSettle();
      await tester.enterText(field('昵称'), '原昵称');
      await tester.enterText(field('新密码（至少 8 位）'), 'original-password');
      await tester.ensureVisible(find.text('注册并登录'));
      await tester.tap(find.text('注册并登录'));
      await tester.pump();
      expect(tester.widget<TextField>(field('手机号或邮箱')).enabled, isFalse);
      await tester.tap(find.byType(BackButton));
      await tester.pump();
      configReply.complete(
        jsonResponse(
          jsonEncode({
            'errCode': 0,
            'data': {
              'config': {'needVerificationCodeRegister': false},
            },
          }),
        ),
      );
      await tester.pumpAndSettle();
      expect(fixture.adapter.requests.length, 1);
      expect(
        fixture.adapter.requests.single.path,
        endsWith('client_config/get'),
      );
      expect(fixture.runtime.phase, AppPhase.login);
      expect(fixture.im.connects, 0);
      expect(find.text('萝卜对话'), findsOneWidget);
    },
  );

  testWidgets(
    'consent write failure is visible without resetting the router or opening SDK',
    (tester) async {
      final fixture = AuthFixture(consentGranted: false);
      fixture.consent.failWrite = true;
      await fixture.runtime.start();
      late GoRouter router;
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            appRuntimeProvider.overrideWith((ref) => fixture.runtime),
          ],
          child: Consumer(
            builder: (context, ref, _) {
              router = ref.watch(routerProvider);
              return const LuoboApp();
            },
          ),
        ),
      );
      await tester.pumpAndSettle();
      final initialRouter = router;
      await tester.tap(find.text('同意并继续'));
      await tester.pumpAndSettle();
      expect(find.text('无法安全保存登录信息，请重试'), findsOneWidget);
      expect(identical(initialRouter, router), isTrue);
      expect(fixture.store.reads, 0);
      expect(fixture.im.connects, 0);
      expect(fixture.adapter.requests, isEmpty);
    },
  );

  testWidgets(
    'code login sends email and never enters a session with missing tokens',
    (tester) async {
      final fixture = AuthFixture();
      await showAuth(tester, fixture);
      await tester.enterText(field('手机号或邮箱'), 'person@example.test');
      await tester.ensureVisible(find.text('使用验证码登录'));
      await tester.tap(find.text('使用验证码登录'));
      await tester.pumpAndSettle();
      await tester.enterText(field('验证码'), '123456');
      await tester.ensureVisible(find.text('登录'));
      await tester.tap(find.text('登录'));
      await tester.pumpAndSettle();
      final payload = fixture.adapter.requests.single.data as Map;
      expect(payload['email'], 'person@example.test');
      expect(payload['verificationCode'], '123456');
      expect(payload.containsKey('password'), isFalse);
      expect(fixture.session.state.credentials, isNull);
      expect(fixture.im.connects, 0);
      expect(find.text('服务返回的数据不完整，请稍后重试'), findsOneWidget);
    },
  );

  testWidgets(
    'missing required invitation does not send OTP or start cooldown',
    (tester) async {
      final fixture = AuthFixture(invitationRequired: true);
      await showAuth(tester, fixture, location: '/register');
      await tester.enterText(field('手机号或邮箱'), 'person@example.test');
      await tester.ensureVisible(find.text('获取验证码'));
      await tester.tap(find.text('获取验证码'));
      await tester.pumpAndSettle();
      expect(
        fixture.adapter.requests.single.path,
        endsWith('client_config/get'),
      );
      expect(find.text('请先填写邀请码'), findsOneWidget);
      expect(find.text('获取验证码'), findsOneWidget);
      expect(find.text('60s'), findsNothing);
    },
  );

  testWidgets('reset preserves email and returns to login only after success', (
    tester,
  ) async {
    final fixture = AuthFixture();
    await showAuth(tester, fixture, location: '/reset');
    await tester.enterText(field('手机号或邮箱'), 'person@example.test');
    await tester.pumpAndSettle();
    await tester.enterText(field('新密码（至少 8 位）'), 'new-password');
    await tester.enterText(field('验证码'), '123456');
    await tester.ensureVisible(find.text('重置密码'));
    await tester.tap(find.text('重置密码'));
    await tester.pumpAndSettle();
    expect(
      (fixture.adapter.requests.single.data as Map)['email'],
      'person@example.test',
    );
    expect(find.text('萝卜对话'), findsOneWidget);
    expect(find.text('密码已重置，请重新登录'), findsOneWidget);
  });

  for (final location in ['/login', '/register', '/reset']) {
    testWidgets('$location stays scrollable at 320pt and 200% text', (
      tester,
    ) async {
      tester.view.physicalSize = const Size(320, 568);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      await showAuth(tester, AuthFixture(), location: location, textScale: 2);
      expect(tester.takeException(), isNull);
      await tester.drag(
        find.byType(SingleChildScrollView),
        const Offset(0, -1200),
      );
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
    });
  }
}
