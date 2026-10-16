import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../core/tenant/session_coordinator.dart';
import '../domain/rtc_call.dart';
import 'livekit_media_session.dart';

typedef RtcMediaConnector = Future<RtcMediaSession> Function(
  String url,
  String token,
  bool Function() stillActive,
);

/// Checks claims for accidental/malicious cross-room credential substitution.
/// JWT signature validation remains the trusted LiveKit server's responsibility.
void validateRtcCertificate({
  required String url,
  required String token,
  required String roomId,
  required String userId,
  DateTime? now,
}) {
  try {
    final uri = Uri.parse(url);
    if (uri.scheme != 'wss' ||
        uri.host.isEmpty ||
        uri.userInfo.isNotEmpty ||
        uri.hasFragment ||
        uri.hasQuery) {
      throw const FormatException();
    }
    final parts = token.split('.');
    if (parts.length != 3 || parts[2].isEmpty) throw const FormatException();
    final claims = jsonDecode(
      utf8.decode(base64Url.decode(base64Url.normalize(parts[1]))),
    );
    final seconds = (now ?? DateTime.now()).millisecondsSinceEpoch ~/ 1000;
    if (claims is! Map ||
        claims['sub'] != userId ||
        claims['video'] is! Map ||
        claims['video']['room'] != roomId ||
        claims['video']['roomJoin'] != true ||
        claims['exp'] is! num ||
        claims['exp'] <= seconds ||
        claims['exp'] > seconds + 86400 ||
        (claims['nbf'] is num && claims['nbf'] > seconds + 30)) {
      throw const FormatException();
    }
  } catch (_) {
    throw const AppFailure(FailureKind.invalidResponse);
  }
}

final class ImRtcTransport implements RtcCallTransport {
  ImRtcTransport({
    required this.gateway,
    required this.session,
    RtcMediaConnector? mediaConnector,
    int? platformId,
  }) : generation = session.state.generation,
       selfId = session.state.credentials?.userId ?? '',
       _accountKey = session.state.credentials?.accountKey ?? '',
       _platform = platformId ?? (Platform.isIOS ? 1 : 2),
       _connect =
           mediaConnector ??
           ((url, token, active) =>
               LiveKitMediaSession.connect(url, token, stillActive: active));

  final OpenImGateway gateway;
  final SessionCoordinator session;
  final String _accountKey;
  final int _platform;
  final RtcMediaConnector _connect;
  @override
  final String selfId;
  @override
  final int generation;
  @override
  bool get current =>
      selfId.isNotEmpty &&
      session.acceptsEvent(generation) &&
      session.state.credentials?.accountKey == _accountKey;
  void _check() {
    if (!current) throw const AppFailure(FailureKind.cancelled);
  }

  Future<T> _run<T>(
    Future<T> Function(IMManager manager) operation, {
    bool allowWhileLocked = false,
  }) {
    _check();
    return gateway.runForSession(
      generation,
      operation,
      allowWhileLocked: allowWhileLocked,
    );
  }

  @override
  Stream<RtcSignal> get signals => gateway.events
      .where(
        (event) =>
            current &&
            event.generation == generation &&
            event.signalAction != null &&
            event.call != null,
      )
      .map((event) => RtcSignal(event.signalAction!, event.call!));

