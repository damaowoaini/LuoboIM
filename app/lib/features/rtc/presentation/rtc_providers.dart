import 'package:flutter_riverpod/legacy.dart';

import '../../../providers.dart';
import '../data/im_rtc_transport.dart';
import '../data/rtc_permissions.dart';
import '../domain/rtc_coordinator.dart';
import 'call_records_providers.dart';

final rtcCoordinatorProvider = ChangeNotifierProvider<RtcCoordinator>((ref) {
  final session = ref.watch(appRuntimeProvider.notifier).session;
  final gateway = ref.watch(imGatewayProvider);
  return RtcCoordinator(
    session: session,
    permissions: NativeRtcPermissions(),
    history: ref.watch(callHistoryRecorderProvider),
    transportFactory: () => ImRtcTransport(gateway: gateway, session: session),
  );
});
