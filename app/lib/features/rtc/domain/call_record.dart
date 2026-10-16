import 'dart:convert';

import 'package:characters/characters.dart';
import 'package:crypto/crypto.dart';

import '../../../core/error/app_failure.dart';
import 'rtc_call.dart';

enum CallDirection { incoming, outgoing }
enum CallEndCause { local, remote, timer, transport }
enum CallRecordsFilter { all, missed }

/// Storage snapshots contain public peer metadata, never credentials/signaling.
final class CallRecord {
  const CallRecord({
    required this.id, required this.claim, required this.peerId,
    required this.name, required this.direction, required this.video,
    required this.admittedAt, this.avatar, this.sequence = 0,
    this.revision = 1, this.accepted = false, this.connected = false,
    this.signalingStarted = false, this.terminal = false,
    this.durationMs = 0, this.endedAt, this.reason, this.cause,
  });

  static const maxPeerLength = 128;
  static const maxNameGraphemes = 128;
  final String id, claim, peerId, name;
  final String? avatar;
  final CallDirection direction;
  final bool video, accepted, connected, signalingStarted, terminal;
  final int sequence, revision, admittedAt, durationMs;
  final int? endedAt;
  final RtcEndReason? reason;
  final CallEndCause? cause;

  static bool validId(String value) => value.isNotEmpty &&
      value.length <= maxPeerLength && utf8.decode(utf8.encode(value)) == value &&
      !RegExp(r'[\x00-\x1f\x7f]').hasMatch(value);

  static String? safeAvatar(String? value) {
    if (value == null || value.length > 2048) return null;
    final uri = Uri.tryParse(value);
    return uri != null && uri.scheme == 'https' && uri.host.isNotEmpty &&
        uri.userInfo.isEmpty && !uri.hasQuery && !uri.hasFragment ? value : null;
  }