  @override
  ImCallInvitation invitation({
    required List<String> inviteeIds,
    required bool video,
    String groupId = '',
  }) {
    _check();
    final ids = inviteeIds
        .where((id) => id.isNotEmpty && id != selfId)
        .toSet()
        .toList();
    if (ids.isEmpty || ids.length > 9 || (groupId.isEmpty && ids.length != 1)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final random = Random.secure();
    return ImCallInvitation(
      roomId: List.generate(
        16,
        (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0'),
      ).join(),
      inviterId: selfId,
      inviteeIds: ids,
      groupId: groupId,
      mediaType: video ? 'video' : 'audio',
      timeoutSeconds: 30,
      initiatedAt: DateTime.now().toUtc(),
      originPlatform: _platform,
      sessionType: groupId.isEmpty ? 1 : 3,
    );
  }

  SignalingInfo _info(ImCallInvitation call) => SignalingInfo(
    userID: selfId,
    invitation: InvitationInfo(
      roomID: call.roomId,
      inviterUserID: call.inviterId,
      inviteeUserIDList: call.inviteeIds,
      groupID: call.groupId,
      timeout: call.timeoutSeconds,
      initiateTime: call.initiatedAt.millisecondsSinceEpoch ~/ 1000,
      mediaType: call.mediaType,
      sessionType: call.sessionType,
      platformID: call.originPlatform,
    ),
    offlinePushInfo: OfflinePushInfo(title: '通话邀请', desc: '你收到一个通话邀请'),
  );

  @override
  Future<ImCallInvitation?> restoreIncoming() async {
    final raw = await _run(
      (manager) =>
          manager.signalingManager.getSignalingInvitationInfoStartApp(),
      allowWhileLocked: true,
    );
    final call = raw.invitation;
    if (call == null ||
        (call.roomID ?? '').isEmpty ||
        !(call.inviteeUserIDList ?? []).contains(selfId)) {
      return null;
    }
    final initiated = DateTime.fromMillisecondsSinceEpoch(
      (call.initiateTime ?? 0) * 1000,
      isUtc: true,
    );
    final timeout = (call.timeout ?? 30).clamp(1, 120);
    if (DateTime.now().isAfter(initiated.add(Duration(seconds: timeout)))) {
      return null;
    }
    return ImCallInvitation(
      roomId: call.roomID!,
      inviterId: call.inviterUserID ?? '',
      inviteeIds: call.inviteeUserIDList ?? [],
      groupId: call.groupID ?? '',
      mediaType: call.mediaType ?? 'audio',
      timeoutSeconds: timeout,
      initiatedAt: initiated,
      originPlatform: call.platformID ?? 0,
      sessionType: call.sessionType ?? 1,
    );
  }

  @override
  Future<RtcMediaSession> join(
    ImCallInvitation call, {
    required bool incoming,
    required bool Function() stillActive,
  }) async {
    final accessRevision = session.interactiveRevision;
    bool active() =>
        current &&
        session.isInteractiveCurrent(generation, accessRevision) &&
        stillActive();
    if (!active()) throw const AppFailure(FailureKind.cancelled);
    var dispatched = false;
    try {
      final certificate = await _run((manager) {
        dispatched = true;
        final signaling = manager.signalingManager;
        if (incoming) return signaling.signalingAccept(info: _info(call));
        return call.groupId.isEmpty
            ? signaling.signalingInvite(info: _info(call))
            : signaling.signalingInviteInGroup(info: _info(call));
      });
      if (!active()) throw const AppFailure(FailureKind.cancelled);
      if (certificate.roomID != call.roomId ||
          (certificate.busyLineUserIDList ?? []).toSet().containsAll(
            call.inviteeIds,
          )) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      final url = certificate.liveURL ?? '';
      final token = certificate.token ?? '';
      validateRtcCertificate(
        url: url,
        token: token,
        roomId: call.roomId,
        userId: selfId,
      );
      _check();
      final media = await _connect(url, token, active);
      if (!active()) {
        await media.close();
        throw const AppFailure(FailureKind.cancelled);
      }
      return media;
    } catch (error) {
      // Cancellation can race the server creating a room. Repeat the idempotent
      // end operation after its late response, while the original account lives.
      if (dispatched && current) {
        try {
          await end(call, incoming ? RtcEndAction.hangUp : RtcEndAction.cancel);
        } catch (_) {}
      }
      if (error is AppFailure) rethrow;
      throw const AppFailure(FailureKind.network);
    }
  }

  @override
  Future<void> end(ImCallInvitation call, RtcEndAction action) async {
    await _run(
      (manager) => switch (action) {
        RtcEndAction.cancel => manager.signalingManager.signalingCancel(
          info: _info(call),
        ),
        RtcEndAction.reject => manager.signalingManager.signalingReject(
          info: _info(call),
        ),
        RtcEndAction.hangUp => manager.signalingManager.signalingHungUp(
          info: _info(call),
        ),
      },
      allowWhileLocked: true,
    );
  }
}
