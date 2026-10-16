import 'dart:async';

import 'package:livekit_client/livekit_client.dart' as lk;
import 'package:logging/logging.dart' as logging;

import '../../../core/error/app_failure.dart';
import '../domain/rtc_call.dart';

void disableRtcContentLogging() {
  // logging rejects setting a non-root logger level until hierarchy is enabled.
  // LiveKit's disableLogging helper alone otherwise throws before Room creation.
  logging.hierarchicalLoggingEnabled = true;
  lk.disableLogging();
}

/// Owns the native room, tracks and credentials. No token is exposed to UI.
final class LiveKitMediaSession implements RtcMediaSession {
  LiveKitMediaSession._() {
    disableRtcContentLogging();
    _room = lk.Room(
      roomOptions: const lk.RoomOptions(adaptiveStream: true, dynacast: true),
    );
    _room.addListener(_changed);
    _listener = _room.createListener()
      ..on<lk.RoomConnectedEvent>(
        (_) => _connection.add(RtcMediaConnection.connected),
      )
      ..on<lk.RoomReconnectedEvent>(
        (_) => _connection.add(RtcMediaConnection.connected),
      )
      ..on<lk.RoomReconnectingEvent>(
        (_) => _connection.add(RtcMediaConnection.reconnecting),
      )
      ..on<lk.RoomDisconnectedEvent>((_) {
        if (!_closed) _connection.add(RtcMediaConnection.disconnected);
      });
  }

  late final lk.Room _room;
  late final lk.EventsListener<lk.RoomEvent> _listener;
  final _changes = StreamController<void>.broadcast();
  final _connection = StreamController<RtcMediaConnection>.broadcast();
  bool _closed = false;
  bool _speaker = false;
  bool _front = true;
  Future<void>? _closing;

  /// URL validation belongs to the production signaling adapter. A separate
  /// tool may use loopback fixtures without weakening production validation.
  static Future<LiveKitMediaSession> connect(
    String url,
    String token, {
    required bool Function() stillActive,
  }) async {
    final session = LiveKitMediaSession._();
    final pending = session._room.connect(url, token);
    // A timeout/cancel does not cancel native connection establishment. Dispose
    // again when its completion arrives so it cannot resurrect a closed room.
    unawaited(
      pending.then((_) async {
        if (session._closed || !stillActive()) {
          try {
            await session._room.disconnect();
          } catch (_) {}
        }
      }, onError: (Object _) {}),
    );
    try {
      await pending.timeout(const Duration(seconds: 20));
      if (!stillActive()) throw const AppFailure(FailureKind.cancelled);
      return session;
    } catch (_) {
      await session.close();
      throw const AppFailure(FailureKind.network);
    }
  }

  void _changed() {
    if (!_closed) _changes.add(null);
  }

  @override
  Stream<void> get changes => _changes.stream;
  @override
  Stream<RtcMediaConnection> get connection => _connection.stream;
  @override
  bool get microphone => _room.localParticipant?.isMicrophoneEnabled() ?? false;
  @override
  bool get camera => _room.localParticipant?.isCameraEnabled() ?? false;
  @override
  bool get speaker => _speaker;
  @override
  List<RtcPeer> get peers => [
    if (_room.localParticipant case final participant?)
      _peer(participant, true),
    ..._room.remoteParticipants.values.map(
      (participant) => _peer(participant, false),
    ),
  ];

  RtcPeer _peer(lk.Participant participant, bool local) => RtcPeer(
    id: participant.identity,
    name: participant.name.isEmpty ? participant.identity : participant.name,
    local: local,
    speaking: participant.isSpeaking,
    microphone: participant.isMicrophoneEnabled(),
    camera: participant.isCameraEnabled(),
  );

  lk.VideoTrack? videoTrack(String identity) {
    final lk.Participant? participant =
        _room.localParticipant?.identity == identity
        ? _room.localParticipant
        : _room.remoteParticipants[identity];
    for (final publication
        in participant?.videoTrackPublications ?? <lk.TrackPublication>[]) {
      if (publication.track case final lk.VideoTrack track) return track;
    }
    return null;
  }

  /// Numeric counters for diagnostics; never exposes room credentials or media.
  Future<({int videoFrames, int audioPackets})> receiverCounters() async {
    var video = 0, audio = 0;
    for (final participant in _room.remoteParticipants.values) {
      for (final publication in participant.videoTrackPublications) {
        if (publication.track case final lk.RemoteVideoTrack track) {
          video +=
              (await track.getReceiverStats())?.framesDecoded?.toInt() ?? 0;
        }
      }
      for (final publication in participant.audioTrackPublications) {
        if (publication.track case final lk.RemoteAudioTrack track) {
          audio +=
              (await track.getReceiverStats())?.packetsReceived?.toInt() ?? 0;
        }
      }
    }
    return (videoFrames: video, audioPackets: audio);
  }

  void _check() {
    if (_closed) throw const AppFailure(FailureKind.cancelled);
  }

  @override
  Future<void> setMicrophone(bool enabled) async {
    _check();
    await _room.localParticipant?.setMicrophoneEnabled(enabled);
    if (_closed) await _room.localParticipant?.setMicrophoneEnabled(false);
    _changed();
  }

  @override
  Future<void> setCamera(bool enabled) async {
    _check();
    await _room.localParticipant?.setCameraEnabled(enabled);
    if (_closed) await _room.localParticipant?.setCameraEnabled(false);
    _changed();
  }

  @override
  Future<void> setSpeaker(bool enabled) async {
    _check();
    await lk.AudioManager.instance.setSpeakerOutputPreferred(enabled);
    if (!_closed) _speaker = enabled;
    _changed();
  }

  @override
  Future<void> switchCamera() async {
    _check();
    for (final publication
        in _room.localParticipant?.videoTrackPublications ??
            <lk.LocalTrackPublication>[]) {
      if (publication.track case final lk.LocalVideoTrack track) {
        _front = !_front;
        await track.setCameraPosition(
          _front ? lk.CameraPosition.front : lk.CameraPosition.back,
        );
      }
    }
  }

  @override
  Future<void> close() => _closing ??= () async {
    _closed = true;
    _room.removeListener(_changed);
    try {
      await _room.disconnect().timeout(const Duration(seconds: 5));
    } catch (_) {}
    await _listener.dispose();
    await _room.dispose();
    await _changes.close();
    await _connection.close();
  }();
}
