import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/features/complaint/complaint_providers.dart';
import 'package:imapp/features/complaint/data/complaint_repository.dart';
import 'package:imapp/features/complaint/presentation/complaint_page.dart';

import '../../core/session_coordinator_test.dart'
    show MemoryStore, FakeIm, credentials;
import '../../support/http_adapter.dart';

void main() {
  const submissionId = '00000000-0000-4000-8000-000000000001';
  TestWidgetsFlutterBinding.ensureInitialized();
  late SessionCoordinator session;
  late TenantApi api;
  late ComplaintRepository repository;
  late TestHttpAdapter http;
  late Future<ResponseBody> Function(RequestOptions) respond;
  setUp(() async {
    session = SessionCoordinator(store: MemoryStore(), im: FakeIm());
    await session.signIn((_) async => credentials('alice'));
    respond = (_) async =>
        jsonResponse('{"errCode":0,"data":{"complaintID":"receipt"}}');
    http = TestHttpAdapter((request) => respond(request));
    api = TenantApi(
      SessionAccess(session),
      dio: Dio()..httpClientAdapter = http,
    );
    repository = ComplaintRepository(api);
  });
  tearDown(() async {
    api.close();
    await session.dispose();
  });

  Future<String> submit({
    String id = 'target',
    String reason = '其他',
    String description = '',
    List<Uri> evidence = const [],
  }) => repository.submit(
    submissionId: submissionId,
    target: ComplaintTarget.group,
    targetId: id,
    targetName: '群组',
    reason: reason,
    description: description,
    evidence: evidence,
  );

  test('real complaint DTO uses self chat credential and legacy group/evidence contract', () async {
    expect(
      await submit(
        evidence: [Uri.parse('https://objects.example.test/evidence.png')],
      ),
      'receipt',
    );
    final request = http.requests.single;
    expect(
      request.uri.toString(),
      'https://one.example.com/chat/complaint/submit',
    );
    expect(request.headers['token'], 'chat-test');
    expect(request.data, {
      'clientSubmissionID': submissionId,
      'complaintUserID': 'alice',
      'complaintType': 2,
      'targetID': 'target',
      'targetName': '群组',
      'reason': '其他',
      'description': '',
      'evidence': ['https://objects.example.test/evidence.png'],
    });
  });

  test('lost submit ACK is recovered by the same owner-scoped receipt without resubmitting', () async {
    respond = (request) async {
      if (request.uri.path.endsWith('/submit')) {
        throw DioException(
          requestOptions: request,
          type: DioExceptionType.connectionError,
        );
      }
      return jsonResponse(
        jsonEncode({
          'errCode': 0,
          'data': {
            'found': true,
            'complaintID': 'recovered',
            'status': 0,
            'clientSubmissionID': submissionId,
          },
        }),
      );
    };
    expect(await submit(), 'recovered');
    expect(http.requests.map((r) => r.uri.path), [
      '/chat/complaint/submit',
      '/chat/complaint/receipt',
    ]);
    expect(http.requests.last.data, {'clientSubmissionID': submissionId});
    expect(
      http.requests.every((r) => r.headers['token'] == 'chat-test'),
      isTrue,
    );
  });

  test(
    'missing receipt remains unknown and never starts another submission',
    () async {
      respond = (request) async {
        if (request.uri.path.endsWith('/submit')) {
          throw DioException(
            requestOptions: request,
            type: DioExceptionType.receiveTimeout,
          );
        }
        return jsonResponse('{"errCode":0,"data":{"found":false}}');
      };
      await expectLater(
        submit(),
        throwsA(
          isA<AppFailure>().having((e) => e.kind, 'kind', FailureKind.timeout),
        ),
      );
      expect(
        http.requests.where((r) => r.uri.path.endsWith('/submit')).length,
        1,
      );
      expect(http.requests.length, 2);
      expect(repository.canReplay, isFalse);
    },
  );

  test(
    'receipt mismatch and definite rejection cannot report success or resubmit',
    () async {
      respond = (_) async => jsonResponse(
        jsonEncode({
          'errCode': 0,
          'data': {
            'found': true,
            'complaintID': 'foreign',
            'status': 0,
            'clientSubmissionID': '00000000-0000-4000-8000-000000000002',
          },
        }),
      );
      await expectLater(
        repository.receipt(submissionId),
        throwsA(
          isA<AppFailure>().having(
            (e) => e.kind,
            'kind',
            FailureKind.invalidResponse,
          ),
        ),
      );
      respond = (_) async => jsonResponse('{"errCode":1002}');
      await expectLater(submit(), throwsA(isA<AppFailure>()));
      expect(http.requests.length, 2);
      expect(
        http.requests.where((r) => r.uri.path.endsWith('/submit')).length,
        1,
      );
    },
  );

  test('long supplementary-character nickname is a bounded snapshot and does not block reporting its ID', () async {
    await repository.submit(
      submissionId: submissionId,
      target: ComplaintTarget.user,
      targetId: 'target',
      targetName: '😀' * 64,
      reason: '其他',
    );
    final data = http.requests.single.data as Map;
    expect(utf8.encode(data['targetName'] as String).length, 252);
    expect(data['targetName'], '😀' * 63);
    expect(data['targetID'], 'target');
  });

  test('invalid text, image URL and paging never dispatch', () async {
    for (final action in <Future<Object?> Function()>[
      () => submit(id: ' target'),
      () => submit(reason: ''),
      () => submit(description: 'x' * 4097),
      () => submit(reason: 'private\nreason'),
      () => submit(evidence: [Uri.parse('http://unsafe.test/a.png')]),
      () => submit(evidence: [Uri.parse('https://secret@unsafe.test/a.png')]),
      () => submit(
        evidence: [
          Uri.parse('https://safe.test/a.png'),
          Uri.parse('https://safe.test/a.png'),
        ],
      ),
      () => repository.search(page: 0),
    ]) {
      await expectLater(action(), throwsA(isA<AppFailure>()));
    }
    expect(http.requests, isEmpty);
  });

  test('own paged receipts decode legacy evidence and discard unsafe images; foreign ownership is rejected', () async {
    var owner = 'alice';
    respond = (_) async => jsonResponse(
      jsonEncode({
        'errCode': 0,
        'data': {
          'total': 1,
          'complaints': [
            {
              'id': 'receipt',
              'complaintUserID': owner,
              'targetID': 'target',
              'targetName': '目标',
              'reason': '其他',
              'description': 'details',
              'status': 0,
              'createTime': '2026-10-15T12:00:00+08:00',
              'evidence': jsonEncode([
                'http://unsafe.test/a.png',
                'https://safe.test/a.png',
              ]),
            },
          ],
        },
      }),
    );
    final result = await repository.search();
    expect(result.records.single.evidence.single.host, 'safe.test');
    expect(result.records.single.createdAt, isNotNull);
    expect(http.requests.single.data, {
      'complaintUserID': 'alice',
      'pageNumber': 1,
      'showNumber': 20,
    });
    owner = 'bob';
    await expectLater(
      repository.search(),
      throwsA(
        isA<AppFailure>().having(
          (e) => e.kind,
          'kind',
          FailureKind.invalidResponse,
        ),
      ),
    );
  });

  test(
    'late old-account reply is cancelled without changing the newer account',
    () async {
      final entered = Completer<void>(), release = Completer<ResponseBody>();
      respond = (_) {
        entered.complete();
        return release.future;
      };
      final pending = submit();
      final rejected = expectLater(pending, throwsA(isA<AppFailure>()));
      await entered.future;
      await session.signOut(revoke: false);
      await session.signIn((_) async => credentials('bob'));
      release.complete(
        jsonResponse('{"errCode":0,"data":{"complaintID":"receipt"}}'),
      );
      await rejected;
      expect(session.state.credentials?.userId, 'bob');
      expect(http.requests.length, 1);
    },
  );

  Future<void> show(WidgetTester tester) => tester.pumpWidget(
    ProviderScope(
      overrides: [complaintRepositoryProvider.overrideWithValue(repository)],
      child: MaterialApp(
        builder: (context, child) => MediaQuery(
          data: MediaQuery.of(context)
              .copyWith(textScaler: const TextScaler.linear(1.6)),
          child: child!,
        ),
        home: const ComplaintPage(
          target: ComplaintTarget.user,
          targetId: 'target',
          targetName: '测试用户',
        ),
      ),
    ),
  );

  testWidgets(
    'small large-text form requires a reason and keeps draft without dispatch',
    (tester) async {
      tester.view.physicalSize = const Size(360, 640);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      await show(tester);
      await tester.enterText(find.byType(TextField), '尚未提交的说明');
      await tester.ensureVisible(find.text('提交投诉'));
      await tester.tap(find.text('提交投诉'));
      await tester.pumpAndSettle();
      expect(find.text('请选择投诉原因'), findsOneWidget);
      expect(find.text('尚未提交的说明'), findsOneWidget);
      expect(http.requests, isEmpty);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'unknown commit leaves draft, disables repeat submission and offers own receipts',
    (tester) async {
      respond = (request) async => throw DioException(
        requestOptions: request,
        type: DioExceptionType.connectionError,
      );
      await show(tester);
      await tester.tap(find.byType(DropdownButtonFormField<String>));
      await tester.pumpAndSettle();
      await tester.tap(find.text('其他').last);
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextField), '保留这段说明');
      await tester.ensureVisible(find.text('提交投诉'));
      final submitButton = tester.widget<FilledButton>(
        find.widgetWithText(FilledButton, '提交投诉'),
      );
      await tester.runAsync(() async {
        await (submitButton.onPressed as Future<void> Function())();
      });
      await tester.pumpAndSettle();
      expect(find.textContaining('暂未确认提交结果'), findsOneWidget);
      expect(
        tester
            .widget<FilledButton>(find.widgetWithText(FilledButton, '提交投诉'))
            .onPressed,
        isNull,
      );
      expect(find.text('保留这段说明'), findsOneWidget);
      await tester.drag(find.byType(Scrollable).first, const Offset(0, -300));
      await tester.pumpAndSettle();
      expect(find.text('查看我的投诉'), findsOneWidget);
      expect(find.text('查询本次提交结果'), findsOneWidget);
      expect(find.text('重试本次提交'), findsNothing);
      expect(
        http.requests.where((r) => r.uri.path.endsWith('/submit')).length,
        1,
      );
      expect(http.requests.length, 2);
      final key = (http.requests.first.data as Map)['clientSubmissionID'];
      late Completer<ResponseBody> queryReply;
      final recover = tester.widget<OutlinedButton>(
        find.widgetWithText(OutlinedButton, '查询本次提交结果'),
      );
      late Future<void> query;
      await tester.runAsync(() async {
        final queryEntered = Completer<void>();
        queryReply = Completer<ResponseBody>();
        respond = (_) {
          queryEntered.complete();
          return queryReply.future;
        };
        query = (recover.onPressed as Future<void> Function())();
        await queryEntered.future;
      });
      await tester.pump();
      expect(find.text('正在查询提交结果…'), findsOneWidget);
      expect(find.text('正在准备或上传图片…'), findsNothing);
      await tester.runAsync(() async {
        queryReply.complete(
          jsonResponse('{"errCode":0,"data":{"found":false}}'),
        );
        await query;
      });
      await tester.pumpAndSettle();
      expect(find.textContaining('原请求仍可能在处理中'), findsOneWidget);
      expect((http.requests.last.data as Map)['clientSubmissionID'], key);
      expect(
        http.requests.where((r) => r.uri.path.endsWith('/submit')).length,
        1,
      );
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'verified durable receipt permits only the frozen identical submission retry',
    (tester) async {
      respond = (request) async {
        if (request.uri.path.endsWith('/submit')) {
          throw DioException(
            requestOptions: request,
            type: DioExceptionType.connectionError,
          );
        }
        return jsonResponse(
          '{"errCode":0,"data":{"found":false,"replaySafe":true}}',
        );
      };
      await show(tester);
      await tester.tap(find.byType(DropdownButtonFormField<String>));
      await tester.pumpAndSettle();
      await tester.tap(find.text('其他').last);
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextField), '保留同一投诉内容');
      await tester.scrollUntilVisible(
        find.text('提交投诉'),
        200,
        scrollable: find.byType(Scrollable).first,
      );
      final submitButton = tester.widget<FilledButton>(
        find.widgetWithText(FilledButton, '提交投诉'),
      );
      await tester.runAsync(() async {
        await (submitButton.onPressed as Future<void> Function())();
      });
      await tester.pumpAndSettle();
      expect(repository.canReplay, isTrue);
      expect(tester.widget<TextField>(find.byType(TextField)).enabled, isFalse);
      await tester.scrollUntilVisible(
        find.text('重试本次提交'),
        200,
        scrollable: find.byType(Scrollable).first,
      );
      final retry = tester.widget<OutlinedButton>(
        find.widgetWithText(OutlinedButton, '重试本次提交'),
      );
      await tester.runAsync(() async {
        await (retry.onPressed as Future<void> Function())();
      });
      await tester.pumpAndSettle();
      final submits = http.requests
          .where((r) => r.uri.path.endsWith('/submit'))
          .toList();
      expect(submits.length, 2);
      expect(submits.last.data, submits.first.data);
      expect((submits.last.data as Map)['description'], '保留同一投诉内容');
      expect(http.requests.length, 4);
      expect(find.textContaining('暂未确认提交结果'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}
