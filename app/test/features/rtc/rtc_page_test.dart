import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/rtc/domain/rtc_call.dart';
import 'package:imapp/features/rtc/domain/rtc_coordinator.dart';
import 'package:imapp/features/rtc/presentation/rtc_page.dart';
import 'package:imapp/features/rtc/presentation/rtc_providers.dart';

import '../../core/im/sdk_harness.dart';
import 'rtc_test.dart' as fixture;

void main() {
  testWidgets(
    'global incoming card rejects without permissions or media; hidden on the full call page',
    (tester) async {
      final harness = SdkHarness();
      await tester.runAsync(harness.start);
      final transport = fixture.Transport(harness.session.state.generation);
      final permission = fixture.Permissions();
      final coordinator = RtcCoordinator(
        session: harness.session,
        permissions: permission,
        transportFactory: () => transport,
      );
      var opens = 0;
      Widget app(bool show) => ProviderScope(
        overrides: [rtcCoordinatorProvider.overrideWith((ref) => coordinator)],
        child: MaterialApp(
          home: RtcIncomingHost(
            onOpenCall: () => opens++,
            showOngoingBar: show,
            child: const Scaffold(body: Text('会话')),
          ),
        ),
      );
      await tester.pumpWidget(app(true));
      transport.events.add(RtcSignal(ImSignalAction.invited, fixture.call()));
      await tester.pump();
      expect(find.text('接听'), findsOneWidget);
      await tester.tap(find.text('拒绝'));
      await tester.pump();
      expect(permission.calls, 0);
      expect(transport.joins, 0);
      expect(transport.ended, [RtcEndAction.reject]);
      expect(opens, 0);
      transport.events.add(
        RtcSignal(ImSignalAction.invited, fixture.call(room: 'next')),
      );
      await tester.pumpWidget(app(false));
      await tester.pump();
      expect(find.text('接听'), findsNothing);
      await tester.pumpWidget(const SizedBox());
      await tester.runAsync(harness.dispose);
      await transport.events.close();
    },
  );

  testWidgets(
    'incoming full-page actions remain usable on a small screen at large text',
    (tester) async {
      tester.view.physicalSize = const Size(360, 640);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final harness = SdkHarness();
      await tester.runAsync(harness.start);
      final transport = fixture.Transport(harness.session.state.generation);
      final permission = fixture.Permissions();
      final coordinator = RtcCoordinator(
        session: harness.session,
        permissions: permission,
        transportFactory: () => transport,
      );
      transport.events.add(RtcSignal(ImSignalAction.invited, fixture.call()));
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            rtcCoordinatorProvider.overrideWith((ref) => coordinator),
          ],
          child: MaterialApp(
            builder: (context, child) => MediaQuery(
              data: MediaQuery.of(context)
                  .copyWith(textScaler: const TextScaler.linear(1.6)),
              child: child!,
            ),
            home: const RtcPage(),
          ),
        ),
      );
      await tester.pump();
      expect(find.text('接听'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.tap(find.text('拒绝'));
      await tester.pump();
      expect(find.text('通话已拒绝'), findsOneWidget);
      expect(permission.calls, 0);
      await tester.pumpWidget(const SizedBox());
      await tester.runAsync(harness.dispose);
      await transport.events.close();
    },
  );
}
