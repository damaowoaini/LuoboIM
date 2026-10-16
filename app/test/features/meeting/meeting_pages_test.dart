import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/meeting/presentation/meeting_editor_page.dart';
import 'package:imapp/features/meeting/presentation/meeting_detail_page.dart';
import 'package:imapp/features/meeting/presentation/meetings_page.dart';

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

  Future<void> mount(WidgetTester tester, Widget page) async {
    tester.view.physicalSize = const Size(360, 760);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp(
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(context)
                .copyWith(textScaler: const TextScaler.linear(1.6)),
            child: child!,
          ),
          home: page,
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets(
    'meeting list and details remain readable at large text without pretending media admission is enabled',
    (tester) async {
      harness.rows.add(harness.row());
      await mount(tester, MeetingsPage(repository: harness.repository));
      expect(find.text('合成会议'), findsOneWidget);
      await tester.tap(find.text('合成会议'));
      await tester.pumpAndSettle();
      expect(find.text('会议详情'), findsOneWidget);
      final button = tester.widget<FilledButton>(
        find.widgetWithText(FilledButton, '音视频入会暂未开放'),
      );
      expect(button.onPressed, isNull);
      expect(harness.calls('signalingJoinMeeting'), 0);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'invalid schedule keeps the draft and does not request room creation',
    (tester) async {
      await mount(tester, MeetingEditorPage(repository: harness.repository));
      await tester.enterText(find.byType(TextField).first, '中' * 100);
      await tester.ensureVisible(find.text('保存会议'));
      await tester.tap(find.text('保存会议'));
      await tester.pumpAndSettle();
      expect(find.text('请检查填写的信息'), findsOneWidget);
      expect(harness.calls('signalingCreateMeeting'), 0);
      expect(
        tester.widget<TextField>(find.byType(TextField).first).controller!.text,
        '中' * 100,
      );
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'signout erases the stale editor draft and prevents the old page from submitting',
    (tester) async {
      await mount(tester, MeetingEditorPage(repository: harness.repository));
      await tester.enterText(find.byType(TextField).first, '旧账号的会议主题');
      await tester.runAsync(
        () => harness.sdk.session.signOut().timeout(const Duration(seconds: 2)),
      );
      await tester.pumpAndSettle();
      expect(
        tester.widget<TextField>(find.byType(TextField).first).controller!.text,
        isEmpty,
      );
      await tester.ensureVisible(find.text('保存会议'));
      expect(
        tester
            .widget<FilledButton>(find.widgetWithText(FilledButton, '保存会议'))
            .onPressed,
        isNull,
      );
      expect(harness.calls('signalingCreateMeeting'), 0);
    },
  );

  testWidgets('signout clears and disables an already open invitation dialog', (
    tester,
  ) async {
    harness.rows.add(harness.row());
    await mount(
      tester,
      MeetingDetailPage(
        repository: harness.repository,
        meetingId: 'meeting-room',
      ),
    );
    await tester.scrollUntilVisible(
      find.text('邀请参会者'),
      300,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.ensureVisible(find.text('邀请参会者'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('邀请参会者'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), 'old-owner-invitee');
    await tester.runAsync(
      () => harness.sdk.session.signOut().timeout(const Duration(seconds: 2)),
    );
    await tester.pumpAndSettle();
    expect(
      tester.widget<TextField>(find.byType(TextField)).controller!.text,
      isEmpty,
    );
    expect(
      tester
          .widget<FilledButton>(find.widgetWithText(FilledButton, '提交邀请'))
          .onPressed,
      isNull,
    );
    expect(harness.calls('signalingUpdateMeetingInfo'), 0);
  });
}
