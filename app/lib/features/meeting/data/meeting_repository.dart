import 'package:flutter_openim_sdk/flutter_openim_sdk.dart' as sdk;

import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../core/tenant/session_coordinator.dart';
import '../domain/meeting.dart';

final class MeetingRepository {
  MeetingRepository({
    required this.gateway,
    required this.session,
    DateTime Function()? now,
  }) : generation = session.state.generation,
       interactiveRevision = session.interactiveRevision,
       selfId = session.state.credentials?.userId ?? '',
       _accountKey = session.state.credentials?.accountKey,
       now = now ?? DateTime.now;
  final OpenImGateway gateway;
  final SessionCoordinator session;
  final int generation;
  final int interactiveRevision;
  final String selfId;
  final String? _accountKey;
  final DateTime Function() now;
  final Set<String> _mutating = {};

  bool get current =>
      selfId.isNotEmpty &&
      session.isInteractiveCurrent(generation, interactiveRevision) &&
      session.state.credentials?.accountKey == _accountKey;
  void check() {
    if (!current) {
      throw const AppFailure(FailureKind.cancelled);
    }
  }

  Future<T> _run<T>(Future<T> Function(sdk.IMManager) operation) {
    check();
    return gateway.runForSession(generation, operation);
  }

  Future<List<Meeting>> list() async {
    final value = await _run(
      (manager) => manager.signalingManager.signalingGetMeetings(),
    );
    check();
    final source = value.meetingInfoList ?? const <sdk.MeetingInfo>[];
    // The legacy RPC is an unpaginated list. Never label a client truncation as
    // a complete page. A server pagination extension remains a separate task.
    if (source.length > 1000) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final ids = <String>{};
    final result = <Meeting>[];
    for (final item in source) {
      final meeting = Meeting.fromJson(item.toJson());
      if (!ids.add(meeting.id) ||
          (meeting.hostId != selfId && !meeting.inviteeIds.contains(selfId))) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      result.add(meeting);
    }
    return List.unmodifiable(result);
  }

  Future<Meeting?> get(String id) async {
    meetingId(id);
    for (final item in await list()) {
      if (item.id == id) {
        return item;
      }
    }
    return null;
  }

  Future<MeetingWriteResult> create(MeetingDraft draft) async {
    draft.validate(now());
    check();
    if (!_mutating.add('create')) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    var dispatched = false;
    String? room;
    try {
      final grant = await _run((manager) {
        dispatched = true;
        return manager.signalingManager.signalingCreateMeeting(
          meetingName: draft.title.trim(),
          meetingHostUserID: selfId,
          startTime: draft.start.millisecondsSinceEpoch ~/ 1000,
          meetingDuration: draft.duration.inSeconds,
          inviteeUserIDList: draft.inviteeIds.toSet().toList(),
        );
      });
      // A scheduled meeting does not connect media or retain its short-lived
      // creation certificate. Admission/permissions precede any future join.
      check();
      room = meetingId(grant.roomID);
      final saved = await get(room);
      final confirmed =
          saved != null &&
          saved.hostId == selfId &&
          saved.title == draft.title.trim() &&
          saved.start.millisecondsSinceEpoch ~/ 1000 ==
              draft.start.millisecondsSinceEpoch ~/ 1000 &&
          saved.end.difference(saved.start) == draft.duration;
      return MeetingWriteResult(
        roomId: room,
        confirmed: confirmed,
        meeting: saved,
      );
    } catch (error) {
      check();
      if (!dispatched || _definiteRejection(error)) {
        rethrow;
      }
      return MeetingWriteResult(roomId: room, confirmed: false);
    } finally {
      _mutating.remove('create');
    }
  }

