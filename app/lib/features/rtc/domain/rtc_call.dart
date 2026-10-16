import '../../../core/im/im_event.dart';

export '../../../core/im/im_event.dart' show ImCallInvitation, ImSignalAction;

enum RtcPhase {
  idle,
  requestingPermission,
  outgoing,
  incoming,
  connecting,
  active,
  reconnecting,
  ended,
}

enum RtcEndReason {
  cancelled,
  declined,
  timedOut,
  hungUp,
  busy,
  otherDevice,
  disconnected,
  permissionDenied,
  sessionChanged,
  failed,
}

enum RtcEndAction { cancel, reject, hangUp }

enum RtcMediaConnection { connected, reconnecting, disconnected }

final class RtcPeer {
  const RtcPeer({
    required this.id,
    required this.name,
    this.local = false,
    this.speaking = false,
    this.microphone = false,
    this.camera = false,
  });
  final String id;
  final String name;
  final bool local;
  final bool speaking;
  final bool microphone;
  final bool camera;
}

final class RtcSignal {
  const RtcSignal(this.action, this.invitation);
  final ImSignalAction action;
  final ImCallInvitation invitation;
}

/// Media implementations own tracks and tokens. Business/UI state receives
/// participant metadata and controls, never room credentials.
abstract class RtcMediaSession {
  Stream<void> get changes;
  Stream<RtcMediaConnection> get connection;
  List<RtcPeer> get peers;
  bool get microphone;
  bool get camera;
  bool get speaker;
  Future<void> setMicrophone(bool enabled);
  Future<void> setCamera(bool enabled);
  Future<void> setSpeaker(bool enabled);
  Future<void> switchCamera();
  Future<void> close();
}

abstract interface class RtcCallTransport {
  String get selfId;
  int get generation;
  bool get current;
  Stream<RtcSignal> get signals;
  ImCallInvitation invitation({
    required List<String> inviteeIds,
    required bool video,
    String groupId = '',
  });
  Future<ImCallInvitation?> restoreIncoming();
  Future<RtcMediaSession> join(
    ImCallInvitation invitation, {
    required bool incoming,
    required bool Function() stillActive,
  });
  Future<void> end(ImCallInvitation invitation, RtcEndAction action);
}

abstract interface class RtcPermissionGate {
  Future<bool> request({required bool video});
  Future<bool> openSettings();
}
