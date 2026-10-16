import '../error/app_failure.dart';

enum ImEventKind {
  connecting,
  connected,
  connectionFailed,
  kickedOffline,
  tokenExpired,
  syncStarted,
  syncFinished,
  syncFailed,
  conversationsChanged,
  unreadChanged,
  messageReceived,
  offlineMessageReceived,
  transientMessageReceived,
  messageDeleted,
  messageRevoked,
  readReceiptChanged,
  messageExtensionsChanged,
  historyInvalidated,
  conversationHistoryInvalidated,
  sendProgress,
  groupsChanged,
  contactsChanged,
  selfProfileChanged,
  presenceChanged,
  customBusinessReceived,
  signalingChanged,
}

enum ImSignalAction {
  invited,
  cancelled,
  timedOut,
  accepted,
  rejected,
  acceptedOnOtherDevice,
  rejectedOnOtherDevice,
  hungUp,
  participantConnected,
  participantDisconnected,
  meetingStreamChanged,
  customSignal,
}

/// Sanitized call metadata. Credentials and SDK objects never enter events.
final class ImCallInvitation {
  ImCallInvitation({
    required this.roomId,
    required this.inviterId,
    required Iterable<String> inviteeIds,
    required this.mediaType,
    required this.timeoutSeconds,
    required this.initiatedAt,
    required this.originPlatform,
    required this.sessionType,
    this.groupId = '',
    this.actorId = '',
  }) : inviteeIds = List.unmodifiable(inviteeIds);
  final String roomId;
  final String inviterId;
  final List<String> inviteeIds;
  final String groupId;
  final String actorId;
  final String mediaType;
  final int timeoutSeconds;
  final DateTime initiatedAt;
  final int originPlatform;
  final int sessionType;
  bool get video => mediaType == 'video';
  @override
  String toString() => 'ImCallInvitation';
}

/// Repository invalidations and lifecycle events. SDK models and private
/// message bodies remain inside the IM boundary; presentation receives domain
/// data from repositories, and checks [generation] against the active session.
final class ImEvent {
  ImEvent({
    required this.kind,
    required this.generation,
    Iterable<String> resourceIds = const [],
    this.count,
    this.signalAction,
    this.failure,
    this.call,
    this.historyRevision,
    this.historyClearing,
  }) : resourceIds = List.unmodifiable(resourceIds);

  final ImEventKind kind;
  final int generation;
  final List<String> resourceIds;
  final int? count;
  final ImSignalAction? signalAction;
  final AppFailure? failure;
  final ImCallInvitation? call;
  final int? historyRevision;
  final bool? historyClearing;

  @override
  String toString() => 'ImEvent(${kind.name}, generation: $generation)';
}
