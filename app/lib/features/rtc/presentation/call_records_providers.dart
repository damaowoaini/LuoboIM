import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../providers.dart';
import '../domain/call_history_recorder.dart';

// Stable manager, new immutable owner binding on every actual login. Reading a
// history page is not what activates recording.
final callHistoryRecorderProvider = Provider<CallHistoryRecorder>((ref) {
  final recorder = CallHistoryRecorder(
    session: ref.watch(appRuntimeProvider.notifier).session);
  ref.onDispose(recorder.dispose);
  return recorder;
});