  Future<MeetingWriteResult> update(
    String id,
    Map<String, Object> patch,
  ) async {
    final room = meetingId(id);
    if (patch.isEmpty || patch.length > 12) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    for (final field in patch.entries) {
      if (Meeting.settingNames.contains(field.key)) {
        if (field.value is! bool) {
          throw const AppFailure(FailureKind.invalidInput);
        }
      } else if (field.key == 'inviteeUserIDList') {
        meetingUserIds(field.value);
      } else if (field.key == 'meetingName') {
        MeetingDraft(
          title: field.value is String ? field.value as String : '',
          start: now(),
          duration: const Duration(hours: 1),
        ).validate(now());
      } else if ((field.key != 'startTime' && field.key != 'endTime') ||
          field.value is! int) {
        throw const AppFailure(FailureKind.invalidInput);
      }
    }
    final currentMeeting = await get(room);
    check();
    if (currentMeeting == null) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final inviteOnly = patch.keys.every((key) => key == 'inviteeUserIDList');
    if (currentMeeting.hostId != selfId &&
        !(inviteOnly && currentMeeting.canInvite(selfId))) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    final start =
        patch['startTime'] ??
        currentMeeting.start.millisecondsSinceEpoch ~/ 1000;
    final end =
        patch['endTime'] ?? currentMeeting.end.millisecondsSinceEpoch ~/ 1000;
    if (start is! int ||
        end is! int ||
        start < 946684800 ||
        end > 4103049600 ||
        end <= start ||
        end - start > 7 * 24 * 3600) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    return _write(
      room,
      () => _run(
        (manager) => manager.signalingManager.signalingUpdateMeetingInfo(
          info: {'roomID': room, ...patch},
        ),
      ),
      verify: (saved) {
        for (final field in patch.entries) {
          if (Meeting.settingNames.contains(field.key) &&
              saved.settings[field.key] != field.value) {
            return false;
          }
          if (field.key == 'meetingName' && saved.title != field.value) {
            return false;
          }
          if (field.key == 'startTime' &&
              saved.start.millisecondsSinceEpoch ~/ 1000 != field.value) {
            return false;
          }
          if (field.key == 'endTime' &&
              saved.end.millisecondsSinceEpoch ~/ 1000 != field.value) {
            return false;
          }
          if (field.key == 'inviteeUserIDList' &&
              !meetingUserIds(field.value).every(saved.inviteeIds.contains)) {
            return false;
          }
        }
        return true;
      },
    );
  }

  Future<MeetingWriteResult> close(String id) async {
    final room = meetingId(id);
    final meeting = await get(room);
    check();
    if (meeting == null || meeting.hostId != selfId) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    return _write(
      room,
      () => _run(
        (manager) => manager.signalingManager.signalingCloseRoom(roomID: room),
      ),
      removed: true,
    );
  }

  Future<MeetingWriteResult> moderate(
    String id,
    String userId,
    MeetingMediaKind kind, {
    required bool mute,
  }) async {
    final room = meetingId(id);
    meetingUserIds([userId]);
    final meeting = await get(room);
    check();
    if (meeting == null ||
        meeting.hostId != selfId ||
        kind == MeetingMediaKind.screen) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    if (userId != selfId && !meeting.inviteeIds.contains(userId)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    return _write(
      room,
      () => _run(
        (manager) => manager.signalingManager.signalingOperateStream(
          roomID: room,
          userID: userId,
          streamType: kind.name,
          mute: mute,
        ),
      ),
      verify: (saved) =>
          (kind == MeetingMediaKind.audio
                  ? saved.microphoneDisabledIds
                  : saved.videoDisabledIds)
              .contains(userId) ==
          mute,
    );
  }

  Future<MeetingWriteResult> _write(
    String room,
    Future<dynamic> Function() action, {
    bool removed = false,
    bool Function(Meeting)? verify,
  }) async {
    check();
    if (!_mutating.add(room)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    try {
      check();
      await action();
      check();
      final saved = await get(room);
      return MeetingWriteResult(
        roomId: room,
        confirmed: removed
            ? saved == null
            : saved != null && (verify?.call(saved) ?? true),
        meeting: saved,
      );
    } catch (error) {
      check();
      if (_definiteRejection(error)) {
        rethrow;
      }
      return MeetingWriteResult(roomId: room, confirmed: false);
    } finally {
      _mutating.remove(room);
    }
  }

  bool _definiteRejection(Object error) =>
      error is AppFailure &&
      const {
        FailureKind.invalidInput,
        FailureKind.permissionDenied,
        FailureKind.sessionExpired,
        FailureKind.cancelled,
      }.contains(error.kind);
}
