import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart' show debugPrintSynchronously;
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/chat/data/chat_background_store.dart';
import 'package:imapp/features/chat/data/chat_reading_preferences.dart';
import 'package:imapp/features/chat/presentation/chat_location_view.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';

final class ChatLocationFixture {
  final sdk = SdkHarness();
  final navigator = GlobalKey<NavigatorState>();
  final clock = DateTime.fromMillisecondsSinceEpoch(1700000000000);
  late final backgrounds = ChatBackgroundStore(
    supportDirectory: () async => sdk.directory,
  );
  final reading = ChatReadingPreferencesStore(
    readDouble: (_) async => 1,
    writeDouble: (_, _) async {},
  );
  bool started = false;
  bool traceActual = false;
  void Function(String)? _actualFrameTrace;
  static const cid = 'single-alice-bob';

  Map<String, dynamic> wire({
    String id = 'received-location',
    bool private = false,
    double latitude = 31.2,
    double longitude = 121.5,
    String? description,
  }) => {
    ...SdkHarness.message(id: id, text: ''),
    'contentType': MessageType.location,
    'locationElem': {
      'latitude': latitude,
      'longitude': longitude,
      'description':
          description ??
          jsonEncode({
            'name': '人民公园',
            'addr': '东门集合',
            'url': 'https://untrusted.example/static?key=never-fetch',
          }),
    },
    if (private) 'attachedInfoElem': {'isPrivateChat': true},
  };

  Future<void> frame(WidgetTester tester) async {
    final trace = _actualFrameTrace;
    trace?.call('frame.runAsync.before');
    await tester.runAsync(() async {
      await Future<void>.delayed(Duration.zero);
    });
    trace?.call('frame.runAsync.after');
    trace?.call('frame.pump.before');
    await tester.pump(const Duration(milliseconds: 20));
    trace?.call('frame.pump.after');
  }

  Future<void> until(WidgetTester tester, bool Function() ready) async {
    for (var turn = 0; turn < 500; ++turn) {
      if (ready()) {
        return;
      }
      await frame(tester);
    }
    throw TestFailure('Expected bounded actual location page/route state');
  }

  Future<void> ready(WidgetTester tester, Finder finder) async {
    await until(
      tester,
      () =>
          finder.evaluate().length == 1 &&
          finder.hitTestable().evaluate().length == 1,
    );
    final route = ModalRoute.of(tester.element(finder));
    await until(
      tester,
      () =>
          route?.isCurrent == true &&
          (route!.animation == null ||
              route.animation!.status == AnimationStatus.completed),
    );
  }

  Future<T> actual<T>(WidgetTester tester, Future<T> Function() action) async {
    T? value;
    Object? error;
    StackTrace? stack;
    var done = false;
    final tracing = traceActual;
    void note(String step) {
      if (tracing) {
        debugPrintSynchronously('chat_input_actual $step');
      }
    }

    note('enter');
    await tester.runAsync(() async {
      note('start');
      unawaited(
        Future<T>.sync(action).then<void>(
          (result) {
            value = result;
            done = true;
            note('observed.done');
          },
          onError: (Object caught, StackTrace trace) {
            error = caught;
            stack = trace;
            done = true;
            note('observed.error');
          },
        ),
      );
    });
    note('start.returned');
    final priorTrace = _actualFrameTrace;
    if (tracing) {
      var frameMarkers = 0;
      _actualFrameTrace = (step) {
        // Only the first two original frames: no added timer or polling turn.
        if (frameMarkers < 8) {
          ++frameMarkers;
          note(step);
        }
      };
    }
    try {
      note('until.enter');
      await until(tester, () => done);
      note('until.returned');
    } finally {
      if (tracing) {
        _actualFrameTrace = priorTrace;
      }
    }
    if (error != null) {
      Error.throwWithStackTrace(error!, stack!);
    }
    note('return');
    return value as T;
  }

  Future<void> mount(
    WidgetTester tester, {
    Map<String, dynamic>? message,
    bool merge = false,
    bool small = false,
  }) async {
    sdk.conversations.add(SdkHarness.conversation());
    final raw = message ?? wire();
    sdk.messages.add(
      merge
          ? {
              ...SdkHarness.message(id: 'location-root', text: ''),
              'contentType': MessageType.merger,
              'mergeElem': {
                'multiMessage': [raw],
              },
            }
          : raw,
    );
    await tester.runAsync(sdk.start);
    started = true;
    if (small) {
      tester.view.physicalSize = const Size(360, 640);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
    }
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          appRuntimeProvider.overrideWith((_) => sdk.runtime),
          imGatewayProvider.overrideWithValue(sdk.gateway),
          chatReadingStoreProvider.overrideWithValue(reading),
          chatBackgroundStoreProvider.overrideWithValue(backgrounds),
        ],
        child: MaterialApp(
          navigatorKey: navigator,
          theme: await captureTheme(tester),
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(context)
                .copyWith(textScaler: TextScaler.linear(small ? 1.6 : 1)),
            child: child!,
          ),
          home: ChatPage(conversationId: cid, now: () => clock),
        ),
      ),
    );
    if (merge) {
      await ready(tester, find.textContaining('聊天记录 ·'));
      await tester.tap(find.textContaining('聊天记录 ·').hitTestable());
    }
    await ready(tester, find.byType(ChatLocationView));
  }

  VoidCallback cardCallback(WidgetTester tester) => tester
      .widget<InkWell>(
        find.descendant(
          of: find.byType(ChatLocationView),
          matching: find.byType(InkWell),
        ),
      )
      .onTap!;
  Future<void> open(WidgetTester tester) async {
    await tester.tap(find.text('位置 · 查看详情').hitTestable());
    await ready(tester, find.byType(ChatLocationDetail));
  }

  Future<void> coverAndReturn(WidgetTester tester) async {
    final route = MaterialPageRoute<void>(
      builder: (_) => const Scaffold(body: Text('外来页面')),
    );
    unawaited(navigator.currentState!.push(route));
    await ready(tester, find.text('外来页面'));
    navigator.currentState!.pop();
    var completed = false;
    unawaited(
      route.completed.then<void>((_) {
        completed = true;
      }),
    );
    await until(tester, () => completed);
    await frame(tester);
  }

  Future<void> changeOwner({
    String user = 'alice',
    String tenant = 'other-tenant',
    String issuer = 'https://other-issuer.example/',
  }) async {
    await sdk.session.signOut();
    sdk.userId = user;
    final c = sdk.credentials;
    await sdk.session.signIn(
      (_) async => SessionCredentials(
        userId: user,
        tenantId: tenant,
        tenantName: c.tenantName,
        domains: c.domains,
        chatToken: c.chatToken,
        imToken: c.imToken,
        issuer: issuer,
      ),
    );
  }

  Future<void> close(WidgetTester tester) async {
    try {
      debugPrintSynchronously('chat_location_cleanup sdk.begin');
      if (started) {
        await actual(
          tester,
          () => sdk.dispose().timeout(const Duration(seconds: 20)),
        );
      }
      debugPrintSynchronously('chat_location_cleanup sdk.end');
    } finally {
      debugPrintSynchronously('chat_location_cleanup unmount.begin');
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pumpAndSettle();
      reading.dispose();
      backgrounds.dispose();
      debugPrintSynchronously('chat_location_cleanup unmount.end');
    }
  }
}
