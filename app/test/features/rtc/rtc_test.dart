import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:livekit_client/livekit_client.dart' as lk;
import 'package:logging/logging.dart' as logging;
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/rtc/data/im_rtc_transport.dart';
import 'package:imapp/features/rtc/data/livekit_media_session.dart';
import 'package:imapp/features/rtc/domain/rtc_call.dart';
import 'package:imapp/features/rtc/domain/rtc_coordinator.dart';

import '../../core/im/sdk_harness.dart';

String token(String room, {String sub = 'alice', int? expires}) =>
    'header.${base64Url.encode(utf8.encode(jsonEncode({
      'sub': sub,
      'exp': expires ?? DateTime.now().millisecondsSinceEpoch ~/ 1000 + 300,
      'video': {'room': room, 'roomJoin': true},
    }))).replaceAll('=', '')}.signature';

class Media implements RtcMediaSession {
  final changed = StreamController<void>.broadcast();
  final states = StreamController<RtcMediaConnection>.broadcast();
  int closed = 0, captures = 0;
  bool failMicrophone = false;
  @override
  bool microphone = false, camera = false, speaker = false;
  @override
  List<RtcPeer> get peers => [];
  @override
  Stream<void> get changes => changed.stream;
  @override
  Stream<RtcMediaConnection> get connection => states.stream;
  @override
  Future<void> setMicrophone(bool value) async {
    if (value && failMicrophone) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    microphone = value;
    if (value) captures++;
  }

  @override
  Future<void> setCamera(bool value) async {
    camera = value;
    if (value) captures++;
  }

  @override
  Future<void> setSpeaker(bool value) async {
    speaker = value;
  }

  @override
  Future<void> switchCamera() async {}
  @override
  Future<void> close() async {
    closed++;
    microphone = camera = false;
  }
}

class Permissions implements RtcPermissionGate {
  Completer<bool>? pending;
  bool granted = true;
  int calls = 0;
  Object? failure;
  @override
  Future<bool> request({required bool video}) {
    calls++;
    if (failure != null) return Future.error(failure!);
    return pending?.future ?? Future.value(granted);
  }

  @override
  Future<bool> openSettings() async => true;
}

class Transport implements RtcCallTransport {
  Transport(this.generation);
  final events = StreamController<RtcSignal>.broadcast(sync: true);
  final media = Media();
  final ended = <RtcEndAction>[];
  Completer<RtcMediaSession>? pending;
  int joins = 0;
  @override
  final int generation;
  @override
  String get selfId => 'alice';
  @override
  bool current = true;
  @override
  Stream<RtcSignal> get signals => events.stream;
  @override
  ImCallInvitation invitation({
    required List<String> inviteeIds,
    required bool video,
    String groupId = '',
  }) => call(inviteeIds: inviteeIds, video: video, inviter: selfId);
  @override
  Future<ImCallInvitation?> restoreIncoming() async => null;
  @override
  Future<RtcMediaSession> join(
    ImCallInvitation invitation, {
    required bool incoming,
    required bool Function() stillActive,
  }) {
    joins++;
    return pending?.future ?? Future.value(media);
  }

  @override
  Future<void> end(ImCallInvitation invitation, RtcEndAction action) async {
    ended.add(action);
  }
}

ImCallInvitation call({
  List<String> inviteeIds = const ['alice'],
  String inviter = 'bob',
  bool video = true,
  String room = 'room-fixture',
  DateTime? initiated,
}) => ImCallInvitation(
  roomId: room,
  inviterId: inviter,
  inviteeIds: inviteeIds,
  mediaType: video ? 'video' : 'audio',
  timeoutSeconds: 30,
  initiatedAt: initiated ?? DateTime.now().toUtc(),
  originPlatform: 2,
  sessionType: 1,
);

