import 'dart:async';

import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/error/app_failure.dart';
import '../../core/im/im_event.dart';
import '../../core/network/tenant_api.dart';
import '../../providers.dart';
import 'data/moments_repository.dart';
import 'domain/moment.dart';
import 'presentation/moment_list_controller.dart';

void watchMomentEvents(
  Ref ref,
  MomentsRepository repository,
  void Function() refresh,
) {
  Timer? debounce;
  void schedule() {
    if (!ref.mounted || !repository.isCurrent) return;
    debounce?.cancel();
    debounce = Timer(const Duration(milliseconds: 200), () {
      if (ref.mounted && repository.isCurrent) refresh();
    });
  }

  final subscription = ref.watch(imGatewayProvider).events.listen((event) {
    if (event.generation == repository.api.access.generation &&
        (event.kind == ImEventKind.customBusinessReceived ||
            event.kind == ImEventKind.syncFinished)) {
      schedule();
    }
  });
  final lifecycle = AppLifecycleListener(onResume: schedule);
  ref.onDispose(() {
    debounce?.cancel();
    lifecycle.dispose();
    unawaited(subscription.cancel());
  });
}

final momentsRepositoryProvider = Provider.autoDispose<MomentsRepository?>((
  ref,
) {
  final api = ref.watch(tenantApiProvider);
  return api == null ? null : MomentsRepository(api);
});
final momentListProvider = Provider.autoDispose
    .family<MomentListController?, String?>((ref, author) {
      final repository = ref.watch(momentsRepositoryProvider);
      if (repository == null) return null;
      final controller = MomentListController(repository, author: author);
      watchMomentEvents(ref, repository, () => unawaited(controller.refresh()));
      ref.onDispose(controller.dispose);
      controller.refresh();
      return controller;
    });
final momentDetailProvider = FutureProvider.autoDispose.family<Moment, String>((
  ref,
  id,
) {
  final repository = ref.watch(momentsRepositoryProvider);
  if (repository == null) throw const AppFailure(FailureKind.cancelled);
  watchMomentEvents(ref, repository, ref.invalidateSelf);
  return repository.detail(id);
});
final momentUnreadProvider = FutureProvider.autoDispose<int>((ref) {
  final repository = ref.watch(momentsRepositoryProvider);
  if (repository == null) throw const AppFailure(FailureKind.cancelled);
  watchMomentEvents(ref, repository, ref.invalidateSelf);
  return repository.unreadCount();
});