  factory CallRecord.admit(ImCallInvitation call, {
    required String selfId, required String title, required DateTime now,
  }) {
    if (call.groupId.isNotEmpty || call.inviteeIds.length != 1) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final incoming = call.inviterId != selfId;
    final peer = incoming ? call.inviterId : call.inviteeIds.single;
    if (
        !validId(selfId) || !validId(peer) || peer == selfId ||
        (incoming && !call.inviteeIds.contains(selfId)) ||
        call.roomId.isEmpty || utf8.encode(call.roomId).length > 512 ||
        utf8.decode(utf8.encode(call.roomId)) != call.roomId ||
        RegExp(r'[\x00-\x1f\x7f]').hasMatch(call.roomId) ||
        !const ['audio', 'video'].contains(call.mediaType)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final started = call.initiatedAt.millisecondsSinceEpoch;
    if (started < 946684800000 || started > 4102444800000) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final invitees = [...call.inviteeIds]..sort();
    String hash(Object value) => sha256.convert(utf8.encode(jsonEncode(value))).toString();
    final display = title.trim().isEmpty ? peer : title.trim();
    final name = display.characters.take(maxNameGraphemes).toString();
    return CallRecord(
      id: hash(['rtc-room-v1', call.roomId]),
      claim: hash([selfId, call.inviterId, invitees, call.sessionType,
        call.mediaType, started]),
      peerId: peer, name: utf8.encode(name).length <= 1024 ? name : peer,
      direction: incoming ? CallDirection.incoming : CallDirection.outgoing,
      video: call.video, admittedAt: now.toUtc().millisecondsSinceEpoch,
    );
  }

  bool get missed => terminal && direction == CallDirection.incoming &&
      !accepted && !connected &&
      ((reason == RtcEndReason.timedOut &&
          (cause == CallEndCause.timer || cause == CallEndCause.remote)) ||
       (cause == CallEndCause.remote &&
          (reason == RtcEndReason.cancelled || reason == RtcEndReason.hungUp)));

  String label({required bool live}) {
    if (!terminal) return live ? '通话进行中' : '上次通话结果未确认';
    if (connected) return '已通话';
    if (missed) return '未接来电';
    return switch (reason) {
      RtcEndReason.permissionDenied => '未获得通话权限',
      RtcEndReason.otherDevice => '已在其他设备处理',
      RtcEndReason.busy => '忙线，未接入',
      RtcEndReason.declined => cause == CallEndCause.local ? '已拒绝' : '对方已拒绝',
      RtcEndReason.cancelled => signalingStarted ? '已取消' : '未发出呼叫',
      RtcEndReason.timedOut => '呼叫未接通',
      RtcEndReason.failed => signalingStarted ? '连接失败，结果未确认' : '未能发起通话',
      _ => '通话已结束',
    };
  }

  CallRecord event({bool? accepted, bool? connected, bool? signalingStarted,
    bool? terminal, int? durationMs, int? endedAt,
    RtcEndReason? reason, CallEndCause? cause}) => CallRecord(
      id: id, claim: claim, peerId: peerId, name: name, avatar: avatar,
      direction: direction, video: video, admittedAt: admittedAt,
      sequence: sequence, revision: revision + 1,
      accepted: accepted ?? this.accepted, connected: connected ?? this.connected,
      signalingStarted: signalingStarted ?? this.signalingStarted,
      terminal: terminal ?? this.terminal, durationMs: durationMs ?? this.durationMs,
      endedAt: endedAt ?? this.endedAt, reason: reason ?? this.reason,
      cause: cause ?? this.cause);

  Map<String, Object?> toRow() => {
    'call_id': id, 'tuple_digest': claim, 'event_revision': revision,
    'peer_id': peerId, 'name': name, 'avatar': safeAvatar(avatar),
    'direction': direction.name, 'video': video ? 1 : 0,
    'admitted_at': admittedAt, 'accepted': accepted ? 1 : 0,
    'connected': connected ? 1 : 0, 'signaling_started': signalingStarted ? 1 : 0,
    'terminal': terminal ? 1 : 0, 'duration_ms': durationMs,
    'ended_at': endedAt, 'reason': reason?.name, 'cause': cause?.name,
  };

  factory CallRecord.fromRow(Map<String, Object?> row) {
    final value = CallRecord(
    id: row['call_id'] as String, claim: row['tuple_digest'] as String,
    peerId: row['peer_id'] as String, name: row['name'] as String,
    avatar: row['avatar'] as String?, sequence: row['sequence'] as int,
    revision: row['event_revision'] as int, admittedAt: row['admitted_at'] as int,
    direction: CallDirection.values.byName(row['direction'] as String),
    video: row['video'] == 1, accepted: row['accepted'] == 1,
    connected: row['connected'] == 1, signalingStarted: row['signaling_started'] == 1,
    terminal: row['terminal'] == 1, durationMs: row['duration_ms'] as int,
    endedAt: row['ended_at'] as int?,
    reason: row['reason'] == null ? null : RtcEndReason.values.byName(row['reason'] as String),
    cause: row['cause'] == null ? null : CallEndCause.values.byName(row['cause'] as String),
    );
    value.validate();
    return value;
  }

  void validate() {
    if (!RegExp(r'^[a-f0-9]{64}$').hasMatch(id) ||
        !RegExp(r'^[a-f0-9]{64}$').hasMatch(claim) || !validId(peerId) ||
        name.characters.length > maxNameGraphemes || utf8.encode(name).length > 1024 ||
        revision < 1 || revision > 0x1fffffffffffff || sequence < 0 ||
        admittedAt < 946684800000 || admittedAt > 4102444800000 ||
        durationMs < 0 || durationMs > 31536000000 ||
        (endedAt != null && (endedAt! < admittedAt || endedAt! > 4102444800000)) ||
        (connected && !accepted) ||
        (terminal && (reason == null || cause == null || endedAt == null)) ||
        (!terminal && (reason != null || cause != null || endedAt != null))) {
      throw const AppFailure(FailureKind.invalidInput);
    }
  }
}

final class CallRecordsCursor {
  const CallRecordsCursor(this.accountKey, this.filter, this.revision,
    this.highWater, this.beforeSequence);
  final String accountKey;
  final CallRecordsFilter filter;
  final int revision, highWater, beforeSequence;
}

final class CallRecordsPageResult {
  const CallRecordsPageResult(this.items, this.next);
  final List<CallRecord> items;
  final CallRecordsCursor? next;
}