Future<void> settle() async {
  for (var i = 0; i < 8; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test('LiveKit logging starts safely from the library default and suppresses private records', () async {
    logging.hierarchicalLoggingEnabled = false;
    expect(disableRtcContentLogging, returnsNormally);
    final records = <logging.LogRecord>[];
    final subscription = lk.logger.onRecord.listen(records.add);
    lk.logger.warning('fixture-private-token');
    await Future<void>.delayed(Duration.zero);
    expect(records, isEmpty);
    expect(lk.getLoggingLevel(), logging.Level.OFF);
    await subscription.cancel();
  });
  test('certificates bind WSS, participant, room, grant and short expiry', () {
    void validate(String url, String value) => validateRtcCertificate(
      url: url,
      token: value,
      roomId: 'room',
      userId: 'alice',
    );
    validate('wss://rtc.example.com', token('room'));
    for (final url in [
      'ws://127.0.0.1:37880',
      'wss://user:pass@rtc.example.com',
      'wss://rtc.example.com?token=x',
    ]) {
      expect(() => validate(url, token('room')), throwsA(isA<AppFailure>()));
    }
    for (final value in [
      token('other'),
      token('room', sub: 'bob'),
      token('room', expires: 1),
      'malformed',
    ]) {
      expect(
        () => validate('wss://rtc.example.com', value),
        throwsA(isA<AppFailure>()),
      );
    }
  });

  group('actual OpenIM MethodChannel adapter', () {
    late SdkHarness harness;
    setUp(() async {
      harness = SdkHarness();
      await harness.start();
    });
    tearDown(() => harness.dispose());
    test(
      'native callback maps sanitized immutable metadata and its generation',
      () async {
        final transport = ImRtcTransport(
          gateway: harness.gateway,
          session: harness.session,
        );
        final received = <RtcSignal>[];
        final subscription = transport.signals.listen(received.add);
        await harness.callback('signalingListener', {
          'type': 'onReceiveNewInvitation',
          'data': jsonEncode({
            'userID': 'bob',
            'token': 'private-not-domain',
            'invitation': {
              'roomID': 'codec-room',
              'inviterUserID': 'bob',
              'inviteeUserIDList': ['alice'],
              'mediaType': 'video',
              'timeout': 99999,
              'initiateTime': DateTime.now().millisecondsSinceEpoch ~/ 1000,
              'platformID': 2,
              'sessionType': 1,
            },
          }),
        });
        expect(received.single.invitation.roomId, 'codec-room');
        expect(received.single.invitation.timeoutSeconds, 120);
        expect(
          received.single.invitation.toString(),
          isNot(contains('private')),
        );
        expect(
          () => received.single.invitation.inviteeIds.add('other'),
          throwsUnsupportedError,
        );
        await harness.session.signOut();
        await subscription.cancel();
      },
    );
    test(
      'late invite after cancellation compensates without starting media',
      () async {
        final response = Completer<String>();
        var connected = 0;
        harness.overrideCall = (method) async {
          if (method.method == 'signalingInvite') return response.future;
          return harness.respond(method);
        };
        final transport = ImRtcTransport(
          gateway: harness.gateway,
          session: harness.session,
          platformId: 2,
          mediaConnector: (_, _, _) async {
            connected++;
            return Media();
          },
        );
        final invitation = transport.invitation(
          inviteeIds: ['bob'],
          video: true,
        );
        var active = true;
        final joined = transport.join(
          invitation,
          incoming: false,
          stillActive: () => active,
        );
        final assertion = expectLater(joined, throwsA(isA<AppFailure>()));
        await settle();
        active = false;
        response.complete(
          jsonEncode({
            'roomID': invitation.roomId,
            'liveURL': 'wss://rtc.example.com',
            'token': token(invitation.roomId),
          }),
        );
        await assertion;
        expect(connected, 0);
        expect(
          harness.calls.where((item) => item.method == 'signalingCancel'),
          hasLength(1),
        );
        final params =
            harness.calls
                    .firstWhere((item) => item.method == 'signalingInvite')
                    .arguments
                as Map;
        expect(params['signalingInfo']['invitation']['platformID'], 2);
        expect(params['signalingInfo']['invitation']['inviteeUserIDList'], [
          'bob',
        ]);
      },
    );
    test(
      'rejects substituted room credentials before media creation',
      () async {
        var connected = false;
        harness.overrideCall = (method) async =>
            method.method == 'signalingInvite'
            ? jsonEncode({
                'roomID': 'wrong',
                'liveURL': 'wss://rtc.example.com',
                'token': token('wrong'),
              })
            : harness.respond(method);
        final transport = ImRtcTransport(
          gateway: harness.gateway,
          session: harness.session,
          mediaConnector: (_, _, _) async {
            connected = true;
            return Media();
          },
        );
        await expectLater(
          transport.join(
            transport.invitation(inviteeIds: ['bob'], video: false),
            incoming: false,
            stillActive: () => true,
          ),
          throwsA(isA<AppFailure>()),
        );
        expect(connected, false);
      },
    );
    test(
      'account change makes old transport unable to send call actions',
      () async {
        final transport = ImRtcTransport(
          gateway: harness.gateway,
          session: harness.session,
        );
        final invitation = transport.invitation(
          inviteeIds: ['bob'],
          video: false,
        );
        await harness.session.signOut();
        harness.userId = 'charlie';
        await harness.session.signIn((_) async => harness.credentials);
        final before = harness.calls.length;
        await expectLater(
          transport.end(invitation, RtcEndAction.hangUp),
          throwsA(isA<AppFailure>()),
        );
        expect(harness.calls.length, before);
      },
    );
  });

  group('call lifecycle', () {
    late SdkHarness harness;
    late Transport transport;
    late Permissions permission;
    late RtcCoordinator coordinator;
    setUp(() async {
      harness = SdkHarness();
      await harness.start();
      transport = Transport(harness.session.state.generation);
      permission = Permissions();
      coordinator = RtcCoordinator(
        session: harness.session,
        permissions: permission,
        transportFactory: () => transport,
      );
    });
    tearDown(() async {
      coordinator.dispose();
      await transport.events.close();
      await harness.dispose();
    });
    test('outgoing permission-plugin failure never cancels a call that was not dispatched', () async {
      permission.failure = const AppFailure(FailureKind.configuration);
      await coordinator.dial(inviteeIds: ['bob'], video: true);
      await settle();
      expect(coordinator.phase, RtcPhase.ended);
      expect(transport.joins, 0);
      expect(transport.ended, isEmpty);
    });
    test(
      'incoming permission-plugin failure rejects the pending invitation',
      () async {
        permission.failure = const AppFailure(FailureKind.configuration);
        transport.events.add(RtcSignal(ImSignalAction.invited, call()));
        await coordinator.accept();
        await settle();
        expect(transport.joins, 0);
        expect(transport.ended, [RtcEndAction.reject]);
      },
    );
    test('media publication failure after acceptance hangs up instead of cancelling an accepted call', () async {
      transport.media.failMicrophone = true;
      await coordinator.dial(inviteeIds: ['bob'], video: false);
      transport.events.add(
        RtcSignal(
          ImSignalAction.accepted,
          call(inviter: 'alice', inviteeIds: ['bob'], video: false),
        ),
      );
      await settle();
      expect(coordinator.reason, RtcEndReason.failed);
      expect(transport.ended, [RtcEndAction.hangUp]);
      expect(transport.media.closed, greaterThan(0));
    });
    test('permission cancelled while pending never dispatches signaling or capture', () async {
      permission.pending = Completer<bool>();
      final dialing = coordinator.dial(inviteeIds: ['bob'], video: true);
      await settle();
      await coordinator.hangUp();
      permission.pending!.complete(true);
      await dialing;
      expect(transport.joins, 0);
      expect(transport.ended, isEmpty);
      expect(transport.media.captures, 0);
    });
    test('outgoing room stays muted until peer accepts, then captures and closes once', () async {
      await coordinator.dial(inviteeIds: ['bob'], video: true);
      expect(transport.media.captures, 0);
      transport.events.add(
        RtcSignal(ImSignalAction.accepted, coordinator.call!),
      );
      await settle();
      expect(coordinator.phase, RtcPhase.active);
      expect(transport.media.captures, 2);
      await coordinator.hangUp();
      await coordinator.hangUp();
      await settle();
      expect(transport.media.closed, 1);
      expect(transport.ended, [RtcEndAction.hangUp]);
    });
    test(
      'late media connection after hangup is closed and never published',
      () async {
        transport.pending = Completer<RtcMediaSession>();
        final dialing = coordinator.dial(inviteeIds: ['bob'], video: true);
        await settle();
        await coordinator.hangUp();
        transport.pending!.complete(transport.media);
        await dialing;
        expect(transport.media.closed, 1);
        expect(transport.media.captures, 0);
        expect(coordinator.reason, RtcEndReason.cancelled);
      },
    );
    test(
      'duplicate, expired and other-device invites never reopen capture',
      () async {
        transport.events.add(RtcSignal(ImSignalAction.invited, call()));
        transport.events.add(RtcSignal(ImSignalAction.invited, call()));
        expect(coordinator.phase, RtcPhase.incoming);
        transport.events.add(
          RtcSignal(ImSignalAction.acceptedOnOtherDevice, call()),
        );
        expect(coordinator.reason, RtcEndReason.otherDevice);
        transport.events.add(RtcSignal(ImSignalAction.invited, call()));
        transport.events.add(
          RtcSignal(
            ImSignalAction.invited,
            call(
              room: 'expired',
              initiated: DateTime.now().subtract(const Duration(minutes: 2)),
            ),
          ),
        );
        expect(coordinator.phase, RtcPhase.ended);
        expect(permission.calls, 0);
      },
    );
    test(
      'session invalidates immediately and stops existing native media',
      () async {
        await coordinator.dial(inviteeIds: ['bob'], video: false);
        transport.events.add(
          RtcSignal(ImSignalAction.accepted, coordinator.call!),
        );
        await settle();
        final exiting = harness.session.signOut();
        expect(coordinator.reason, RtcEndReason.sessionChanged);
        expect(coordinator.media, isNull);
        await exiting;
        await settle();
        expect(transport.media.closed, 1);
        expect(transport.media.microphone, false);
      },
    );
    test(
      'permission denial rejects the incoming call without connecting',
      () async {
        permission.granted = false;
        transport.events.add(RtcSignal(ImSignalAction.invited, call()));
        await coordinator.accept();
        await settle();
        expect(coordinator.reason, RtcEndReason.permissionDenied);
        expect(transport.joins, 0);
        expect(transport.ended, [RtcEndAction.reject]);
      },
    );
    test(
      'background stops camera and does not automatically resume capture',
      () async {
        await coordinator.dial(inviteeIds: ['bob'], video: true);
        transport.events.add(
          RtcSignal(ImSignalAction.accepted, coordinator.call!),
        );
        await settle();
        coordinator.background(true);
        await settle();
        expect(transport.media.camera, false);
        coordinator.background(false);
        await settle();
        expect(transport.media.camera, false);
      },
    );
    test('ring timeout rejects once and never starts native media', () async {
      transport.events.add(
        RtcSignal(
          ImSignalAction.invited,
          call(
            initiated: DateTime.now().subtract(
              const Duration(milliseconds: 29900),
            ),
          ),
        ),
      );
      await Future<void>.delayed(const Duration(milliseconds: 180));
      expect(coordinator.reason, RtcEndReason.timedOut);
      expect(transport.ended, [RtcEndAction.reject]);
      expect(transport.joins, 0);
    });
    test('ordinary local locking preserves an established audio session and allows its hangup', () async {
      await coordinator.dial(inviteeIds: ['bob'], video: false);
      transport.events.add(
        RtcSignal(ImSignalAction.accepted, coordinator.call!),
      );
      await settle();
      expect(transport.media.microphone, true);
      final generation = harness.session.state.generation;
      harness.session.updateInteractiveAccess(false, 1);
      await settle();
      expect(harness.session.state.generation, generation);
      expect(transport.media.closed, 0);
      expect(transport.media.microphone, true);
      await coordinator.hangUp();
      expect(transport.ended, [RtcEndAction.hangUp]);
      expect(transport.media.closed, 1);
    });
    test('permission success arriving after a lock and unlock cannot start the old outgoing call', () async {
      permission.pending = Completer<bool>();
      final old = coordinator.dial(inviteeIds: ['bob'], video: true);
      harness.session.updateInteractiveAccess(false, 1);
      harness.session.updateInteractiveAccess(true, 2);
      permission.pending!.complete(true);
      await old;
      await settle();
      expect(transport.joins, 0);
      expect(transport.media.captures, 0);
    });
    test('a locked incoming call cannot request capture permission but can still be rejected', () async {
      transport.events.add(RtcSignal(ImSignalAction.invited, call()));
      harness.session.updateInteractiveAccess(false, 1);
      await coordinator.accept();
      expect(permission.calls, 0);
      expect(transport.joins, 0);
      await coordinator.hangUp();
      expect(transport.ended, [RtcEndAction.reject]);
    });
  });
}
