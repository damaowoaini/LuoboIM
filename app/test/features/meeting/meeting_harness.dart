import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:imapp/features/meeting/data/meeting_repository.dart';

import '../../core/im/sdk_harness.dart';

final class MeetingHarness {
  final sdk = SdkHarness();
  final now = DateTime.utc(2030, 1, 1);
  late MeetingRepository repository;
  final rows = <Map<String, dynamic>>[];
  Future<dynamic> Function(MethodCall)? override;
  bool applyChanges = true;

  Map<String, dynamic> row({
    String id = 'meeting-room',
    String host = 'alice',
  }) => {
    'roomID': id,
    'meetingName': '合成会议',
    'hostUserID': host,
    'inviteeUserIDList': ['bob'],
    'startTime':
        now.add(const Duration(minutes: 10)).millisecondsSinceEpoch ~/ 1000,
    'endTime':
        now.add(const Duration(minutes: 70)).millisecondsSinceEpoch ~/ 1000,
    'participantCanUnmuteSelf': true,
    'participantCanEnableVideo': true,
    'onlyHostInviteUser': true,
    'onlyHostShareScreen': true,
    'joinDisableMicrophone': true,
    'joinDisableVideo': true,
    'isMuteAllMicrophone': false,
    'isMuteAllVideo': false,
    'canScreenUserIDList': <String>[],
    'disableMicrophoneUserIDList': <String>[],
    'disableVideoUserIDList': <String>[],
  };

  Future<void> start() async {
    sdk.overrideCall = (call) async =>
        override == null ? respond(call) : override!(call);
    await sdk.start();
    repository = MeetingRepository(
      gateway: sdk.gateway,
      session: sdk.session,
      now: () => now,
    );
  }

  dynamic respond(MethodCall call) {
    final args = call.arguments as Map;
    if (call.method == 'signalingGetMeetings') {
      return jsonEncode({'meetingInfoList': rows});
    }
    if (call.method == 'signalingCreateMeeting') {
      final info = args['info'] as Map;
      rows.add({
        ...row(id: 'created-room'),
        ...Map<String, dynamic>.from(info),
        'roomID': 'created-room',
        'hostUserID': 'alice',
        'endTime':
            (info['startTime'] as int) + (info['meetingDuration'] as int),
      });
      return jsonEncode({
        'roomID': 'created-room',
        'token': 'synthetic-must-not-reach-UI',
        'liveURL': 'wss://media.example.test',
      });
    }
    if (call.method == 'signalingUpdateMeetingInfo' && applyChanges) {
      final info = Map<String, dynamic>.from(args['info'] as Map);
      rows.singleWhere((row) => row['roomID'] == info['roomID']).addAll(info);
    }
    if (call.method == 'signalingCloseRoom' && applyChanges) {
      rows.removeWhere((row) => row['roomID'] == args['roomID']);
    }
    if (call.method == 'signalingOperateStream' && applyChanges) {
      final row = rows.singleWhere((row) => row['roomID'] == args['roomID']);
      final field = args['streamType'] == 'audio'
          ? 'disableMicrophoneUserIDList'
          : 'disableVideoUserIDList';
      final ids = List<String>.from(row[field] as List);
      if (args['mute'] == true) {
        ids.add(args['userID'] as String);
      } else {
        ids.remove(args['userID']);
      }
      row[field] = ids;
    }
    return sdk.respond(call);
  }

  int calls(String method) =>
      sdk.calls.where((call) => call.method == method).length;
  Future<void> dispose() => sdk.dispose();
}
