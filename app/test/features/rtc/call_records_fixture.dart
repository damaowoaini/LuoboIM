import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:imapp/app.dart';
import 'package:imapp/core/app_runtime.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/rtc/data/call_records_repository.dart';
import 'package:imapp/features/rtc/domain/call_history_recorder.dart';
import 'package:imapp/features/rtc/domain/call_record.dart';
import 'package:imapp/features/rtc/domain/rtc_call.dart';
import 'package:imapp/features/rtc/domain/rtc_coordinator.dart';
import 'package:imapp/features/rtc/presentation/call_records_providers.dart';
import 'package:imapp/features/rtc/presentation/rtc_providers.dart';
import 'package:imapp/providers.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';

ImCallInvitation historyInvitation({String room = 'record-room', String inviter = 'bob',
    List<String> invitees = const ['alice'], bool video = false, String group = '', DateTime? initiated}) =>
  ImCallInvitation(roomId: room, inviterId: inviter, inviteeIds: invitees,
    groupId: group, mediaType: video ? 'video' : 'audio', timeoutSeconds: 30,
    initiatedAt: initiated ?? DateTime.now().toUtc(), sessionType: group.isEmpty ? 1 : 3,
    originPlatform: 2);

final class HistoryPermissions implements RtcPermissionGate {
  bool granted = true;
  int requests = 0;
  Completer<bool>? pending;
  @override Future<bool> request({required bool video}) { requests++; return pending?.future ?? Future.value(granted); }
  @override Future<bool> openSettings() async => true;
}
final class HistoryMedia implements RtcMediaSession {
  final changed = StreamController<void>.broadcast(sync: true);
  final states = StreamController<RtcMediaConnection>.broadcast(sync: true);
  @override bool microphone = false, camera = false, speaker = false;
  @override List<RtcPeer> get peers => [];
  @override Stream<void> get changes => changed.stream;
  @override Stream<RtcMediaConnection> get connection => states.stream;
  @override Future<void> setMicrophone(bool value) async { microphone = value; }
  @override Future<void> setCamera(bool value) async { camera = value; }
  @override Future<void> setSpeaker(bool value) async { speaker = value; }
  @override Future<void> switchCamera() async {}
  @override Future<void> close() async { microphone = camera = false; }
  Future<void> dispose() async { await changed.close(); await states.close(); }
}

final class HistoryTransport implements RtcCallTransport {
  HistoryTransport(this.owner, this.generation);
  final SessionCredentials owner;
  @override final int generation;
  @override String get selfId => owner.userId;
  @override bool current = true;
  final events = StreamController<RtcSignal>.broadcast(sync: true);
  final media = HistoryMedia();
  final endActions = <RtcEndAction>[];
  Completer<RtcMediaSession>? joinReply;
  Object? joinFailure;
  int joins = 0, _serial = 0;
  @override Stream<RtcSignal> get signals => events.stream;
  @override ImCallInvitation invitation({required List<String> inviteeIds, required bool video, String groupId = ''}) =>
    historyInvitation(room: 'outgoing-${++_serial}-$generation', inviter: selfId,
      invitees: inviteeIds, video: video, group: groupId);
  @override Future<ImCallInvitation?> restoreIncoming() async => null;
  @override Future<RtcMediaSession> join(ImCallInvitation call, {required bool incoming, required bool Function() stillActive}) {
    joins++;
    if (joinFailure != null) return Future.error(joinFailure!);
    return joinReply?.future ?? Future.value(media);
  }
  @override Future<void> end(ImCallInvitation call, RtcEndAction action) async { endActions.add(action); }
  Future<void> dispose() async { await events.close(); await media.dispose(); }
}

/// Real SQLite, coordinator, session/gateway and production router. Only native
/// signaling/media/OS and the existing SDK method boundary are synthetic.
final class CallRecordsFixture {
  final sdk = SdkHarness();
  final permissions = HistoryPermissions();
  final transports = <HistoryTransport>[];
  late Directory directory;
  late CallHistoryRecorder history;
  late RtcCoordinator rtc;
  late GoRouter router;
  late ProviderContainer container;
  int tick = 0;
  DateTime now = DateTime.now().toUtc();
  HistoryTransport get transport => transports.last;
  CallRecordsDatabaseOpener? overrideOpen;
  bool _providerOwnsRtc = false;

