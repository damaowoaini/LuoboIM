import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/error/app_failure.dart';
import '../../group/domain/group.dart';
import '../contacts_providers.dart';
import '../domain/contact.dart';
import 'contact_widgets.dart';

enum ContactSearchKind { userId, phoneNumber, groupId }

class ContactSearchPage extends ConsumerStatefulWidget {
  const ContactSearchPage({super.key, this.initialKind = ContactSearchKind.userId});
  final ContactSearchKind initialKind;
  @override
  ConsumerState<ContactSearchPage> createState() => _ContactSearchPageState();
}

class _ContactSearchPageState extends ConsumerState<ContactSearchPage> {
  final input = TextEditingController();
  final users = <Contact>[];
  final groups = <GroupSummary>[];
  late ContactSearchKind kind;
  bool busy = false, searched = false;
  AppFailure? failure;
  int ticket = 0;
  String observedInput = '';
  @override
  void initState() {
    super.initState();
    kind = widget.initialKind;
    input.addListener(inputChanged);
  }

  @override
  void didUpdateWidget(covariant ContactSearchPage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.initialKind != widget.initialKind) {
      kind = widget.initialKind;
      // A route mode is a new lookup intent. Keep the explicit input but never
      // re-use another namespace's result or automatically repeat its request.
      invalidateLookup();
    }
  }

  void inputChanged() {
    if (observedInput == input.text) return;
    observedInput = input.text;
    invalidateLookup();
  }

  void invalidateLookup() {
    ticket++;
    if (!mounted) return;
    setState(() {
      users.clear();
      groups.clear();
      failure = null;
      searched = false;
      busy = false;
    });
  }

  @override
  void dispose() {
    ticket++;
    input.removeListener(inputChanged);
    input.dispose();
    super.dispose();
  }

  Future<void> search() async {
    final repository = ref.read(contactsRepositoryProvider);
    if (repository == null || busy) return;
    final query = input.text.trim();
    if (query.isEmpty) return;
    final lookupKind = kind;
    final current = ++ticket;
    bool isCurrent() =>
        mounted &&
        current == ticket &&
        lookupKind == kind &&
        input.text.trim() == query &&
        identical(ref.read(contactsRepositoryProvider), repository) &&
        repository.access.isCurrent;
    setState(() {
      busy = true;
      failure = null;
      users.clear();
      groups.clear();
    });
    try {
      if (lookupKind == ContactSearchKind.groupId) {
        final values = await repository.searchGroups(query);
        if (isCurrent()) {
          setState(() => groups.addAll(values));
        }
      } else {
        final result = await repository.lookupUser(
          query,
          lookupKind == ContactSearchKind.userId
              ? ContactLookupMode.userId
              : ContactLookupMode.phoneNumber,
        );
        if (isCurrent()) {
          setState(() => users.addAll(result.items));
        }
      }
    } on AppFailure catch (error) {
      if (isCurrent()) {
        setState(() => failure = error);
      }
    } finally {
      if (isCurrent()) {
        setState(() {
          busy = false;
          searched = true;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    ref.listen(contactsRepositoryProvider, (previous, next) {
      if (!identical(previous, next)) invalidateLookup();
    });
    if (ref.watch(contactsRepositoryProvider) == null) {
      return const SizedBox.shrink();
    }
    return Scaffold(
      appBar: AppBar(title: const Text('添加好友或群')),
      body: ListView(
        padding: const EdgeInsets.all(20),
        children: [
          DropdownButtonFormField<ContactSearchKind>(
            key: ValueKey(kind),
            initialValue: kind,
            isExpanded: true,
            decoration: const InputDecoration(labelText: '查询方式'),
            items: const [
              DropdownMenuItem(value: ContactSearchKind.userId, child: Text('账号 ID')),
              DropdownMenuItem(
                value: ContactSearchKind.phoneNumber,
                child: Text('手机号'),
              ),
              DropdownMenuItem(value: ContactSearchKind.groupId, child: Text('群 ID')),
            ],
            onChanged: (value) {
              if (value == null || value == kind) return;
              kind = value;
              invalidateLookup();
            },
          ),
          const SizedBox(height: 20),
          TextField(
            controller: input,
            onSubmitted: (_) => search(),
            decoration: InputDecoration(
              hintText: switch (kind) {
                ContactSearchKind.userId => '输入完整账号 ID',
                ContactSearchKind.phoneNumber => '输入完整手机号',
                ContactSearchKind.groupId => '输入完整群 ID',
              },
              prefixIcon: const Icon(Icons.search),
            ),
          ),
          const SizedBox(height: 12),
          FilledButton(
            onPressed: busy ? null : () => search(),
            child: const Text('搜索'),
          ),
          const SizedBox(height: 24),
          if (failure != null) ContactProblem(error: failure!, retry: search),
          for (final user in users)
            ListTile(
              leading: ContactAvatar(uri: user.avatar),
              title: Text(user.displayName),
              subtitle: Text('ID：${user.id}'),
              trailing: const Icon(Icons.chevron_right),
              onTap: () => context.push(contactPath(user.id)),
            ),
          for (final group in groups)
            ListTile(
              leading: ContactAvatar(uri: group.avatar, group: true),
              title: Text(group.name),
              subtitle: Text('${group.members} 人'),
              trailing: const Icon(Icons.chevron_right),
              onTap: () => context.push(groupPath(group.id)),
            ),
          if (busy)
            const Center(child: CircularProgressIndicator())
          else if (searched &&
              failure == null &&
              users.isEmpty &&
              groups.isEmpty)
            const Center(child: Text('没有找到匹配的结果')),
        ],
      ),
    );
  }
}
