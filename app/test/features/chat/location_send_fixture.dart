import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart' show debugPrintSynchronously;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/data/native_location_once.dart';
import 'package:imapp/features/chat/domain/location_pick.dart';
import 'package:imapp/features/chat/presentation/chat_controller.dart';
import 'package:imapp/features/chat/presentation/chat_location_picker_page.dart';
import 'package:imapp/features/chat/presentation/chat_media_actions.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';
import 'chat_location_fixture.dart';

final class FakeLocationOnce implements NativeLocationOnce {
  final requests = <String>[];
  final cancellations = <String>[];
  Future<LocationFix> Function(String)? reply;
  LocationFix fix(String id) => LocationFix.parse(
    {
      'requestId': id,
      'longitude': 121.4,
      'latitude': 31.1,
      'accuracyMeters': 30.0,
      'timestampMillis': DateTime.now().millisecondsSinceEpoch,
      'coordinateSystem': 'wgs84',
      'approximate': true,
    },
    id,
    DateTime.now(),
  );
  @override
  Future<LocationFix> request(String id) async {
    requests.add(id);
    return reply == null ? fix(id) : await reply!(id);
  }

  @override
  Future<void> cancel(String id) async {
    cancellations.add(id);
  }
}

final class FakeLocationMap implements LocationMapHost {
  FakeLocationMap({
    required this.nonce,
    required this.candidate,
    required this.status,
    required this.failed,
    required this.retired,
    required this.autoReady,
  });
  final String nonce;
  final ValueChanged<String> candidate, status;
  final VoidCallback failed, retired;
  final bool autoReady;
  bool disposed = false;
  int starts = 0;
  final commands = <(String, Map<String, Object>)>[];
  @override
  Widget build(BuildContext context) => const Center(child: Text('受控地图'));
  @override
  Future<void> start() async {
    ++starts;
    if (autoReady) {
      ready();
    }
  }

  void ready() => status(
    jsonEncode({'requestNonce': nonce, 'sequence': 0, 'state': 'ready'}),
  );
  // Deliberately deliver even after dispose: it is the production Dart view,
  // not this mock, that must discard old transport callbacks.
  void pick({
    int sequence = 1,
    String name = '集合地点',
    double lng = 121.5,
    double lat = 31.2,
  }) => candidate(
    jsonEncode({
      'requestNonce': nonce,
      'sequence': sequence,
      'lng': lng,
      'lat': lat,
      'name': name,
    }),
  );
  @override
  Future<bool> command(String method, Map<String, Object> arguments) async {
    commands.add((method, Map.of(arguments)));
    return true;
  }

  @override
  Future<void> dispose() async {
    disposed = true;
  }
}

final class LocationSendFixture {
  final base = ChatLocationFixture();
  SdkHarness get sdk => base.sdk;
  final native = FakeLocationOnce();
  final hosts = <FakeLocationMap>[];
  FakeLocationMap get host => hosts.last;
  final releases = <VoidCallback>[];
  late ChatController controller;
  bool autoReady = true, failSend = false, invalidCreate = false;
  Future<void> Function()? beforeCreate, beforeSend;
  int creates = 0, sends = 0;
  String cid = ChatLocationFixture.cid;
  late final ValueNotifier<String> sourceCid;
  LocationPick pick([int sequence = 1]) => LocationPick.parse(
    jsonEncode({
      'requestNonce': 'a' * 64,
      'sequence': sequence,
      'lng': 121.5,
      'lat': 31.2,
      'name': '集合地点',
    }),
    nonce: 'a' * 64,
    afterSequence: 0,
  );

  Completer<void> defer() {
    final result = Completer<void>();
    releases.add(() {
      if (!result.isCompleted) {
        result.complete();
      }
    });
    return result;
  }

  Future<void> start({bool group = false}) async {
    cid = group ? 'group-conversation-a' : ChatLocationFixture.cid;
    sdk.conversations.add(SdkHarness.conversation(id: cid, group: group));
    sdk.messages.add(SdkHarness.message(id: 'existing', text: '原有聊天'));
    await sdk.start();
    base.started = true;
    sdk.overrideCall = (call) async {
      if (call.method == 'createLocationMessage') {
        ++creates;
        await beforeCreate?.call();
        final args = call.arguments as Map;
        return jsonEncode({
          ...SdkHarness.message(
            id: 'location-created-$creates',
            text: '',
            sender: 'alice',
            status: 1,
          ),
          'contentType': invalidCreate ? 101 : 109,
          'locationElem': {
            'latitude': args['latitude'],
            'longitude': args['longitude'],
            'description': args['description'],
          },
        });
      }
      if (call.method == 'sendMessage') {
        ++sends;
        await beforeSend?.call();
        if (failSend) {
          throw PlatformException(code: '999');
        }
      }
      return sdk.respond(call);
    };
  }

