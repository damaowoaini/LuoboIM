import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/features/contacts/contacts_providers.dart';
import 'package:imapp/features/contacts/data/contacts_repository.dart';
import 'package:imapp/features/contacts/domain/contact.dart'
    show ContactLookupMode;
import 'package:imapp/features/contacts/presentation/contact_search_page.dart';

import '../../core/im/sdk_harness.dart';
import '../../core/im/capture_theme.dart';
import '../../support/http_adapter.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const target = '7031156314';
  const other = '8488972824';
  late SdkHarness harness;
  late TenantApi api;
  late ContactsRepository repository;
  late TestHttpAdapter http;
  late Future<ResponseBody> Function(RequestOptions) handler;
  var duplicateSdkIdentity = false;
  var started = false;
  late GlobalKey captureKey;

  Map<String, Object?> row({String id = target, Object? policy = 1}) => {
    'userID': id,
    'nickname': id == target ? '目标本人' : '另一账号',
    'allowAddFriend': ?policy,
  };
  ResponseBody response(List<Object?> users, {int? total}) => jsonResponse(
    jsonEncode({
      'errCode': 0,
      'data': {'users': users, 'total': total ?? users.length},
    }),
  );
  setUp(() {
    harness = SdkHarness();
    started = false;
    duplicateSdkIdentity = false;
    handler = (request) async => request.uri.path.endsWith('user/find/full')
        ? response([row()])
        : response([]);
  });
  Future<void> start() async {
    await harness.start();
    started = true;
    http = TestHttpAdapter((request) => handler(request));
    api = TenantApi(
      SessionAccess(harness.session),
      dio: Dio()..httpClientAdapter = http,
    );
    repository = ContactsRepository(harness.gateway, api.access, api);
    harness.overrideCall = (call) async {
      if (call.method == 'getUsersInfo') {
        return jsonEncode([
          {'publicInfo': row()},
          if (duplicateSdkIdentity) {'publicInfo': row()},
        ]);
      }
      return harness.respond(call);
    };
  }

  tearDown(() async {
    if (started) {
      api.close();
      await harness.dispose();
    }
  });
  Matcher failure([FailureKind? kind]) => throwsA(
    kind == null
        ? isA<AppFailure>()
        : isA<AppFailure>().having((e) => e.kind, 'kind', kind),
  );

  test(
    'an exact ordinary profile with explicit allow policy remains usable',
    () async {
      await start();
      final profile = await repository.profile(target);
      expect(profile.id, target);
      expect(profile.canRequestFriend, isTrue);
      expect(http.requests.single.uri.path, '/chat/user/find/full');
    },
  );
  test(
    'business profile failure cannot silently grant friend permission',
    () async {
      await start();
      handler = (_) async => jsonResponse('{"errCode":500}');
      await expectLater(repository.profile(target), failure());
    },
  );
  test(
    'another business identity cannot authorize the requested profile',
    () async {
      await start();
      handler = (_) async => response([row(id: other)]);
      await expectLater(
        repository.profile(target),
        failure(FailureKind.invalidResponse),
      );
    },
  );
  test(
    'missing friend policy cannot silently grant friend permission',
    () async {
      await start();
      handler = (_) async => response([row(policy: null)]);
      await expectLater(
        repository.profile(target),
        failure(FailureKind.invalidResponse),
      );
    },
  );
  test(
    'duplicate SDK identity is rejected before business authorization',
    () async {
      await start();
      duplicateSdkIdentity = true;
      await expectLater(
        repository.profile(target),
        failure(FailureKind.invalidResponse),
      );
      expect(http.requests, isEmpty);
    },
  );
  test(
    'friend submission rechecks current target policy before native mutation',
    () async {
      await start();
      expect((await repository.profile(target)).canRequestFriend, isTrue);
      handler = (_) async => response([row(policy: 2)]);
      await expectLater(
        repository.requestFriend(target, '测试申请'),
        failure(FailureKind.permissionDenied),
      );
      expect(
        harness.calls.where((call) => call.method == 'addFriend'),
        isEmpty,
      );
    },
  );
  test(
    'a permitted friend request still dispatches to the exact account',
    () async {
      await start();
      await repository.requestFriend(target, '测试申请');
      final call = harness.calls.singleWhere(
        (call) => call.method == 'addFriend',
      );
      expect(call.arguments['toUserID'], target);
      expect(http.requests.single.uri.path, '/chat/user/find/full');
    },
  );
  test(
    'exact ID response with a substituted account does not fall back to phone',
    () async {
      await start();
      handler = (_) async => response([row(id: other)]);
      await expectLater(
        repository.lookupUser(target, ContactLookupMode.userId),
        failure(FailureKind.invalidResponse),
      );
      expect(http.requests, hasLength(1));
      expect(http.requests.single.uri.path, '/chat/user/find/full');
    },
  );
  test('explicit phone mode resolves only its unique phone owner', () async {
    await start();
    handler = (_) async => response([row(id: other)]);
    final result = await repository.lookupUser(
      target,
      ContactLookupMode.phoneNumber,
    );
    expect(result.items.single.id, other);
    expect(http.requests.single.uri.path, '/chat/user/search/full');
    expect(http.requests.single.data, {
      'keyword': target,
      'pagination': {'pageNumber': 1, 'showNumber': 2},
    });
  });
  test('ambiguous phone results cannot select their first account', () async {
    await start();
    handler = (_) async => response([row(), row(id: other)]);
    await expectLater(
      repository.lookupUser(target, ContactLookupMode.phoneNumber),
      failure(FailureKind.invalidResponse),
    );
  });
  test('an exact ID miss is empty and never falls back to phone', () async {
    await start();
    handler = (_) async => response([]);
    expect(
      (await repository.lookupUser(target, ContactLookupMode.userId)).items,
      isEmpty,
    );
    expect(http.requests, hasLength(1));
    expect(http.requests.single.uri.path, '/chat/user/find/full');
  });

  Future<void> mount(WidgetTester tester) async {
    await tester.runAsync(start);
    tester.view.physicalSize = const Size(360, 640);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final theme = await captureTheme(tester);
    captureKey = GlobalKey();
    await tester.pumpWidget(
      ProviderScope(
        overrides: [contactsRepositoryProvider.overrideWithValue(repository)],
        child: RepaintBoundary(
          key: captureKey,
          child: MaterialApp(
            theme: theme,
            debugShowCheckedModeBanner: false,
            builder: (context, child) => MediaQuery(
              data: MediaQuery.of(context)
                  .copyWith(textScaler: TextScaler.linear(1.6)),
              child: child!,
            ),
            home: const ContactSearchPage(),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  Future<void> waitForHttp(WidgetTester tester) async {
    for (var i = 0; i < 20 && http.requests.isEmpty; i++) {
      await tester.runAsync(
        () => Future<void>.delayed(const Duration(milliseconds: 10)),
      );
      await tester.pump();
    }
    expect(http.requests, hasLength(1));
  }

  Future<void> search(WidgetTester tester) async {
    await tester.enterText(find.byType(TextField), target);
    // Invoke the rendered button's production callback; this suite checks
    // asynchronous query ownership, not platform pointer hit testing.
    await tester.runAsync(() async {
      tester.widget<FilledButton>(find.byType(FilledButton)).onPressed!();
      await Future<void>.delayed(const Duration(milliseconds: 20));
    });
    await tester.pump();
    await waitForHttp(tester);
    await tester.pumpAndSettle();
  }

  Future<void> close(WidgetTester tester) async {
    await tester.runAsync(harness.dispose);
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump();
  }

  Future<void> capture(WidgetTester tester, String name) async {
    const directory = String.fromEnvironment('TEST_CAPTURE_DIR');
    if (directory.isEmpty) return;
    await tester.runAsync(() async {
      final boundary =
          captureKey.currentContext!.findRenderObject()!
              as RenderRepaintBoundary;
      final image = await boundary.toImage(pixelRatio: 2);
      final bytes = (await image.toByteData(format: ui.ImageByteFormat.png))!;
      await Directory(directory).create(recursive: true);
      await File('$directory/$name.png')
          .writeAsBytes(bytes.buffer.asUint8List());
      image.dispose();
    });
  }

  testWidgets('complete account ID resolves through exact ID lookup', (
    tester,
  ) async {
    await mount(tester);
    await search(tester);
    expect(find.text('ID：$target'), findsOneWidget);
    expect(http.requests.single.uri.path, '/chat/user/find/full');
    expect(tester.takeException(), isNull);
    await capture(tester, 'contact-id-found-360-large-text');
    await close(tester);
  });
  testWidgets('numeric account ID cannot select another phone owner', (
    tester,
  ) async {
    await mount(tester);
    handler = (request) async => request.uri.path.endsWith('user/find/full')
        ? response([row()])
        : response([row(id: other)]);
    await search(tester);
    expect(find.text('ID：$target'), findsOneWidget);
    expect(find.text('ID：$other'), findsNothing);
    await close(tester);
  });
  testWidgets('editing a pending lookup discards the previous query response', (
    tester,
  ) async {
    await mount(tester);
    final pending = Completer<ResponseBody>();
    handler = (_) => pending.future;
    await tester.enterText(find.byType(TextField), target);
    await tester.runAsync(() async {
      tester.widget<FilledButton>(find.byType(FilledButton)).onPressed!();
      await Future<void>.delayed(const Duration(milliseconds: 20));
    });
    await tester.pump();
    await waitForHttp(tester);
    await tester.enterText(find.byType(TextField), other);
    await tester.runAsync(() async {
      pending.complete(response([row()]));
      await Future<void>.delayed(const Duration(milliseconds: 20));
    });
    await tester.pumpAndSettle();
    expect(find.text('ID：$target'), findsNothing);
    expect(tester.takeException(), isNull);
    await capture(tester, 'contact-query-edited-360-large-text');
    await close(tester);
  });
}
