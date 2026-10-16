import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers.dart';
import 'data/meeting_repository.dart';

final meetingRepositoryProvider = Provider<MeetingRepository>((ref) {
  ref.watch(appAccessProvider.select((access) => access.revision));
  return MeetingRepository(
    gateway: ref.watch(imGatewayProvider),
    session: ref.watch(appRuntimeProvider).session,
  );
});
