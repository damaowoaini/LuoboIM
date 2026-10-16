import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/error/app_failure.dart';
import '../contacts_providers.dart';
import '../data/contacts_repository.dart';
import '../../group/data/group_repository.dart';
import '../../group/presentation/create_group_page.dart';
import '../domain/contact.dart';
import 'contact_widgets.dart';

Future<List<Contact>?> selectContacts(
  BuildContext context, {
  String title = '选择好友',
  Set<String> excluded = const {},
}) => Navigator.of(context).push<List<Contact>>(
  MaterialPageRoute(
    builder: (_) => SelectContactsPage(title: title, excluded: excluded),
  ),
);

Future<void> startConversation(BuildContext context) async {
  await Navigator.of(context).push<void>(
    MaterialPageRoute(
      builder: (_) =>
          const SelectContactsPage(createConversation: true, title: '发起聊天'),
    ),
  );
}

class SelectContactsPage extends ConsumerStatefulWidget {
  const SelectContactsPage({
    super.key,
    required this.title,
    this.excluded = const {},
    this.createConversation = false,
  });
  final String title;
  final Set<String> excluded;
  final bool createConversation;
  @override
  ConsumerState<SelectContactsPage> createState() => _SelectContactsPageState();
}

class _SelectContactsPageState extends ConsumerState<SelectContactsPage> {
  final selected = <String>{};
  String query = '';
  bool busy = false;
  ContactsRepository? selectionOwner;
  GroupRepository? selectionGroups;
  int view = 0;
  bool editingGroup = false;

  bool current(ContactsRepository contacts, GroupRepository groups) =>
      mounted && identical(contacts, selectionOwner) && identical(groups, selectionGroups) &&
      identical(contacts, ref.read(contactsRepositoryProvider)) &&
      identical(groups, ref.read(groupRepositoryProvider)) &&
      contacts.access.isCurrent && groups.access.isCurrent &&
      identical(contacts.access.credentials, contacts.access.session.state.credentials) &&
      identical(groups.access.credentials, contacts.access.credentials);

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (!editingGroup && ModalRoute.of(context)?.isCurrent == false) ++view;
  }

  Future<void> submit(List<Contact> all, ContactsRepository contacts, GroupRepository groups,
      int original) async {
    final route = ModalRoute.of(context);
    bool active() => current(contacts, groups) && original == view && route?.isCurrent == true;
    if (!active() || busy || selected.isEmpty) return;
    final chosen = all.where((user) => selected.contains(user.id)).toList();
    if (chosen.isEmpty) return;
    if (!widget.createConversation) {
      Navigator.pop(context, chosen);
      return;
    }
    if (chosen.length > 1) {
      final owner = contacts.access.credentials;
      setState(() => busy = true);
      editingGroup = true;
      // Selection is still editable by returning before create is dispatched.
      // Only the dedicated editor performs explicit group creation.
      try {
        await Navigator.of(context).push<void>(MaterialPageRoute(
          builder: (_) => CreateGroupPage(members: List.unmodifiable(chosen), owner: groups),
        ));
      } finally {
        editingGroup = false;
        if (mounted && identical(contacts.access.session.state.credentials, owner)) {
          setState(() => busy = false);
        }
      }
      return;
    }
    setState(() => busy = true);
    try {
      final id = await contacts.conversation(chosen.single.id);
      if (mounted && active()) {
        refreshContacts(ref);
        context.go('/chat/${Uri.encodeComponent(id)}');
      }
    } on AppFailure catch (error) {
      if (mounted && active()) {
        contactNotice(context, error.message);
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final repository = ref.watch(contactsRepositoryProvider);
    final groups = ref.watch(groupRepositoryProvider);
    if (repository == null || groups == null) return const SizedBox.shrink();
    selectionOwner ??= repository;
    selectionGroups ??= groups;
    if (!current(repository, groups)) {
      return Scaffold(appBar: AppBar(title: Text(widget.title)),
        body: const Center(child: Text('会话已变化，请返回后重新选择好友')));
    }
    final renderedView = view;
    bool renderedCurrent() => current(repository, groups) && renderedView == view &&
      ModalRoute.of(context)?.isCurrent == true;
    final friends = ref.watch(friendsProvider);
    return PopScope(
      canPop: !busy,
      child: Scaffold(
        appBar: AppBar(
          title: Text(widget.title),
          actions: [
            TextButton(
              onPressed: !busy && selected.isNotEmpty && friends.hasValue
                  ? () => submit(friends.requireValue, repository, groups, renderedView)
                  : null,
              child: Text(busy ? '处理中…' : '确定 (${selected.length})'),
            ),
          ],
        ),
        body: Column(
          children: [
            Padding(
              padding: const EdgeInsets.all(16),
              child: TextField(
                onChanged: (value) {
                  if (renderedCurrent()) setState(() => query = value.trim().toLowerCase());
                },
                decoration: const InputDecoration(
                  prefixIcon: Icon(Icons.search),
                  hintText: '搜索好友',
                ),
              ),
            ),
            Expanded(
              child: friends.when(
                loading: () => const Center(child: CircularProgressIndicator()),
                error: (e, _) => ContactProblem(
                  error: e,
                  retry: () => ref.invalidate(friendsProvider),
                ),
                data: (all) {
                  final visible = all
                      .where(
                        (user) =>
                            user.id != repository.access.credentials.userId &&
                            !widget.excluded.contains(user.id) &&
                            '${user.displayName} ${user.name} ${user.id}'
                                .toLowerCase()
                                .contains(query),
                      )
                      .toList();
                  if (visible.isEmpty) {
                    return const Center(child: Text('没有可选的好友'));
                  }
                  return ListView.builder(
                    itemCount: visible.length,
                    itemBuilder: (context, index) {
                      final user = visible[index];
                      return CheckboxListTile(
                        value: selected.contains(user.id),
                        onChanged: busy
                            ? null
                            : (value) {
                              if (!renderedCurrent()) return;
                              setState(() {
                                if (value == true) {
                                  selected.add(user.id);
                                } else {
                                  selected.remove(user.id);
                                }
                              });
                            },
                        secondary: ContactAvatar(uri: user.avatar),
                        title: Text(user.displayName),
                      );
                    },
                  );
                },
              ),
            ),
          ],
        ),
      ),
    );
  }
}
