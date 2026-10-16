import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/error/app_failure.dart';
import '../contacts_providers.dart';
import '../domain/contact.dart';
import 'contact_widgets.dart';

class ContactRequestsPage extends ConsumerStatefulWidget {
  const ContactRequestsPage({super.key, required this.kind});
  final ContactRequestKind kind;
  @override
  ConsumerState<ContactRequestsPage> createState() =>
      _ContactRequestsPageState();
}

class _ContactRequestsPageState extends ConsumerState<ContactRequestsPage> {
  String? busy;
  Future<void> handle(ContactRequest request, bool accept) async {
    final repository = ref.read(contactsRepositoryProvider);
    if (repository == null || busy != null) return;
    var reason = '';
    if (!accept) {
      final input = await contactInput(
        context,
        '拒绝申请',
        hint: '可填写拒绝原因',
        maxLength: 256,
        lines: 3,
      );
      if (input == null || !mounted || !repository.access.isCurrent) return;
      reason = input;
    }
    setState(() => busy = request.key);
    try {
      await repository.handle(request, accept: accept, reason: reason);
      if (mounted && repository.access.isCurrent) {
        refreshContacts(ref);
        contactNotice(context, accept ? '申请已通过' : '已拒绝申请');
      }
    } on AppFailure catch (error) {
      if (mounted && repository.access.isCurrent) {
        contactNotice(context, error.message);
      }
    } finally {
      if (mounted) setState(() => busy = null);
    }
  }

  @override
  Widget build(BuildContext context) {
    final value = ref.watch(requestsProvider(widget.kind));
    ref.watch(contactsRepositoryProvider);
    return Scaffold(
      appBar: AppBar(
        title: Text(widget.kind == ContactRequestKind.friend ? '新的朋友' : '群申请'),
      ),
      body: value.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ContactProblem(
          error: e,
          retry: () => ref.invalidate(requestsProvider(widget.kind)),
        ),
        data: (items) => items.isEmpty
            ? const Center(child: Text('暂无申请'))
            : RefreshIndicator(
                onRefresh: () async {
                  ref.invalidate(requestsProvider(widget.kind));
                  await ref.read(requestsProvider(widget.kind).future);
                },
                child: ListView.builder(
                  padding: const EdgeInsets.all(16),
                  itemCount: items.length,
                  itemBuilder: (context, index) {
                    final item = items[index];
                    return Card(
                      elevation: 0,
                      child: Padding(
                        padding: const EdgeInsets.all(12),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            ListTile(
                              contentPadding: EdgeInsets.zero,
                              leading: ContactAvatar(uri: item.avatar),
                              title: Text(item.name),
                              subtitle: Text(
                                item.kind == ContactRequestKind.group
                                    ? '${item.incoming ? '申请加入' : '已申请'} ${item.groupName ?? item.groupId}'
                                    : item.incoming
                                    ? '收到的好友申请'
                                    : '发出的好友申请',
                              ),
                              onTap: () =>
                                  context.push(contactPath(item.userId)),
                            ),
                            if (item.reason.isNotEmpty)
                              Padding(
                                padding: const EdgeInsets.symmetric(
                                  vertical: 8,
                                ),
                                child: Text(item.reason),
                              ),
                            if (item.incoming && item.pending)
                              Wrap(
                                spacing: 12,
                                children: [
                                  OutlinedButton(
                                    onPressed: busy == null
                                        ? () => handle(item, false)
                                        : null,
                                    child: const Text('拒绝'),
                                  ),
                                  FilledButton(
                                    onPressed: busy == null
                                        ? () => handle(item, true)
                                        : null,
                                    child: Text(
                                      busy == item.key ? '处理中…' : '通过',
                                    ),
                                  ),
                                ],
                              )
                            else
                              Text(
                                item.status == 1
                                    ? '已通过'
                                    : item.status == -1
                                    ? '已拒绝'
                                    : '等待验证',
                                style: TextStyle(
                                  color: Theme.of(context)
                                      .colorScheme
                                      .onSurfaceVariant,
                                ),
                              ),
                          ],
                        ),
                      ),
                    );
                  },
                ),
              ),
      ),
    );
  }
}
