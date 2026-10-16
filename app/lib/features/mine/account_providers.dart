import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/error/app_failure.dart';
import '../../core/im/im_event.dart';
import '../../core/network/tenant_api.dart';
import '../../providers.dart';
import 'data/account_repository.dart';
import 'data/account_security_repository.dart';

final accountSecurityRepositoryProvider =
    Provider.autoDispose<AccountSecurityRepository?>((ref) {
      final access = ref.watch(sessionAccessProvider);
      return access == null ? null : AccountSecurityRepository(access);
    });

final passwordChangeRepositoryProvider = accountSecurityRepositoryProvider;

final accountRepositoryProvider = Provider.autoDispose<AccountRepository?>((
  ref,
) {
  final api = ref.watch(tenantApiProvider);
  return api == null
      ? null
      : AccountRepository(api, ref.watch(imGatewayProvider));
});

final accountProfileProvider = FutureProvider.autoDispose<AccountProfile>((
  ref,
) {
  final repository = ref.watch(accountRepositoryProvider);
  if (repository == null) throw const AppFailure(FailureKind.cancelled);
  Timer? debounce;
  final subscription = ref.watch(imGatewayProvider).events.listen((event) {
    if (repository.isCurrent &&
        event.generation == repository.api.access.generation &&
        (event.kind == ImEventKind.selfProfileChanged ||
            event.kind == ImEventKind.syncFinished)) {
      debounce?.cancel();
      debounce = Timer(const Duration(milliseconds: 200), () {
        if (ref.mounted && repository.isCurrent) ref.invalidateSelf();
      });
    }
  });
  ref.onDispose(() {
    debounce?.cancel();
    unawaited(subscription.cancel());
  });
  return repository.profile();
});
