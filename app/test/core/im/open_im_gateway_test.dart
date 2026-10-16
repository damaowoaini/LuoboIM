import 'dart:async';
import 'dart:io';

import 'package:flutter/services.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/im/im_event.dart';
import 'package:imapp/core/im/open_im_gateway.dart';
import 'package:imapp/core/tenant/session_credentials.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('luobo_gateway_test');
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  late Directory support;
  late OpenImGateway gateway;
  IMManager? manager;
  var consent = true;
  var creations = 0;
  var currentUser = '';
  final calls = <MethodCall>[];
  Future<dynamic> Function(MethodCall call)? overrideCall;

  SessionCredentials credentials(String user) => SessionCredentials(
    userId: user,
    tenantId: 'tenant-a',
    tenantName: '测试租户',
    domains: ['im.example.com'],
    chatToken: 'chat-private',
    imToken: 'im-private',
  );

  OpenImGateway create({
    Duration timeout = const Duration(seconds: 1),
    void Function(int)? expired,
  }) => OpenImGateway(
    hasPrivacyConsent: () => consent,
    managerFactory: () {
      creations++;
      return manager = IMManager(channel);
    },
    supportDirectory: () async => support,
    platformId: 2,
    operationTimeout: timeout,
    onSessionExpired: expired,
  );

  Future<void> callback(String method, Map<String, dynamic> arguments) async {
    final done = Completer<void>();
    // Test the same encoded callback boundary used by Android and iOS.
    // ignore: deprecated_member_use
    messenger.handlePlatformMessage(
      channel.name,
      const StandardMethodCodec().encodeMethodCall(
        MethodCall(method, arguments),
      ),
      (_) => done.complete(),
    );
    await done.future;
    await Future<void>.delayed(Duration.zero);
  }

  Matcher failure(FailureKind kind) =>
      isA<AppFailure>().having((e) => e.kind, 'kind', kind);

  setUp(() async {
    support = await Directory.systemTemp.createTemp('luobo-gateway-test-');
    consent = true;
    creations = 0;
    currentUser = '';
    manager = null;
    calls.clear();
    overrideCall = null;
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      if (overrideCall != null) return overrideCall!(call);
      if (call.method == 'initSDK') return true;
      if (call.method == 'login') {
        currentUser = call.arguments['userID'] as String;
      }
      if (call.method == 'getSelfUserInfo') return '{"userID":"$currentUser"}';
      return null;
    });
    gateway = create();
  });

  tearDown(() async {
    overrideCall = null;
    await gateway.dispose();
    messenger.setMockMethodCallHandler(channel, null);
    await support.delete(recursive: true);
  });

  test(
    'privacy gate and idle exit never construct or invoke the SDK',
    () async {
      consent = false;
      await gateway.disconnect();
      await expectLater(
        gateway.connect(credentials('alice'), 1),
        throwsA(failure(FailureKind.configuration)),
      );
      expect(creations, 0);
      expect(calls, isEmpty);
      expect(await support.list().isEmpty, isTrue);
    },
  );

  test('account directories and native login use tenant endpoints and private logs disabled', () async {
    final alice = credentials('alice');
    final bob = credentials('bob');
    await gateway.connect(alice, 1);
    await gateway.connect(bob, 2);
    final initializations = calls
        .where((call) => call.method == 'initSDK')
        .toList();
    expect(initializations, hasLength(2));
    final first = initializations[0].arguments as Map;
    final second = initializations[1].arguments as Map;
    expect(first['dataDir'], '${support.path}/openim/${alice.accountKey}');
    expect(second['dataDir'], '${support.path}/openim/${bob.accountKey}');
    expect(second['dataDir'], isNot(first['dataDir']));
    expect(first['apiAddr'], 'https://im.example.com/api/');
    expect(first['wsAddr'], 'wss://im.example.com/msg_gateway');
    expect(first['logLevel'], 1);
    expect(first['isLogStandardOutput'], isFalse);
    expect(creations, 1);
    expect(
      calls.where((call) => call.method == 'login').last.arguments['token'],
      'im-private',
    );
  });

  test(
    'login failures clear the native session even when logout itself fails',
    () async {
      overrideCall = (call) async {
        if (call.method == 'initSDK') return true;
        if (call.method == 'login' || call.method == 'logout') {
          throw PlatformException(
            code: '1501',
            message: 'private token or body',
          );
        }
        return null;
      };
      await expectLater(
        gateway.connect(credentials('alice'), 8),
        throwsA(failure(FailureKind.sessionExpired)),
      );
      expect(
        calls.map((call) => call.method),
        containsAllInOrder(['login', 'logout', 'unInitSDK']),
      );
      expect(manager!.isLogined, isFalse);
      expect(manager!.userID, isEmpty);
      await gateway.disconnect();
      expect(calls.where((call) => call.method == 'unInitSDK'), hasLength(1));
    },
  );

  for (final sample in <(String, FailureKind)>[
    ('lookup private.invalid: no such host', FailureKind.network),
    ('dial tcp: connection refused', FailureKind.network),
    ('request failed: context deadline exceeded', FailureKind.timeout),
    ('TLS handshake timeout', FailureKind.timeout),
    (
      'x509: certificate signed by unknown authority',
      FailureKind.configuration,
    ),
    ('database internal failure', FailureKind.unknown),
  ]) {
    test('generic native failure safely classifies ${sample.$2.name}: '
        '${sample.$1.split(':').first}', () async {
      overrideCall = (call) async {
        if (call.method == 'initSDK') return true;
        if (call.method == 'login') {
          throw PlatformException(
            code: '10005',
            message: '${sample.$1}; synthetic-private-token-and-body',
          );
        }
        return null;
      };
      await expectLater(
        gateway.connect(credentials('alice'), 9),
        throwsA(
          isA<AppFailure>()
              .having((error) => error.kind, 'safe kind', sample.$2)
              .having((error) => error.code, 'native code retained', 10005)
              .having(
                (error) => '${error.message} ${error.toString()}',
                'presentation excludes native text',
                isNot(contains('synthetic-private')),
              ),
        ),
      );
      expect(calls.last.method, 'unInitSDK');
    });
  }

  test(
    'connection callback exposes a safe failure and no native body',
    () async {
      final events = <ImEvent>[];
      final subscription = gateway.events.listen(events.add);
      await gateway.connect(credentials('alice'), 9);
      await callback(ListenerType.connectListener, {
        'type': 'onConnectFailed',
      'errCode': 10005,
      'errMsg': 'lookup private.invalid: no such host; private-token',
      });
      expect(events.last.kind, ImEventKind.connectionFailed);
      expect(events.last.failure?.kind, FailureKind.network);
      expect(events.last.failure?.code, 10005);
      expect(events.last.failure.toString(), isNot(contains('private-token')));
      await subscription.cancel();
    },
  );

  test('repeated exit is idempotent and old listener generations cannot enter a new account', () async {
    final events = <ImEvent>[];
    final subscription = gateway.events.listen(events.add);
    await gateway.connect(credentials('alice'), 10);
    final oldListener = manager!.conversationManager.listener;
    await callback(ListenerType.conversationListener, {
      'type': 'onTotalUnreadMessageCountChanged',
      'data': 4,
    });
    await gateway.disconnect();
    await gateway.disconnect();
    expect(calls.where((call) => call.method == 'unInitSDK'), hasLength(1));
    oldListener.totalUnreadMessageCountChanged(99);
    await gateway.connect(credentials('bob'), 11);
    oldListener.totalUnreadMessageCountChanged(98);
    await callback(ListenerType.conversationListener, {
      'type': 'onTotalUnreadMessageCountChanged',
      'data': 5,
    });
    expect(events.map((event) => (event.generation, event.count)), [
      (10, 4),
      (11, 5),
    ]);
    await subscription.cancel();
  });

  test(
    'native expiry reports its generation once and stops accepting callbacks',
    () async {
      final expired = <int>[];
      await gateway.dispose();
      gateway = create(expired: expired.add);
      final events = <ImEvent>[];
      final subscription = gateway.events.listen(events.add);
      await gateway.connect(credentials('alice'), 12);
      await callback(ListenerType.connectListener, {
        'type': 'onUserTokenExpired',
      });
      await callback(ListenerType.connectListener, {'type': 'onKickedOffline'});
      await callback(ListenerType.conversationListener, {
        'type': 'onTotalUnreadMessageCountChanged',
        'data': 99,
      });
      expect(expired, [12]);
      expect(events.map((event) => event.kind), [ImEventKind.tokenExpired]);
      await subscription.cancel();
    },
  );

  test(
    'superseding an in-flight initialization cancels before native login',
    () async {
      final initialized = Completer<bool>();
      final started = Completer<void>();
      overrideCall = (call) async {
        if (call.method == 'initSDK') {
          started.complete();
          return initialized.future;
        }
        return null;
      };
      final connecting = gateway.connect(credentials('alice'), 15);
      final expectation = expectLater(
        connecting,
        throwsA(failure(FailureKind.cancelled)),
      );
      await started.future;
      final disconnecting = gateway.disconnect();
      initialized.complete(true);
      await expectation;
      await disconnecting;
      expect(calls.any((call) => call.method == 'login'), isFalse);
      expect(calls.where((call) => call.method == 'unInitSDK'), hasLength(1));
    },
  );

  test('a timed-out native login is quarantined until it settles and is torn down again', () async {
    await gateway.dispose();
    gateway = create(timeout: const Duration(milliseconds: 40));
    final lateLogin = Completer<void>();
    var loginCount = 0;
    overrideCall = (call) async {
      if (call.method == 'initSDK') return true;
      if (call.method == 'login') {
        currentUser = call.arguments['userID'] as String;
        if (++loginCount == 1) {
          await lateLogin.future;
        }
      }
      if (call.method == 'getSelfUserInfo') return '{"userID":"$currentUser"}';
      return null;
    };
    await expectLater(
      gateway.connect(credentials('alice'), 20),
      throwsA(failure(FailureKind.timeout)),
    );
    await expectLater(
      gateway.connect(credentials('bob'), 21),
      throwsA(failure(FailureKind.timeout)),
    );
    expect(loginCount, 1);
    lateLogin.complete();
    await Future<void>.delayed(Duration.zero);
    await gateway.connect(credentials('bob'), 22);
    expect(loginCount, 2);
    expect(manager!.userID, 'bob');
    final methods = calls.map((call) => call.method).toList();
    final lastLogin = methods.lastIndexOf('login');
    expect(
      methods.sublist(methods.indexOf('getSelfUserInfo') + 1, lastLogin),
      contains('unInitSDK'),
    );
  });
}
