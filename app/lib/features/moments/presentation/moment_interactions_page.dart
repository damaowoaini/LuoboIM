import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/error/app_failure.dart';
import '../../../design/components/feedback.dart';
import '../../contacts/presentation/contact_widgets.dart';
import '../data/moments_repository.dart';
import '../domain/moment.dart';
import '../moments_providers.dart';
import 'moment_widgets.dart';

final momentInteractionsProvider =
    Provider.autoDispose<MomentInteractionsController?>((ref) {
      final repository = ref.watch(momentsRepositoryProvider);
      if (repository == null) return null;
      final controller = MomentInteractionsController(repository);
      watchMomentEvents(ref, repository, () => unawaited(controller.refresh()));
      ref.onDispose(controller.dispose);
      controller.refresh();
      return controller;
    });

class MomentInteractionsController extends ChangeNotifier {
  MomentInteractionsController(this.repository) {
    _invalidation = repository.api.access.session.invalidations.listen((_) {
      _request++;
      items = [];
      _notify();
    });
  }
  final MomentsRepository repository;
  late final StreamSubscription<int> _invalidation;
  List<MomentInteraction> items = [];
  int _page = 0, _request = 0;
  bool loading = false, more = true, clearing = false, _disposed = false;
  AppFailure? failure;
  bool get usable => !_disposed && repository.isCurrent;
  bool _current(int request) => usable && request == _request;
  void _notify() {
    if (!_disposed) notifyListeners();
  }

  Future<void> refresh() async {
    if (!usable || clearing) return;
    final request = ++_request;
    loading = true;
    failure = null;
    _notify();
    await _load(request, 1);
  }

  Future<void> next() async {
    if (!usable || loading || !more || clearing) return;
    loading = true;
    failure = null;
    _notify();
    await _load(_request, _page + 1);
  }

  Future<void> _load(int request, int page) async {
    try {
      final result = await repository.interactions(page: page);
      if (!_current(request)) return;
      final previous = page == 1 ? <MomentInteraction>[] : items;
      final seen = previous.map((item) => item.key).toSet();
      final additions = result.where((item) => seen.add(item.key)).toList();
      if (result.length == 20 && additions.isEmpty) {
        throw const AppFailure(FailureKind.dataChanged);
      }
      items = List.unmodifiable([...previous, ...additions]);
      more = result.length == 20;
      _page = page;
      // Read the count only after rendering a successful first page. Clearing
      // the list is a separate explicit action, so pagination remains stable.
      if (page == 1) await repository.clearUnread(type: 1);
    } on AppFailure catch (error) {
      if (_current(request)) failure = error;
    } finally {
      if (_current(request)) {
        loading = false;
        _notify();
      }
    }
  }

  Future<void> clear() async {
    if (!usable || clearing || loading) return;
    clearing = true;
    _notify();
    try {
      await repository.clearUnread(type: 3);
      if (!usable) return;
      _request++;
      items = [];
      _page = 0;
      more = false;
      failure = null;
    } finally {
      clearing = false;
      _notify();
    }
  }

  @override
  void dispose() {
    _disposed = true;
    _request++;
    unawaited(_invalidation.cancel());
    super.dispose();
  }
}

class MomentInteractionsPage extends ConsumerWidget {
  const MomentInteractionsPage({super.key});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final controller = ref.watch(momentInteractionsProvider);
    if (controller == null) return const SizedBox.shrink();
    return ListenableBuilder(
      listenable: controller,
      builder: (context, _) => Scaffold(
        appBar: AppBar(
          title: const Text('互动消息'),
          actions: [
            TextButton(
              onPressed: controller.loading || controller.clearing
                  ? null
                  : () async {
                      if (!await confirmAction(
                            context,
                            '清空互动消息？',
                            '将清空当前互动记录和未读数，动态内容会保留。',
                            action: '清空',
                          ) ||
                          !controller.usable) {
                        return;
                      }
                      try {
                        await controller.clear();
                        ref.invalidate(momentUnreadProvider);
                      } catch (error) {
                        if (context.mounted && controller.usable) {
                          showNotice(
                            context,
                            error is AppFailure ? error.message : '清空失败，请重试',
                          );
                        }
                      }
                    },
              child: const Text('清空'),
            ),
          ],
        ),
        body: RefreshIndicator(
          onRefresh: () async {
            await controller.refresh();
            ref.invalidate(momentUnreadProvider);
          },
          child: ListView.builder(
            physics: const AlwaysScrollableScrollPhysics(),
            itemCount: controller.items.length + 1,
            itemBuilder: (context, index) {
              if (index == controller.items.length) {
                if (controller.loading || controller.clearing) {
                  return const Padding(
                    padding: EdgeInsets.all(24),
                    child: Center(child: CircularProgressIndicator()),
                  );
                }
                if (controller.failure != null) {
                  return ErrorPanel(
                    error: controller.failure!,
                    retry: controller.items.isEmpty
                        ? controller.refresh
                        : controller.next,
                  );
                }
                if (controller.items.isEmpty) {
                  return const Padding(
                    padding: EdgeInsets.all(48),
                    child: Center(child: Text('还没有新的互动')),
                  );
                }
                return Center(
                  child: controller.more
                      ? TextButton(
                          onPressed: controller.next,
                          child: const Text('加载更多'),
                        )
                      : const Padding(
                          padding: EdgeInsets.all(24),
                          child: Text('已经到底了'),
                        ),
                );
              }
              final entry = controller.items[index], moment = entry.moment;
              return Card(
                elevation: 0,
                child: ListTile(
                  leading: ContactAvatar(uri: entry.actor.avatar),
                  title: Text(switch (entry.type) {
                    1 => '${entry.actor.displayName} 点赞了动态',
                    2 => '${entry.actor.displayName} 提到了你',
                    3 => '${entry.actor.displayName} 评论了动态',
                    _ => '动态有新的互动',
                  }),
                  subtitle: Text(
                    '${moment.text.isEmpty ? '媒体动态' : moment.text}\n${momentDate(entry.createdAt)}',
                    maxLines: 3,
                    overflow: TextOverflow.ellipsis,
                  ),
                  trailing: const Icon(Icons.chevron_right),
                  onTap: () => context.push(
                    '/moments/item/${Uri.encodeComponent(moment.id)}',
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
