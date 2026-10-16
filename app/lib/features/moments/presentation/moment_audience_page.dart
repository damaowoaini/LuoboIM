import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../design/components/feedback.dart';
import '../../contacts/contacts_providers.dart';
import '../../contacts/presentation/contact_widgets.dart';
import '../domain/moment.dart';

final class MomentAudience {
  MomentAudience(
    this.visibility,
    Iterable<String> users,
    Iterable<String> groups,
  ) : users = Set.unmodifiable(users),
      groups = Set.unmodifiable(groups);
  final MomentVisibility visibility;
  final Set<String> users, groups;
}

class MomentAudiencePage extends ConsumerStatefulWidget {
  const MomentAudiencePage({super.key, required this.initial});
  final MomentAudience initial;
  @override
  ConsumerState<MomentAudiencePage> createState() => _MomentAudiencePageState();
}

class _MomentAudiencePageState extends ConsumerState<MomentAudiencePage> {
  late MomentVisibility visibility = widget.initial.visibility;
  late final users = {...widget.initial.users},
      groups = {...widget.initial.groups};
  String query = '';
  void toggle(Set<String> values, String id, bool selected) => setState(() {
    if (selected) {
      values.add(id);
    } else {
      values.remove(id);
    }
  });
  @override
  Widget build(BuildContext context) {
    final friends = ref.watch(friendsProvider),
        joined = ref.watch(joinedGroupsProvider);
    final repository = ref.watch(contactsRepositoryProvider);
    final restricted =
        visibility == MomentVisibility.included ||
        visibility == MomentVisibility.excluded;
    final valid = !restricted || users.isNotEmpty || groups.isNotEmpty;
    return Scaffold(
      appBar: AppBar(
        title: const Text('谁可以看'),
        actions: [
          TextButton(
            onPressed: valid && repository?.access.isCurrent == true
                ? () => Navigator.pop(
                    context,
                    MomentAudience(visibility, users, groups),
                  )
                : null,
            child: const Text('完成'),
          ),
        ],
      ),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          RadioGroup<MomentVisibility>(
            groupValue: visibility,
            onChanged: (value) {
              if (value != null) setState(() => visibility = value);
            },
            child: Column(
              children: [
                for (final value in MomentVisibility.values)
                  RadioListTile<MomentVisibility>(
                    value: value,
                    title: Text(value.label),
                  ),
              ],
            ),
          ),
          if (restricted) ...[
            const Divider(),
            Text(
              visibility == MomentVisibility.included
                  ? '选中的好友及群成员可见'
                  : '选中的好友及群成员不可见',
            ),
            const SizedBox(height: 8),
            Text('已选 ${users.length} 位好友、${groups.length} 个群；以发布时的群成员为准'),
            const SizedBox(height: 12),
            TextField(
              decoration: const InputDecoration(hintText: '搜索好友或群'),
              onChanged: (value) =>
                  setState(() => query = value.trim().toLowerCase()),
            ),
            const SizedBox(height: 12),
            friends.when(
              loading: () => const LinearProgressIndicator(),
              error: (error, _) => ErrorPanel(
                error: error,
                retry: () => ref.invalidate(friendsProvider),
              ),
              data: (values) => Column(
                children: [
                  const ListTile(title: Text('好友')),
                  for (final user in values.where(
                    (user) =>
                        user.id != repository?.access.credentials.userId &&
                        '${user.displayName} ${user.id}'.toLowerCase().contains(
                          query,
                        ),
                  ))
                    CheckboxListTile(
                      value: users.contains(user.id),
                      title: Text(user.displayName),
                      secondary: ContactAvatar(uri: user.avatar),
                      onChanged: (value) =>
                          toggle(users, user.id, value == true),
                    ),
                ],
              ),
            ),
            joined.when(
              loading: () => const LinearProgressIndicator(),
              error: (error, _) => ErrorPanel(
                error: error,
                retry: () => ref.invalidate(joinedGroupsProvider),
              ),
              data: (values) => Column(
                children: [
                  const ListTile(title: Text('我加入的群')),
                  for (final group in values.where(
                    (group) =>
                        group.status == 0 &&
                        '${group.name} ${group.id}'.toLowerCase().contains(
                          query,
                        ),
                  ))
                    CheckboxListTile(
                      value: groups.contains(group.id),
                      title: Text(group.name),
                      subtitle: Text('${group.members} 位成员'),
                      secondary: ContactAvatar(uri: group.avatar, group: true),
                      onChanged: (value) =>
                          toggle(groups, group.id, value == true),
                    ),
                ],
              ),
            ),
          ],
        ],
      ),
    );
  }
}
