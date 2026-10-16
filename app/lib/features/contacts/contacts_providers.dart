import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/error/app_failure.dart';
import '../../core/im/im_event.dart';
import '../../core/network/tenant_api.dart';
import '../../providers.dart';
import '../group/data/group_repository.dart';
import '../group/domain/group.dart';
import 'data/contacts_repository.dart';
import 'domain/contact.dart';

final contactsRepositoryProvider = Provider.autoDispose<ContactsRepository?>((
  ref,
) {
  final access = ref.watch(sessionAccessProvider);
  final api = ref.watch(tenantApiProvider);
  if (access == null || api == null) return null;
  return ContactsRepository(ref.watch(imGatewayProvider), access, api);
});
final groupRepositoryProvider = Provider.autoDispose<GroupRepository?>((ref) {
  final access = ref.watch(sessionAccessProvider);
  if (access == null) return null;
  return GroupRepository(ref.watch(imGatewayProvider), access);
});

void watchContactChanges(
  Ref ref,
  ContactsRepository repository,
  Set<ImEventKind> kinds,
) {
  Timer? debounce;
  final subscription = repository.gateway.events.listen((event) {
    if (event.generation != repository.access.generation ||
        !repository.access.isCurrent ||
        !kinds.contains(event.kind)) {
      return;
    }
    debounce?.cancel();
    debounce = Timer(const Duration(milliseconds: 150), () {
      if (ref.mounted) ref.invalidateSelf();
    });
  });
  ref.onDispose(() {
    debounce?.cancel();
    unawaited(subscription.cancel());
  });
}

ContactsRepository requireContacts(Ref ref, Set<ImEventKind> kinds) {
  final repository = ref.watch(contactsRepositoryProvider);
  if (repository == null) throw const AppFailure(FailureKind.cancelled);
  watchContactChanges(ref, repository, kinds);
  return repository;
}

final friendsProvider = FutureProvider.autoDispose<List<Contact>>(
  (ref) => requireContacts(ref, {ImEventKind.contactsChanged}).friends(),
);
final blacklistProvider = FutureProvider.autoDispose<List<Contact>>(
  (ref) => requireContacts(ref, {ImEventKind.contactsChanged}).blocked(),
);
final joinedGroupsProvider = FutureProvider.autoDispose<List<GroupSummary>>(
  (ref) => requireContacts(ref, {ImEventKind.groupsChanged}).groups(),
);
final requestsProvider = FutureProvider.autoDispose
    .family<List<ContactRequest>, ContactRequestKind>(
      (ref, kind) => requireContacts(ref, {
        kind == ContactRequestKind.friend
            ? ImEventKind.contactsChanged
            : ImEventKind.groupsChanged,
      }).requests(kind),
    );
final contactProfileProvider = FutureProvider.autoDispose
    .family<Contact, String>(
      (ref, id) => requireContacts(ref, {
        ImEventKind.contactsChanged,
        ImEventKind.selfProfileChanged,
      }).profile(id),
    );
final groupProfileProvider = FutureProvider.autoDispose
    .family<GroupProfile, String>((ref, id) {
      requireContacts(ref, {ImEventKind.groupsChanged});
      final repository = ref.watch(groupRepositoryProvider);
      if (repository == null) throw const AppFailure(FailureKind.cancelled);
      return repository.profile(id);
    });

void refreshContacts(WidgetRef ref) {
  ref.invalidate(friendsProvider);
  ref.invalidate(blacklistProvider);
  ref.invalidate(joinedGroupsProvider);
  ref.invalidate(requestsProvider);
  ref.invalidate(contactProfileProvider);
  ref.invalidate(groupProfileProvider);
}
