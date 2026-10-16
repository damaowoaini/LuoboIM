import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/error/app_failure.dart';
import '../../../design/components/feedback.dart';
import '../data/moments_repository.dart';
import '../domain/moment.dart';
import '../moments_providers.dart';
import 'moment_widgets.dart';

Future<void> momentComment(
  BuildContext context,
  MomentsRepository repository,
  Moment moment,
  MomentComment? reply,
  Future<void> Function(Moment) apply,
) async {
  await promptText(
    context,
    reply == null ? '评论动态' : '回复 ${reply.author.displayName}',
    hint: '写下你的想法',
    maxLength: 2000,
    lines: 4,
    save: (text) async {
      final result = await repository.comment(
        moment.id,
        text,
        replyTo: reply?.author.id ?? '',
      );
      if (repository.isCurrent) await apply(result);
    },
  );
}

Future<bool> momentDeleteComment(
  BuildContext context,
  MomentsRepository repository,
  Moment moment,
  MomentComment comment,
  Future<void> Function(Moment) apply,
) async {
  if (!await confirmAction(context, '删除评论？', '删除后无法恢复。', action: '删除') ||
      !repository.isCurrent) {
    return false;
  }
  final result = await repository.deleteComment(moment, comment);
  if (repository.isCurrent) await apply(result);
  return true;
}

class MomentsPage extends ConsumerWidget {
  const MomentsPage({super.key, this.author});
  final String? author;
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final controller = ref.watch(momentListProvider(author));
    final unread = ref.watch(momentUnreadProvider);
    if (controller == null) return const SizedBox.shrink();
    final repository = controller.repository;
    Future<void> guard(Future<void> Function() action) async {
      try {
        await action();
      } catch (error) {
        if (context.mounted && repository.isCurrent) {
          showNotice(
            context,
            error is AppFailure ? error.message : '操作未完成，请重试',
          );
        }
      }
    }

    Future<void> open(Moment moment) async {
      await context.push('/moments/item/${Uri.encodeComponent(moment.id)}');
      if (context.mounted && repository.isCurrent) await controller.refresh();
    }

