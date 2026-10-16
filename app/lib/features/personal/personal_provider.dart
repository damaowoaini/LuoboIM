import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/error/app_failure.dart';
import '../../providers.dart';
import 'data/personal_repository.dart';

final personalRepositoryProvider = Provider.autoDispose<PersonalRepository>((
  ref,
) {
  final runtime = ref.watch(appRuntimeProvider);
  ref.watch(appAccessProvider.select((access) => access.revision));
  final snapshot = runtime.session.state;
  final interactiveRevision = runtime.session.interactiveRevision;
  final credentials = snapshot.credentials;
  if (credentials == null ||
      !runtime.session.acceptsEvent(snapshot.generation)) {
    throw const AppFailure(FailureKind.sessionExpired);
  }
  final repository = PersonalRepository(
    accountKey: credentials.accountKey,
    isCurrent: () => runtime.session.isInteractiveCurrent(
      snapshot.generation,
      interactiveRevision,
    ),
  );
  ref.onDispose(() => unawaited(repository.close()));
  return repository;
});
