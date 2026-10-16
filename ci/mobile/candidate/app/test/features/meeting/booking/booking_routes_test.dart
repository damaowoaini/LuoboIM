import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/app.dart';
import 'package:imapp/core/app_runtime.dart';
import 'package:imapp/core/config/app_config.dart';
import 'package:imapp/core/config/route_config_loader.dart';
import 'package:imapp/core/im/open_im_gateway.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/features/meeting/booking/booking_providers.dart';
import 'package:imapp/providers.dart';

import '../../../core/privacy_bootstrap_test.dart' show ConsentMemory, ImProbe, StoreProbe;

void main() {
  for (final consented in [false, true]) {
    testWidgets('actual booking routes cannot open a journal before ${consented ? 'login' : 'consent'}', (tester) async {
      final consent = ConsentMemory()..granted = consented;
      final store = StoreProbe(), im = ImProbe();
      late final SessionCoordinator session;
      // FIFO seed futures must originate in the real zone, like the real App.
      await tester.runAsync(() async {
        session = SessionCoordinator(store: store, im: im);
      });
      final config = AppConfig(privacyUrl: 'https://policy.fixture.test/privacy',
        termsUrl: 'https://policy.fixture.test/terms', fallbackRoutes: [Uri.parse('https://route.fixture.test/')]);
      final runtime = AppRuntime(config: config, consent: consent, session: session,
        routes: RouteConfigLoader(config: config), auth: (_, _) async => throw StateError('No HTTP auth in route fixture'));
      var journalStarts = 0, sdkStarts = 0;
      late final OpenImGateway gateway;
      await tester.runAsync(() async {
        gateway = OpenImGateway(hasPrivacyConsent: () => runtime.hasPrivacyConsent,
          managerFactory: () { sdkStarts++; throw StateError('SDK forbidden before booking authorization'); });
      });
      final container = ProviderContainer(overrides: [
        appRuntimeProvider.overrideWith((_) => runtime), imGatewayProvider.overrideWithValue(gateway),
        bookingControllerProvider.overrideWith((_) { journalStarts++; throw StateError('Journal forbidden'); }),
      ]);
      try {
        await tester.runAsync(() => runtime.start().timeout(const Duration(seconds: 5)));
        for (final path in ['/meetings/bookings', '/meetings/bookings/new',
          '/meetings/bookings/20000000-0000-4000-8000-000000000001']) {
          final router = container.read(routerProvider)..go(path);
          await tester.pumpWidget(UncontrolledProviderScope(container: container, child: const LuoboApp()));
          await tester.pumpAndSettle();
          expect(router.routeInformationProvider.value.uri.path, consented ? '/login' : '/privacy');
          expect(journalStarts, 0); expect(sdkStarts, 0);
        }
      } finally {
        await tester.pumpWidget(const SizedBox.shrink());
        await tester.pump();
        // Provider disposal starts unawaited lifecycle work. Start the entire
        // release in the real async zone so its FIFO is not stranded in FakeAsync.
        await tester.runAsync(() async {
          debugPrint('BOOKING_ROUTE_CLEANUP phase=container_start');
          container.dispose();
          debugPrint('BOOKING_ROUTE_CLEANUP phase=session_start');
          await session.dispose().timeout(const Duration(seconds: 5));
          debugPrint('BOOKING_ROUTE_CLEANUP phase=gateway_start');
          await gateway.dispose().timeout(const Duration(seconds: 5));
          debugPrint('BOOKING_ROUTE_CLEANUP phase=complete');
        });
      }
      expect(tester.takeException(), isNull);
    });
  }
}
