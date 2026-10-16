import 'dart:convert';

import '../../../core/error/app_failure.dart';

enum MeetingMediaKind { audio, video, screen }

String meetingId(Object? value) {
  if (value is! String ||
      value.isEmpty ||
      value.length > 128 ||
      !RegExp(r'^[a-zA-Z0-9_-]+$').hasMatch(value)) {
    throw const AppFailure(FailureKind.invalidResponse);
  }
  return value;
}

List<String> meetingUserIds(Object? value) {
  if (value == null) {
    return const [];
  }
  if (value is! List || value.length > 100) {
    throw const AppFailure(FailureKind.invalidResponse);
  }
  final result = <String>[];
  for (final item in value) {
    if (item is! String ||
        item.isEmpty ||
        utf8.encode(item).length > 64 ||
        RegExp(r'[\x00-\x20\x7f]').hasMatch(item)) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final id = item;
    if (result.contains(id)) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    result.add(id);
  }
  return List.unmodifiable(result);
}

final class Meeting {
  Meeting.fromJson(Map<String, dynamic> json)
    : id = meetingId(json['roomID']),
      hostId = meetingUserIds([json['hostUserID']]).single,
      title = _title(json['meetingName']),
      start = _date(json['startTime']),
      end = _date(json['endTime']),
      inviteeIds = meetingUserIds(json['inviteeUserIDList']),
      settings = Map.unmodifiable({
        for (final name in settingNames) name: _flag(json[name]),
      }),
      screenAllowedIds = meetingUserIds(json['canScreenUserIDList']),
      microphoneDisabledIds = meetingUserIds(
        json['disableMicrophoneUserIDList'],
      ),
      videoDisabledIds = meetingUserIds(json['disableVideoUserIDList']) {
    if (!end.isAfter(start) ||
        end.difference(start) > const Duration(days: 7)) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
  }
  final String id, title, hostId;
  final DateTime start, end;
  final List<String> inviteeIds;
  final Map<String, bool?> settings;
  final List<String> screenAllowedIds, microphoneDisabledIds, videoDisabledIds;
  static const settingNames = {
    'participantCanUnmuteSelf',
    'participantCanEnableVideo',
    'onlyHostInviteUser',
    'onlyHostShareScreen',
    'joinDisableMicrophone',
    'joinDisableVideo',
    'isMuteAllVideo',
    'isMuteAllMicrophone',
  };

  bool canInvite(String userId) =>
      userId == hostId || settings['onlyHostInviteUser'] == false;

  // Older meetings may have no custom topic. Preserve that wire value and
  // display the same host fallback as the legacy client.
  String get displayTitle => title.trim().isEmpty ? '$hostId 发起的会议' : title;

  static String _title(Object? value) {
    if (value == null) {
      return '';
    }
    if (value is! String ||
        utf8.encode(value).length > 256 ||
        RegExp(r'[\x00-\x1f\x7f]').hasMatch(value)) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return value;
  }

  static bool? _flag(Object? value) {
    if (value == null || value is bool) {
      return value as bool?;
    }
    throw const AppFailure(FailureKind.invalidResponse);
  }

  static DateTime _date(Object? value) {
    if (value is! int || value < 946684800 || value > 4103049600) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return DateTime.fromMillisecondsSinceEpoch(value * 1000, isUtc: true);
  }
}

final class MeetingDraft {
  const MeetingDraft({
    required this.title,
    required this.start,
    required this.duration,
    this.inviteeIds = const [],
  });
  final String title;
  final DateTime start;
  final Duration duration;
  final List<String> inviteeIds;

  void validate(DateTime now, {DateTime? originalStart}) {
    if (title.trim().isEmpty ||
        utf8.encode(title.trim()).length > 256 ||
        RegExp(r'[\x00-\x1f\x7f]').hasMatch(title) ||
        (start.isBefore(now.subtract(const Duration(minutes: 1))) &&
            (originalStart == null ||
                !start.isAtSameMomentAs(originalStart))) ||
        start.isAfter(now.add(const Duration(days: 365))) ||
        duration.inSeconds < 60 ||
        duration.inSeconds > 7 * 24 * 3600 ||
        duration.inMicroseconds % 1000000 != 0 ||
        inviteeIds.length > 100) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    try {
      meetingUserIds(inviteeIds);
    } on AppFailure {
      throw const AppFailure(FailureKind.invalidInput);
    }
  }
}

/// No room token or native object may enter presentation state.
final class MeetingWriteResult {
  const MeetingWriteResult({
    this.roomId,
    required this.confirmed,
    this.meeting,
  });
  final String? roomId;
  final bool confirmed;
  final Meeting? meeting;
  String get message => confirmed ? '请求已完成，会议列表已核对' : '结果尚未确认，请返回会议列表核对，勿重复提交。';
}