    return ListenableBuilder(
      listenable: controller,
      builder: (context, _) => Scaffold(
        appBar: AppBar(
          title: Text(
            author == null
                ? '朋友圈'
                : author == repository.owner
                ? '我的动态'
                : '个人动态',
          ),
          actions: [
            IconButton(
              tooltip: '互动消息',
              onPressed: () async {
                await context.push('/moments/interactions');
                ref.invalidate(momentUnreadProvider);
              },
              icon: Badge(
                isLabelVisible: (unread.value ?? 0) > 0,
                label: Text('${unread.value ?? 0}'),
                child: const Icon(Icons.notifications_none),
              ),
            ),
            IconButton(
              tooltip: '发布动态',
              onPressed: () async {
                await context.push('/moments/publish');
                if (context.mounted && repository.isCurrent) {
                  await controller.refresh();
                }
              },
              icon: const Icon(Icons.add_circle_outline),
            ),
            if (author == null)
              IconButton(
                tooltip: '我的动态',
                onPressed: () => context.push(
                  '/moments/user/${Uri.encodeComponent(repository.owner)}',
                ),
                icon: const Icon(Icons.person_outline),
              ),
          ],
        ),
        body: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 780),
            child: RefreshIndicator(
              onRefresh: () async {
                await controller.refresh();
                ref.invalidate(momentUnreadProvider);
              },
              child: ListView.builder(
                physics: const AlwaysScrollableScrollPhysics(),
                padding: const EdgeInsets.symmetric(vertical: 10),
                itemCount: controller.items.length + 1,
                itemBuilder: (context, index) {
                  if (index == controller.items.length) {
                    if (controller.loading) {
                      return const Padding(
                        padding: EdgeInsets.all(28),
                        child: Center(child: CircularProgressIndicator()),
                      );
                    }
                    if (controller.failure != null) {
                      return ErrorPanel(
                        error: controller.failure!,
                        retry: controller.retry,
                      );
                    }
                    if (controller.items.isEmpty) {
                      return const Padding(
                        padding: EdgeInsets.all(48),
                        child: Center(child: Text('还没有动态，分享一个新发现吧')),
                      );
                    }
                    return Padding(
                      padding: const EdgeInsets.all(16),
                      child: Center(
                        child: controller.more
                            ? TextButton(
                                onPressed: controller.next,
                                child: const Text('加载更多'),
                              )
                            : const Text('已经到底了'),
                      ),
                    );
                  }
                  final moment = controller.items[index];
                  return MomentCard(
                    moment: moment,
                    owner: repository.owner,
                    access: repository.api.access,
                    busy: controller.busy.contains(moment.id),
                    onOpen: () => open(moment),
                    onLike: () => guard(
                      () => controller.update(
                        moment.id,
                        () => repository.like(
                          moment.id,
                          !moment.likedBy(repository.owner),
                        ),
                      ),
                    ),
                    onComment: (reply) => momentComment(
                      context,
                      repository,
                      moment,
                      reply,
                      (updated) =>
                          controller.update(moment.id, () async => updated),
                    ),
                    onDelete: () => guard(() async {
                      if (!await confirmAction(
                            context,
                            '删除这条动态？',
                            '动态及其互动将被删除，无法恢复。',
                            action: '删除',
                          ) ||
                          !repository.isCurrent) {
                        return;
                      }
                      await repository.delete(moment);
                      controller.remove(moment.id);
                    }),
                    onDeleteComment: (comment) => guard(() async {
                      await momentDeleteComment(
                        context,
                        repository,
                        moment,
                        comment,
                        (updated) =>
                            controller.update(moment.id, () async => updated),
                      );
                    }),
                  );
                },
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class MomentDetailPage extends ConsumerStatefulWidget {
  const MomentDetailPage({super.key, required this.id});
  final String id;
  @override
  ConsumerState<MomentDetailPage> createState() => _MomentDetailPageState();
}

class _MomentDetailPageState extends ConsumerState<MomentDetailPage> {
  bool busy = false;
  Future<void> run(
    MomentsRepository repository,
    Future<void> Function() action,
  ) async {
    if (busy || !repository.isCurrent) return;
    setState(() => busy = true);
    try {
      await action();
    } catch (error) {
      if (mounted && repository.isCurrent) {
        showNotice(context, error is AppFailure ? error.message : '操作未完成，请重试');
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final repository = ref.watch(momentsRepositoryProvider);
    final value = ref.watch(momentDetailProvider(widget.id));
    if (repository == null) return const SizedBox.shrink();
    Future<void> refresh(Moment _) async {
      ref.invalidate(momentDetailProvider(widget.id));
    }

    return Scaffold(
      appBar: AppBar(title: const Text('动态详情')),
      body: value.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (error, _) => ErrorPanel(
          error: error,
          retry: () => ref.invalidate(momentDetailProvider(widget.id)),
        ),
        data: (moment) => Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 780),
            child: ListView(
              children: [
                MomentCard(
                  moment: moment,
                  owner: repository.owner,
                  access: repository.api.access,
                  busy: busy,
                  detail: true,
                  onOpen: () {},
                  onLike: () => run(repository, () async {
                    await refresh(
                      await repository.like(
                        moment.id,
                        !moment.likedBy(repository.owner),
                      ),
                    );
                  }),
                  onComment: (reply) => momentComment(
                    context,
                    repository,
                    moment,
                    reply,
                    refresh,
                  ),
                  onDelete: () => run(repository, () async {
                    if (!await confirmAction(
                          context,
                          '删除这条动态？',
                          '动态及其互动将被删除，无法恢复。',
                          action: '删除',
                        ) ||
                        !repository.isCurrent) {
                      return;
                    }
                    await repository.delete(moment);
                    if (context.mounted && repository.isCurrent) {
                      Navigator.pop(context, true);
                    }
                  }),
                  onDeleteComment: (comment) => run(repository, () async {
                    await momentDeleteComment(
                      context,
                      repository,
                      moment,
                      comment,
                      refresh,
                    );
                  }),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
