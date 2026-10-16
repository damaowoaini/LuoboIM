import 'dart:async';
import 'dart:convert';

import 'package:crypto/crypto.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/features/mine/account_providers.dart';
import 'package:imapp/features/mine/data/account_security_repository.dart';
import 'package:imapp/features/mine/presentation/change_password_page.dart';

import '../../core/session_coordinator_test.dart'
    show MemoryStore, FakeIm, credentials;
import '../../support/http_adapter.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SessionCoordinator session;
  late AccountSecurityRepository repository;
  late TestHttpAdapter http;
  late Future<ResponseBody> Function(RequestOptions) respond;
  setUp(() async {
    session = SessionCoordinator(store: MemoryStore(), im: FakeIm());
    await session.signIn((_) async => credentials('alice'));
    respond = (_) async => jsonResponse('{"errCode":0,"data":{}}');
    http = TestHttpAdapter((request) => respond(request));
    repository = AccountSecurityRepository(
      SessionAccess(session),
      transport: () => Dio()..httpClientAdapter = http,
    );
  });
  tearDown(() async => session.dispose());

  test('password change retains self owner and separate chat credential, only legacy digests travel', () async {
    expect(
      await repository.changePassword('current-fixture', 'new-fixture-pass'),
      PasswordChangeOutcome.confirmed,
    );
    final request = http.requests.single;
    expect(
      request.uri.toString(),
      'https://one.example.com/chat/account/password/change',
    );
    expect(request.headers['token'], 'chat-test');
    expect(request.data, {
      'userID': 'alice',
      'currentPassword': md5.convert(utf8.encode('current-fixture')).toString(),
      'newPassword': md5.convert(utf8.encode('new-fixture-pass')).toString(),
    });
  });

  test('server-triggered local signout and a newer account do not cancel frozen old response or acquire new tokens', () async {
    final entered = Completer<void>(), release = Completer<ResponseBody>();
    respond = (_) {
      entered.complete();
      return release.future;
    };
    final pending = repository.changePassword(
      'current-fixture',
      'new-fixture-pass',
    );
    await entered.future;
    await session.signOut(revoke: false);
    await session.signIn((_) async => credentials('bob'));
    release.complete(jsonResponse('{"errCode":0,"data":{}}'));
    expect(await pending, PasswordChangeOutcome.confirmed);
    expect(http.requests.single.data['userID'], 'alice');
    expect(session.state.credentials?.userId, 'bob');
  });

  test('unknown response is explicit and not automatically retried; definite rejection remains an error', () async {
    respond = (request) async => throw DioException(
      requestOptions: request,
      type: DioExceptionType.connectionError,
    );
    expect(
      await repository.changePassword('current-fixture', 'new-fixture-pass'),
      PasswordChangeOutcome.unconfirmed,
    );
    expect(http.requests.length, 1);
    respond = (_) async =>
        jsonResponse('{"errCode":1002,"errMsg":"unsafe server text"}');
    await expectLater(
      repository.changePassword('current-fixture', 'new-fixture-pass'),
      throwsA(
        isA<AppFailure>().having(
          (e) => e.kind,
          'kind',
          FailureKind.permissionDenied,
        ),
      ),
    );
    expect(http.requests.length, 2);
  });

  test('stale form, matching passwords, unsupported lengths and controls cannot dispatch', () async {
    for (final values in [
      ['old', 'short'],
      ['same-fixture', 'same-fixture'],
      ['old', 'new-password\n'],
    ]) {
      await expectLater(
        repository.changePassword(values[0], values[1]),
        throwsA(isA<AppFailure>()),
      );
    }
    await session.signOut(revoke: false);
    await expectLater(
      repository.changePassword('old-fixture', 'new-fixture-pass'),
      throwsA(isA<AppFailure>()),
    );
    expect(http.requests, isEmpty);
  });

  test('ambiguous server acknowledgement does not become a retryable password failure', () async {
    respond = (_) async => jsonResponse('{"errCode":1000}');
    expect(
      await repository.changePassword('current-fixture', 'new-fixture-pass'),
      PasswordChangeOutcome.unconfirmed,
    );
    expect(http.requests.length, 1);
  });

  testWidgets(
    'late confirmed password response never signs out a newer account or shows stale success',
    (tester) async {
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            passwordChangeRepositoryProvider.overrideWithValue(repository),
          ],
          child: const MaterialApp(home: ChangePasswordPage()),
        ),
      );
      final fields = find.byType(TextFormField);
      await tester.enterText(fields.at(0), 'current-fixture');
      await tester.enterText(fields.at(1), 'new-fixture-pass');
      await tester.enterText(fields.at(2), 'new-fixture-pass');
      await tester.ensureVisible(find.text('确认修改'));
      expect(tester.state<FormState>(find.byType(Form)).validate(), isTrue);
      await tester.pump();
      final submit = tester
          .widget<FilledButton>(find.byType(FilledButton))
          .onPressed;
      expect(submit, isNotNull);
      final scope = ProviderScope.containerOf(
        tester.element(find.byType(ChangePasswordPage)),
      );
      expect(scope.read(passwordChangeRepositoryProvider), same(repository));
      expect(repository.access.isCurrent, isTrue);
      await tester.runAsync(() async {
        // Keep transport completers in the real async zone; otherwise their
        // completion waits for the fake frame pump that this block suspends.
        final entered = Completer<void>(), release = Completer<ResponseBody>();
        respond = (_) {
          entered.complete();
          return release.future;
        };
        final saved = (submit as Future<void> Function())();
        await entered.future.timeout(const Duration(seconds: 2));
        await session.signOut(revoke: false);
        await session.signIn((_) async => credentials('bob'));
        release.complete(jsonResponse('{"errCode":0,"data":{}}'));
        await saved;
      });
      await tester.pumpAndSettle();
      expect(session.state.credentials?.userId, 'bob');
      expect(find.text('密码已修改，请使用新密码重新登录'), findsNothing);
      expect(http.requests.length, 1);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'password form at1.6 text scale validates mismatch without submitting',
    (tester) async {
      tester.view.physicalSize = const Size(360, 640);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      await tester.pumpWidget(
        MaterialApp(
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(context)
                .copyWith(textScaler: const TextScaler.linear(1.6)),
            child: child!,
          ),
          home: const ProviderScope(child: ChangePasswordPage()),
        ),
      );
      final fields = find.byType(TextFormField);
      await tester.enterText(fields.at(0), 'current-fixture');
      await tester.enterText(fields.at(1), 'new-fixture-pass');
      await tester.enterText(fields.at(2), 'mismatched');
      await tester.ensureVisible(find.text('确认修改'));
      await tester.tap(find.text('确认修改'));
      await tester.pumpAndSettle();
      expect(find.text('两次新密码不一致'), findsOneWidget);
      expect(http.requests, isEmpty);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}