  Future<Database> open(String key) => overrideOpen?.call(key) ??
    initializeCallRecordsDatabase(databaseFactoryFfi, '${directory.path}/$key.db', key);
  Future<void> start() async {
    sqfliteFfiInit();
    directory = await Directory.systemTemp.createTemp('luobo-call-records-');
    await sdk.start();
    history = CallHistoryRecorder(session: sdk.session,
      now: () => now, monotonicMicros: () => tick,
      repositories: (key) => CallRecordsRepository(accountKey: key, open: open));
    rtc = RtcCoordinator(session: sdk.session, permissions: permissions, history: history,
      transportFactory: () {
        final transport = HistoryTransport(sdk.session.state.credentials!, sdk.session.state.generation);
        transports.add(transport); return transport;
      });
  }
  Future<void> phase(RtcPhase value) async {
    if (rtc.phase == value) return;
    final ready = Completer<void>();
    void changed() { if (rtc.phase == value && !ready.isCompleted) ready.complete(); }
    rtc.addListener(changed);
    try { await ready.future.timeout(const Duration(seconds: 5)); }
    finally { rtc.removeListener(changed); }
  }
  Future<List<CallRecord>> rows({CallRecordsFilter filter = CallRecordsFilter.all}) async {
    await history.flush();
    final access = history.capture();
    return (await access.repository.list(filter: filter, current: () => access.current)).items;
  }
  Future<void> connectedOutgoing() async {
    await rtc.dial(inviteeIds: ['bob'], video: false, title: '同名好友');
    transport.events.add(RtcSignal(ImSignalAction.accepted, rtc.call!));
    await phase(RtcPhase.active);
  }
  Future<void> mount(WidgetTester tester, {bool inbox = false, double width = 360, double height = 640}) async {
    _providerOwnsRtc = true;
    tester.view.physicalSize = Size(width, height); tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize); addTearDown(tester.view.resetDevicePixelRatio);
    final theme = await captureTheme(tester);
    sdk.runtime.phase = AppPhase.online;
    await tester.pumpWidget(ProviderScope(overrides: [
      appRuntimeProvider.overrideWith((_) => sdk.runtime),
      imGatewayProvider.overrideWithValue(sdk.gateway),
      callHistoryRecorderProvider.overrideWithValue(history),
      rtcCoordinatorProvider.overrideWith((_) => rtc),
    ], child: Consumer(builder: (context, ref, _) {
      container = ProviderScope.containerOf(context, listen: false);
      router = ref.watch(routerProvider);
      return MaterialApp.router(theme: theme, routerConfig: router,
        builder: (context, child) => MediaQuery(data: MediaQuery.of(context)
          .copyWith(textScaler: const TextScaler.linear(1.5)), child: child!));
    })));
    router.go(inbox ? '/home' : '/calls'); await frame(tester);
  }
  Future<void> frame(WidgetTester tester) async {
    await tester.runAsync(() async { await Future<void>.delayed(Duration.zero); });
    await tester.pump(const Duration(milliseconds: 20));
  }
  Future<void> until(WidgetTester tester, bool Function() condition) async {
    for (var i = 0; i < 150; i++) {
      if (condition()) return;
      await frame(tester);
    }
    throw TestFailure('Expected bounded call history route/state');
  }
  Future<void> ready(WidgetTester tester, Finder target) async {
    final route = ModalRoute.of(tester.element(target));
    if (route == null) throw TestFailure('Expected owned call history route');
    await until(tester, () => route.isCurrent &&
      (route.animation == null || route.animation!.status == AnimationStatus.completed));
  }
  Future<void> removed(WidgetTester tester, TransitionRoute<dynamic> route) async {
    var done = false; unawaited(route.completed.then<void>((_) { done = true; }));
    await until(tester, () => done);
  }
  Future<void> coverAndReturn(WidgetTester tester, Finder from) async {
    final navigator = Navigator.of(tester.element(from));
    final route = MaterialPageRoute<void>(builder: (_) => const Scaffold(body: Text('foreign-history-route')));
    unawaited(navigator.push(route));
    await until(tester, () => find.text('foreign-history-route').evaluate().isNotEmpty);
    await ready(tester, find.text('foreign-history-route'));
    navigator.pop(); await removed(tester, route);
  }
  Future<void> close() async {
    if (!_providerOwnsRtc) rtc.dispose();
    try {
      print('call_records_cleanup recorder.begin');
      await history.close().timeout(const Duration(seconds: 10));
      print('call_records_cleanup recorder.end');
    } finally {
      history.dispose();
      print('call_records_cleanup sdk.begin');
      await sdk.dispose();
      print('call_records_cleanup sdk.end');
      for (final transport in transports) { await transport.dispose(); }
      await directory.delete(recursive: true);
      print('call_records_cleanup storage.end');
    }
  }
}
