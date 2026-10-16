import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../contacts_providers.dart';
import '../domain/contact.dart';
import 'contact_widgets.dart';
import 'select_contacts_page.dart';

class ContactsPage extends ConsumerStatefulWidget {
  const ContactsPage({super.key});
  @override
  ConsumerState<ContactsPage> createState() => _ContactsPageState();
}

class _ContactsPageState extends ConsumerState<ContactsPage> {
  String query = '';
  @override
  Widget build(BuildContext context) {
    final friends = ref.watch(friendsProvider);
    final requests = ref.watch(requestsProvider(ContactRequestKind.friend));
    final pending =
        requests.value
            ?.where((request) => request.incoming && request.pending)
            .length ??
        0;
    return Scaffold(
      appBar: AppBar(
        title: const Text('通讯录'),
        actions: [
          IconButton(
            tooltip: '添加好友或群',
            onPressed: () => context.push('/contacts/search'),
            icon: const Icon(Icons.person_add_alt),
          ),
          IconButton(
            tooltip: '发起聊天',
            onPressed: () => startConversation(context),
            icon: const Icon(Icons.add_comment_outlined),
          ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: () async {
          refreshContacts(ref);
          await ref.read(friendsProvider.future);
        },
        child: CustomScrollView(
          physics: const AlwaysScrollableScrollPhysics(),
          slivers: [
            SliverPadding(
              padding: const EdgeInsets.all(16),
              sliver: SliverList.list(
                children: [
                  TextField(
                    onChanged: (value) =>
                        setState(() => query = value.trim().toLowerCase()),
                    decoration: const InputDecoration(
                      hintText: '搜索好友昵称、备注或 ID',
                      prefixIcon: Icon(Icons.search),
                    ),
                  ),
                  const SizedBox(height: 16),
                  Card(
                    elevation: 0,
                    child: Column(
                      children: [
                        ListTile(
                          leading: const Icon(Icons.person_add_outlined),
                          title: const Text('新的朋友'),
                          trailing: Badge(
                            isLabelVisible: pending > 0,
                            label: Text('$pending'),
                            child: const Icon(Icons.chevron_right),
                          ),
                          onTap: () =>
                              context.push('/contacts/requests/friend'),
                        ),
                        ListTile(
                          leading: const Icon(Icons.mark_email_unread_outlined),
                          title: const Text('群申请'),
                          trailing: const Icon(Icons.chevron_right),
                          onTap: () => context.push('/contacts/requests/group'),
                        ),
                        ListTile(
                          leading: const Icon(Icons.groups_outlined),
                          title: const Text('我的群聊'),
                          trailing: const Icon(Icons.chevron_right),
                          onTap: () => context.push('/contacts/groups'),
                        ),
                        ListTile(
                          leading: const Icon(Icons.block_outlined),
                          title: const Text('黑名单'),
                          trailing: const Icon(Icons.chevron_right),
                          onTap: () => context.push('/contacts/blocked'),
                        ),
                      ],
                    ),
                  ),
                  const Padding(
                    padding: EdgeInsets.symmetric(horizontal: 8, vertical: 16),
                    child: Text(
                      '好友',
                      style: TextStyle(fontWeight: FontWeight.w600),
                    ),
                  ),
                ],
              ),
            ),
            friends.when(
              loading: () => const SliverToBoxAdapter(
                child: Center(child: CircularProgressIndicator()),
              ),
              error: (error, _) => SliverToBoxAdapter(
                child: ContactProblem(
                  error: error,
                  retry: () => ref.invalidate(friendsProvider),
                ),
              ),
              data: (items) {
                final visible = items
                    .where(
                      (item) =>
                          query.isEmpty ||
                          '${item.displayName} ${item.name} ${item.id}'
                              .toLowerCase()
                              .contains(query),
                    )
                    .toList();
                if (visible.isEmpty) {
                  return SliverToBoxAdapter(
                    child: Padding(
                      padding: const EdgeInsets.all(28),
                      child: Center(
                        child: Text(
                          query.isEmpty ? '还没有好友，点击右上角添加' : '没有找到匹配的好友',
                        ),
                      ),
                    ),
                  );
                }
                return SliverPadding(
                  padding: const EdgeInsets.fromLTRB(20, 0, 20, 20),
                  sliver: SliverList.builder(
                    itemCount: visible.length,
                    itemBuilder: (context, index) {
                      final item = visible[index];
                      return Material(
                        color: Theme.of(context).colorScheme.surface,
                        child: ListTile(
                          leading: ContactAvatar(uri: item.avatar),
                          title: Text(item.displayName),
                          subtitle: item.remark.isEmpty
                              ? null
                              : Text(item.name),
                          onTap: () => context.push(contactPath(item.id)),
                        ),
                      );
                    },
                  ),
                );
              },
            ),
          ],
        ),
      ),
    );
  }
}

class ContactCollectionPage extends ConsumerWidget {
  const ContactCollectionPage({super.key, this.groups = false});
  final bool groups;
  @override
  Widget build(BuildContext context, WidgetRef ref) => Scaffold(
    appBar: AppBar(title: Text(groups ? '我的群聊' : '黑名单')),
    body: groups
        ? ref
              .watch(joinedGroupsProvider)
              .when(
                loading: () => const Center(child: CircularProgressIndicator()),
                error: (e, _) => ContactProblem(
                  error: e,
                  retry: () => ref.invalidate(joinedGroupsProvider),
                ),
                data: (items) => items.isEmpty
                    ? const Center(child: Text('还没有加入群聊'))
                    : RefreshIndicator(
                        onRefresh: () async {
                          ref.invalidate(joinedGroupsProvider);
                          await ref.read(joinedGroupsProvider.future);
                        },
                        child: ListView.builder(
                          itemCount: items.length,
                          itemBuilder: (context, index) {
                            final item = items[index];
                            return ListTile(
                              leading: ContactAvatar(
                                uri: item.avatar,
                                group: true,
                              ),
                              title: Text(item.name),
                              subtitle: Text('${item.members} 人'),
                              onTap: () => context.push(groupPath(item.id)),
                            );
                          },
                        ),
                      ),
              )
        : ref
              .watch(blacklistProvider)
              .when(
                loading: () => const Center(child: CircularProgressIndicator()),
                error: (e, _) => ContactProblem(
                  error: e,
                  retry: () => ref.invalidate(blacklistProvider),
                ),
                data: (items) => items.isEmpty
                    ? const Center(child: Text('黑名单为空'))
                    : ListView.builder(
                        itemCount: items.length,
                        itemBuilder: (context, index) {
                          final item = items[index];
                          return ListTile(
                            leading: ContactAvatar(uri: item.avatar),
                            title: Text(item.displayName),
                            onTap: () => context.push(contactPath(item.id)),
                          );
                        },
                      ),
              ),
  );
}
