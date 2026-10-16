import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/error/app_failure.dart';
import '../contacts_providers.dart';
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
  Future<void> submit(List<Contact> all) async {
    final contacts = ref.read(contactsRepositoryProvider);
    final groups = ref.read(groupRepositoryProvider);
    if (contacts == null || groups == null || busy || selected.isEmpty) return;
    final chosen = all.where((user) => selected.contains(user.id)).toList();
    if (chosen.isEmpty) return;
    if (!widget.createConversation) {
      Navigator.pop(context, chosen);
      return;
    }
    String? name;
    if (chosen.length > 1) {
      name = await contactInput(
        context,
        '群聊名称',
        value: chosen.take(3).map((user) => user.displayName).join('、'),
      );
      if (name == null ||
          name.isEmpty ||
          !mounted ||
          !contacts.access.isCurrent) {
        return;
      }
    }
    setState(() => busy = true);
    try {
      final id = chosen.length == 1
          ? await contacts.conversation(chosen.single.id)
          : await groups.create(name!, chosen.map((user) => user.id).toList());
      if (mounted && contacts.access.isCurrent) {
        refreshContacts(ref);
        context.go('/chat/${Uri.encodeComponent(id)}');
      }
    } on AppFailure catch (error) {
      if (mounted && contacts.access.isCurrent) {
        contactNotice(context, error.message);
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final repository = ref.watch(contactsRepositoryProvider);
    ref.watch(groupRepositoryProvider);
    if (repository == null) return const SizedBox.shrink();
    final friends = ref.watch(friendsProvider);
    return PopScope(
      canPop: !busy,
      child: Scaffold(
        appBar: AppBar(
          title: Text(widget.title),
          actions: [
            TextButton(
              onPressed: !busy && selected.isNotEmpty && friends.hasValue
                  ? () => submit(friends.requireValue)
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
                onChanged: (value) =>
                    setState(() => query = value.trim().toLowerCase()),
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
                            : (value) => setState(() {
                                if (value == true) {
                                  selected.add(user.id);
                                } else {
                                  selected.remove(user.id);
                                }
                              }),
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
