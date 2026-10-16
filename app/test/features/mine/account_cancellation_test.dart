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
import 'package:imapp/features/mine/presentation/cancel_account_page.dart';

import '../../core/session_coordinator_test.dart'
    show MemoryStore, FakeIm, credentials;
import '../../support/http_adapter.dart';

String accepted({String user = 'alice', String status = 'sessions_revoked'}) =>
    jsonEncode({
      'errCode': 0,
      'data': {
        'userID': user,
        'status': status,
        'requestedAt': 1700000000000,
        'dataErased': false,
      },
    });

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late SessionCoordinator session;
  late AccountSecurityRepository repository;
  late TestHttpAdapter http;
  late Future<ResponseBody> Function(RequestOptions) respond;
  setUp(() async {
    session = SessionCoordinator(store: MemoryStore(), im: FakeIm());
    await session.signIn((_) async => credentials('alice'));
    respond = (_) async => jsonResponse(accepted());
    http = TestHttpAdapter((request) => respond(request));
    repository = AccountSecurityRepository(
      SessionAccess(session),
      transport: () => Dio()..httpClientAdapter = http,
    );
  });
  tearDown(() async => session.dispose());

  test('cancellation sends only captured self credentials and digest, with optional MFA', () async {
    expect(
      await repository.cancelAccount(
        'current-fixture',
        googleAuthCode: '123456',
      ),
      AccountCancellationOutcome.sessionsRevoked,
    );
    final request = http.requests.single;
    expect(
      request.uri.toString(),
      'https://one.example.com/chat/account/cancel',
    );
    expect(request.headers['token'], 'chat-test');
    expect(request.data, {
      'userID': 'alice',
      'password': md5.convert(utf8.encode('current-fixture')).toString(),
      'googleAuthCode': '123456',
    });
  });

  test('pending cleanup is distinct; other owner, malformed or unfamiliar success remains unconfirmed', () async {
    respond = (_) async =>
        jsonResponse(accepted(status: 'cancellation_pending'));
    expect(
      await repository.cancelAccount('current-fixture'),
      AccountCancellationOutcome.revocationPending,
    );
    for (final response in [
      accepted(user: 'bob'),
      accepted(status: 'erased'),
      '{"errCode":0,"data":{}}',
    ]) {
      respond = (_) async => jsonResponse(response);
      expect(
        await repository.cancelAccount('current-fixture'),
        AccountCancellationOutcome.unconfirmed,
      );
    }
  });

  test(
    'lost acknowledgement never retries and definite rejection is retained',
    () async {
      respond = (request) async => throw DioException(
        requestOptions: request,
        type: DioExceptionType.connectionError,
      );
      expect(
        await repository.cancelAccount('current-fixture'),
        AccountCancellationOutcome.unconfirmed,
      );
      expect(http.requests.length, 1);
      respond = (_) async =>
          jsonResponse('{"errCode":1002,"errMsg":"private server details"}');
      await expectLater(
        repository.cancelAccount('current-fixture'),
        throwsA(isA<AppFailure>().having((e) => e.code, 'code', 1002)),
      );
      expect(http.requests.length, 2);
    },
  );

  test(
    'invalid MFA, empty password and stale access do not dispatch',
    () async {
      for (final code in ['12345', '1234567', 'abcdef']) {
        await expectLater(
          repository.cancelAccount('current-fixture', googleAuthCode: code),
          throwsA(isA<AppFailure>()),
        );
      }
      await expectLater(
        repository.cancelAccount(''),
        throwsA(isA<AppFailure>()),
      );
      await session.signOut(revoke: false);
      await expectLater(
        repository.cancelAccount('current-fixture'),
        throwsA(isA<AppFailure>()),
      );
      expect(http.requests, isEmpty);
    },
  );

  test('ambiguous server acknowledgement does not become a retryable cancellation failure', () async {
    respond = (_) async => jsonResponse('{"errCode":1000}');
    expect(
      await repository.cancelAccount('current-fixture'),
      AccountCancellationOutcome.unconfirmed,
    );
    expect(http.requests.length, 1);
  });

  testWidgets(
    'small large-text form requires acknowledgement and explicit confirmation; cancel sends nothing',
    (tester) async {
      tester.view.physicalSize = const Size(360, 640);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            accountSecurityRepositoryProvider.overrideWithValue(repository),
          ],
          child: MaterialApp(
            builder: (context, child) => MediaQuery(
              data: MediaQuery.of(context)
                  .copyWith(textScaler: const TextScaler.linear(1.6)),
              child: child!,
            ),
            home: const CancelAccountPage(),
          ),
        ),
      );
      expect(
        tester.widget<FilledButton>(find.byType(FilledButton)).onPressed,
        isNull,
      );
      await tester.enterText(
        find.byType(TextFormField).first,
        'current-fixture',
      );
      await tester.ensureVisible(find.byType(CheckboxListTile));
      await tester.tap(find.byType(CheckboxListTile));
      await tester.pump();
      await tester.ensureVisible(find.text('申请注销'));
      await tester.tap(find.text('申请注销'));
      await tester.pumpAndSettle();
      expect(find.text('确认注销当前账号？'), findsOneWidget);
      expect(http.requests, isEmpty);
      await tester.tap(find.text('返回'));
      await tester.pumpAndSettle();
      expect(http.requests, isEmpty);
      expect(session.state.credentials?.userId, 'alice');
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'late cancellation acknowledgement neither clears a newer account nor announces stale success',
    (tester) async {
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            accountSecurityRepositoryProvider.overrideWithValue(repository),
          ],
          child: const MaterialApp(home: CancelAccountPage()),
        ),
      );
      await tester.enterText(
        find.byType(TextFormField).first,
        'current-fixture',
      );
      await tester.ensureVisible(find.byType(CheckboxListTile));
      await tester.tap(find.byType(CheckboxListTile));
      await tester.pump();
      await tester.ensureVisible(find.text('申请注销'));
      await tester.tap(find.text('申请注销'));
      await tester.pumpAndSettle();
      late Completer<void> entered;
      late Completer<ResponseBody> release;
      await tester.runAsync(() async {
        entered = Completer<void>();
        release = Completer<ResponseBody>();
        respond = (_) {
          entered.complete();
          return release.future;
        };
      });
      await tester.tap(find.text('确认注销'));
      // DialogRoute completes only after its reverse transition finishes.
      await tester.pumpAndSettle();
      await tester.runAsync(() async {
        await entered.future.timeout(const Duration(seconds: 2));
        await session.signOut(revoke: false);
        await session.signIn((_) async => credentials('bob'));
        release.complete(
          jsonResponse(accepted(status: 'cancellation_pending')),
        );
        await Future<void>.delayed(const Duration(milliseconds: 30));
      });
      await tester.pumpAndSettle();
      expect(session.state.credentials?.userId, 'bob');
      expect(find.textContaining('注销请求已受理'), findsNothing);
      expect(http.requests.length, 1);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}