  Future<void> startRepository({bool group = false}) async {
    await start(group: group);
    controller = ChatController(
      ChatRepository(
        gateway: sdk.gateway,
        session: sdk.session,
        conversationId: cid,
      ),
    );
    await controller.start();
  }

  LocationPickerDependencies dependencies({bool configured = true}) =>
      LocationPickerDependencies(
        config: configured
            ? LocationPickerConfig.parse(
                'https://map.example.com/picker',
                '116.4',
                '39.9',
              )
            : null,
        native: native,
        hostFactory:
            ({
              required config,
              required nonce,
              required candidate,
              required status,
              required failed,
              required retired,
            }) {
              final host = FakeLocationMap(
                nonce: nonce,
                candidate: candidate,
                status: status,
                failed: failed,
                retired: retired,
                autoReady: autoReady,
              );
              hosts.add(host);
              return host;
            },
      );
  Future<void> mount(
    WidgetTester tester, {
    bool group = false,
    bool configured = true,
    bool small = false,
  }) async {
    await tester.runAsync(() => start(group: group));
    if (small) {
      tester.view.physicalSize = const Size(360, 640);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
    }
    sourceCid = ValueNotifier(cid);
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          appRuntimeProvider.overrideWith((_) => sdk.runtime),
          imGatewayProvider.overrideWithValue(sdk.gateway),
          chatReadingStoreProvider.overrideWithValue(base.reading),
          chatBackgroundStoreProvider.overrideWithValue(base.backgrounds),
        ],
        child: MaterialApp(
          navigatorKey: base.navigator,
          theme: await captureTheme(tester),
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(context)
                .copyWith(textScaler: TextScaler.linear(small ? 1.6 : 1)),
            child: child!,
          ),
          home: ValueListenableBuilder<String>(
            valueListenable: sourceCid,
            builder: (_, currentCid, _) => ChatPage(
              conversationId: currentCid,
              locationPicker: dependencies(configured: configured),
            ),
          ),
        ),
      ),
    );
    await base.ready(tester, find.byTooltip('发送附件'));
    await base.until(
      tester,
      () =>
          tester
              .widget<ChatMediaActions>(find.byType(ChatMediaActions))
              .controller
              .conversation !=
          null,
    );
    controller = tester
        .widget<ChatMediaActions>(find.byType(ChatMediaActions))
        .controller;
    await _readyAttachment(tester);
  }

  Future<Finder> _readyAttachment(WidgetTester tester) async {
    final button = find.descendant(
      of: find.byType(ChatMediaActions),
      matching: find.byType(IconButton),
    );
    await base.until(
      tester,
      () =>
          button.evaluate().length == 1 &&
          tester.widget<IconButton>(button).onPressed != null &&
          button.hitTestable().evaluate().length == 1,
    );
    await base.ready(tester, button);
    return button;
  }

  Future<void> open(WidgetTester tester) async {
    final button = await _readyAttachment(tester);
    await tester.tap(button.hitTestable());
    await base.until(tester, () => find.text('位置').evaluate().length == 1);
    await tester.ensureVisible(find.text('位置'));
    await base.frame(tester);
    await base.ready(tester, find.text('位置'));
    await tester.tap(find.text('位置').hitTestable());
    await base.ready(tester, find.byType(ChatLocationPickerPage));
    if (hosts.isNotEmpty) {
      await base.until(tester, () => host.starts == 1);
    }
  }

  Future<void> select(
    WidgetTester tester, {
    int sequence = 1,
    String name = '集合地点',
  }) async {
    host.pick(sequence: sequence, name: name);
    await base.frame(tester);
    await base.until(tester, () => find.text(name).evaluate().length == 1);
  }

  Future<void> tap(WidgetTester tester, String text) async {
    final target = find.text(text);
    await tester.ensureVisible(target);
    await base.ready(tester, target);
    await tester.tap(target.hitTestable());
    await base.frame(tester);
  }

  Future<void> close(WidgetTester tester) async {
    for (final release in releases) {
      release();
    }
    try {
      debugPrintSynchronously('location_send_cleanup sdk.begin');
      if (base.started) {
        await base.actual(
          tester,
          () => sdk.dispose().timeout(const Duration(seconds: 20)),
        );
      }
      debugPrintSynchronously('location_send_cleanup sdk.end');
    } finally {
      debugPrintSynchronously('location_send_cleanup unmount.begin');
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pumpAndSettle();
      base.reading.dispose();
      base.backgrounds.dispose();
      sourceCid.dispose();
      debugPrintSynchronously('location_send_cleanup unmount.end');
    }
  }

  Future<void> closeRepository() async {
    for (final release in releases) {
      release();
    }
    controller.dispose();
    await sdk.dispose();
    base.reading.dispose();
    base.backgrounds.dispose();
  }
}
