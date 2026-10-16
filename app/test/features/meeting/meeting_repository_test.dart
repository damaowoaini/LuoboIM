import 'dart:async';
import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/meeting/domain/meeting.dart';
import 'package:imapp/features/meeting/presentation/meeting_controller.dart';

import 'meeting_harness.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late MeetingHarness harness;
  setUp(() async {
    harness = MeetingHarness();
    await harness.start();
  });
  tearDown(() async {
    await harness.dispose();
  });

  test('nullable settings remain unknown and malformed flags, cross-owner or duplicate rooms fail closed', () async {
    harness.rows.add(harness.row()..remove('joinDisableVideo'));
    final item = (await harness.repository.list()).single;
    expect(item.settings['joinDisableVideo'], isNull);
    expect(item.settings['onlyHostInviteUser'], isTrue);
    harness.rows.single['hostUserID'] = 'outsider';
    await expectLater(harness.repository.list(), throwsA(isA<AppFailure>()));
    harness.rows.clear();
    harness.rows.addAll([harness.row(), harness.row()]);
    await expectLater(harness.repository.list(), throwsA(isA<AppFailure>()));
  });

  test('schedule uses Unix seconds, verifies canonical metadata and never joins media or exposes certificates', () async {
    final result = await harness.repository.create(
      MeetingDraft(
        title: ' 合成预约 ',
        start: harness.now.add(const Duration(minutes: 15)),
        duration: const Duration(minutes: 90),
        inviteeIds: const ['bob'],
      ),
    );
    expect(result.confirmed, isTrue);
    expect(result.meeting!.title, '合成预约');
    expect(result.roomId, 'created-room');
    final args =
        harness.sdk.calls
                .singleWhere((call) => call.method == 'signalingCreateMeeting')
                .arguments
            as Map;
    expect(args['info']['meetingDuration'], 5400);
    expect(
      args['info']['startTime'],
      harness.now.add(const Duration(minutes: 15)).millisecondsSinceEpoch ~/
          1000,
    );
    expect(harness.calls('signalingJoinMeeting'), 0);
    expect(result.message, isNot(contains('synthetic-must-not-reach-UI')));
  });

  test('invalid UTC duration, duplicate invitees and UTF8 title bounds fail before any SDK work', () async {
    final before = harness.sdk.calls.length;
    for (final draft in [
      MeetingDraft(
        title: '中' * 100,
        start: harness.now,
        duration: const Duration(hours: 1),
      ),
      MeetingDraft(
        title: 'ok',
        start: harness.now,
        duration: const Duration(microseconds: 60000001),
      ),
      MeetingDraft(
        title: 'ok',
        start: harness.now,
        duration: const Duration(hours: 1),
        inviteeIds: const ['bob', 'bob'],
      ),
    ]) {
      await expectLater(
        harness.repository.create(draft),
        throwsA(isA<AppFailure>()),
      );
    }
    expect(harness.sdk.calls.length, before);
  });

  test('an uncertain create never retries and keeps a generic receipt without raw native text', () async {
    harness.override = (call) async {
      if (call.method == 'signalingCreateMeeting') {
        throw PlatformException(
          code: '10005',
          message: 'private token and body',
        );
      }
      return harness.respond(call);
    };
    final result = await harness.repository.create(
      MeetingDraft(
        title: '预约',
        start: harness.now,
        duration: const Duration(hours: 1),
      ),
    );
    expect(result.confirmed, isFalse);
    expect(result.message, isNot(contains('private token')));
    expect(harness.calls('signalingCreateMeeting'), 1);
  });

  test(
    'a host patch preserves explicit false and omits every unchanged setting',
    () async {
      harness.rows.add(harness.row());
      final result = await harness.repository.update('meeting-room', {
        'participantCanUnmuteSelf': false,
      });
      expect(result.confirmed, isTrue);
      final args =
          harness.sdk.calls
                  .singleWhere(
                    (call) => call.method == 'signalingUpdateMeetingInfo',
                  )
                  .arguments
              as Map;
      expect(args['info'], {
        'roomID': 'meeting-room',
        'participantCanUnmuteSelf': false,
      });
      expect(result.meeting!.settings['participantCanUnmuteSelf'], isFalse);
    },
  );

  test('ACK without canonical change stays unconfirmed and a nonhost cannot dispatch a host-only mutation', () async {
    harness.rows.add(harness.row());
    harness.applyChanges = false;
    expect(
      (await harness.repository.update('meeting-room', {
        'participantCanUnmuteSelf': false,
      })).confirmed,
      isFalse,
    );
    harness.rows.single['hostUserID'] = 'bob';
    harness.rows.single['inviteeUserIDList'] = ['alice'];
    final before = harness.calls('signalingUpdateMeetingInfo');
    await expectLater(
      harness.repository.update('meeting-room', {'isMuteAllVideo': true}),
      throwsA(isA<AppFailure>()),
    );
    expect(harness.calls('signalingUpdateMeetingInfo'), before);
  });

  test('signout between authoritative read and write prevents the later SDK mutation', () async {
    harness.rows.add(harness.row());
    final entered = Completer<void>(), release = Completer<String>();
    final old = jsonEncode({'meetingInfoList': harness.rows});
    harness.override = (call) async {
      if (call.method == 'signalingGetMeetings') {
        entered.complete();
        return release.future;
      }
      return harness.respond(call);
    };
    final pending = expectLater(
      harness.repository.update('meeting-room', {'isMuteAllVideo': true}),
      throwsA(isA<AppFailure>()),
    );
    await entered.future;
    await harness.sdk.session.signOut();
    release.complete(old);
    await pending;
    expect(harness.calls('signalingUpdateMeetingInfo'), 0);
  });

  test('controller clears meeting metadata immediately when its owner generation is invalidated', () async {
    harness.rows.add(harness.row());
    final controller = MeetingController(harness.repository);
    addTearDown(controller.dispose);
    await controller.refresh();
    expect(controller.items, hasLength(1));
    await harness.sdk.session.signOut();
    expect(controller.items, isEmpty);
    expect(controller.usable, isFalse);
  });

  test('host close requires authoritative disappearance and guest close never dispatches', () async {
    harness.rows.add(harness.row());
    harness.applyChanges = false;
    expect((await harness.repository.close('meeting-room')).confirmed, isFalse);
    harness.applyChanges = true;
    expect((await harness.repository.close('meeting-room')).confirmed, isTrue);
    harness.rows.add(
      harness.row(host: 'bob')..['inviteeUserIDList'] = ['alice'],
    );
    final before = harness.calls('signalingCloseRoom');
    await expectLater(
      harness.repository.close('meeting-room'),
      throwsA(isA<AppFailure>()),
    );
    expect(harness.calls('signalingCloseRoom'), before);
  });

  test(
    'unpaginated oversized lists never become a truncated complete success',
    () async {
      harness.rows.addAll(
        List.generate(1001, (index) => harness.row(id: 'room-$index')),
      );
      await expectLater(harness.repository.list(), throwsA(isA<AppFailure>()));
    },
  );

  test('wire booleans cannot be forged by string or number coercion and milliseconds are not Unix seconds', () {
    for (final malformed in [
      harness.row()..['onlyHostInviteUser'] = 'false',
      harness.row()..['isMuteAllVideo'] = 1,
      harness.row()..['startTime'] = harness.now.millisecondsSinceEpoch,
    ]) {
      expect(() => Meeting.fromJson(malformed), throwsA(isA<AppFailure>()));
    }
  });

  test('legacy empty topics retain host fallback and an unchanged past start can be edited without moving the meeting', () async {
    final row = harness.row()
      ..['meetingName'] = ''
      ..['startTime'] =
          harness.now
              .subtract(const Duration(minutes: 30))
              .millisecondsSinceEpoch ~/
          1000;
    harness.rows.add(row);
    final meeting = (await harness.repository.list()).single;
    expect(meeting.title, isEmpty);
    expect(meeting.displayTitle, 'alice 发起的会议');
    final draft = MeetingDraft(
      title: '补充主题',
      start: meeting.start,
      duration: meeting.end.difference(meeting.start),
    );
    expect(() => draft.validate(harness.now), throwsA(isA<AppFailure>()));
    draft.validate(harness.now, originalStart: meeting.start);
    final result = await harness.repository.update(meeting.id, {
      'meetingName': draft.title,
    });
    expect(result.confirmed, isTrue);
    expect(result.meeting!.start, meeting.start);
  });

  test('host media restrictions need authoritative readback and never admit a guest or screen-share dispatch', () async {
    harness.rows.add(harness.row());
    final result = await harness.repository.moderate(
      'meeting-room',
      'bob',
      MeetingMediaKind.audio,
      mute: true,
    );
    expect(result.confirmed, isTrue);
    expect(result.meeting!.microphoneDisabledIds, ['bob']);
    harness.applyChanges = false;
    expect(
      (await harness.repository.moderate(
        'meeting-room',
        'bob',
        MeetingMediaKind.audio,
        mute: false,
      )).confirmed,
      isFalse,
    );
    final before = harness.calls('signalingOperateStream');
    await expectLater(
      harness.repository.moderate(
        'meeting-room',
        'bob',
        MeetingMediaKind.screen,
        mute: true,
      ),
      throwsA(isA<AppFailure>()),
    );
    harness.rows.single['hostUserID'] = 'bob';
    harness.rows.single['inviteeUserIDList'] = ['alice'];
    await expectLater(
      harness.repository.moderate(
        'meeting-room',
        'bob',
        MeetingMediaKind.audio,
        mute: true,
      ),
      throwsA(isA<AppFailure>()),
    );
    expect(harness.calls('signalingOperateStream'), before);
  });
}
