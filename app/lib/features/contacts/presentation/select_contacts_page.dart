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
    this.createGroup = false,
    this.initialMembers = const [],
    this.expectedOwner,
    this.expectedGroups,
  });
  final String title;
  final Set<String> excluded;
  final bool createConversation;
  final bool createGroup;
  final List<Contact> initialMembers;
  final ContactsRepository? expectedOwner;
  final GroupRepository? expectedGroups;
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
  late final List<Contact> fixedMembers;

  @override
  void initState() {
    super.initState();
    fixedMembers = List.unmodifiable(widget.initialMembers);
    selectionOwner = widget.expectedOwner;
    selectionGroups = widget.expectedGroups;
  }

  bool current(ContactsRepository contacts, GroupRepository groups) =>
      mounted && identical(contacts, selectionOwner) && identical(groups, selectionGroups) &&
      widget.initialMembers.length == fixedMembers.length &&
      widget.initialMembers.indexed.every((entry) => entry.$2.id == fixedMembers[entry.$1].id) &&
      (widget.expectedOwner == null || identical(widget.expectedOwner, contacts)) &&
      (widget.expectedGroups == null || identical(widget.expectedGroups, groups)) &&
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
    final fixedIds = fixedMembers.map((user) => user.id).toSet();
    final chosen = [
      ...fixedMembers,
      ...all.where((user) => selected.contains(user.id) && !fixedIds.contains(user.id)),
    ];
    if (chosen.isEmpty) return;
    if (!widget.createConversation && !widget.createGroup) {
      Navigator.pop(context, chosen);
      return;
    }
    if (widget.createGroup && chosen.length < 2) return;
    if (widget.createGroup || chosen.length > 1) {
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
    final fixedIds = fixedMembers.map((user) => user.id).toSet();
    if (!current(repository, groups) ||
        fixedIds.length != fixedMembers.length ||
        fixedIds.any((id) => id.isEmpty || id == repository.access.credentials.userId) ||
        (widget.createGroup && (widget.expectedOwner == null || widget.expectedGroups == null))) {
      return Scaffold(appBar: AppBar(title: Text(widget.title)),
        body: const Center(child: Text('会话已变化，请返回后重新选择好友')));
    }
    final renderedView = view;
    final count = fixedMembers.length + selected.length;
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
              onPressed: !busy && selected.isNotEmpty && friends.hasValue &&
                  (!widget.createGroup || count >= 2)
                  ? () => submit(friends.requireValue, repository, groups, renderedView)
                  : null,
              child: Text(busy ? '处理中…' : '确定 ($count)'),
            ),
          ],
        ),
        body: Column(
          children: [
            if (widget.createGroup)
              ListTile(
                leading: const Icon(Icons.person_outline),
                title: const Text('我已作为群主加入'),
                subtitle: Text('ID：${repository.access.credentials.userId}'),
              ),
            for (final member in fixedMembers)
              CheckboxListTile(
                key: ValueKey('fixed-group-member-${member.id}'),
                value: true,
                onChanged: null,
                title: Text(member.displayName),
                subtitle: Text('ID：${member.id}（当前联系人）'),
                secondary: ContactAvatar(uri: member.avatar),
              ),
            Padding(
              padding: const EdgeInsets.all(16),
              child: TextField(
                onChanged: (value) {
                  if (renderedCurrent()) setState(() => query = value.trim().toLowerCase());
                },
                decoration: InputDecoration(
                  prefixIcon: const Icon(Icons.search),
                  hintText: '搜索好友',
                  helperText: widget.createGroup
                    ? fixedMembers.isEmpty ? '至少选择两位好友' : '再选择至少一位好友'
                    : null,
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
                            !fixedIds.contains(user.id) &&
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
                        subtitle: widget.createGroup ? Text('ID：${user.id}') : null,
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
