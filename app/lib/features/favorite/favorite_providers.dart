import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/error/app_failure.dart';
import '../../core/network/tenant_api.dart';
import '../../providers.dart';
import 'data/favorite_repository.dart';
import 'domain/favorite.dart';
import 'presentation/favorite_list_controller.dart';

class FavoriteRevision extends Notifier<int> {
  @override
  int build() => 0;
  void bump() => state++;
}

final favoriteRevisionProvider = NotifierProvider<FavoriteRevision, int>(
  FavoriteRevision.new,
);
final favoriteListControllerProvider =
    Provider.autoDispose<FavoriteListController?>((ref) {
      final repository = ref.watch(favoriteRepositoryProvider);
      if (repository == null) return null;
      final controller = FavoriteListController(repository);
      ref.onDispose(controller.dispose);
      controller.refresh();
      return controller;
    });

final favoriteRepositoryProvider = Provider.autoDispose<FavoriteRepository?>((
  ref,
) {
  final api = ref.watch(tenantApiProvider);
  return api == null
      ? null
      : FavoriteRepository(
          api,
          privacyLedger: ref.read(imGatewayProvider).privacyLedger,
        );
});
final favoriteCategoriesProvider =
    FutureProvider.autoDispose<List<FavoriteCategory>>((ref) {
      final repository = ref.watch(favoriteRepositoryProvider);
      if (repository == null) throw const AppFailure(FailureKind.cancelled);
      return repository.categories();
    });
final favoriteStatsProvider = FutureProvider.autoDispose<FavoriteStats>((ref) {
  final repository = ref.watch(favoriteRepositoryProvider);
  if (repository == null) throw const AppFailure(FailureKind.cancelled);
  return repository.stats();
});
final favoriteInfoProvider = FutureProvider.autoDispose
    .family<FavoriteItem, String>((ref, id) {
      final repository = ref.watch(favoriteRepositoryProvider);
      if (repository == null) throw const AppFailure(FailureKind.cancelled);
      return repository.info(id);
    });
void refreshFavorites(WidgetRef ref) {
  ref.read(favoriteRevisionProvider.notifier).bump();
  ref.invalidate(favoriteCategoriesProvider);
  ref.invalidate(favoriteStatsProvider);
  ref.invalidate(favoriteInfoProvider);
}

final favoritePrivacyRevisionProvider = StreamProvider.autoDispose<int>((ref) {
  final changes = ref.watch(favoriteRepositoryProvider)?.privacyChanges;
  var revision = 0;
  return changes?.map((_) => ++revision) ?? Stream<int>.value(0);
});
