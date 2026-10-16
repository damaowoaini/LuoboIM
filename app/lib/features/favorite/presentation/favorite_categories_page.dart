import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/error/app_failure.dart';
import '../../../design/components/feedback.dart';
import '../data/favorite_repository.dart';
import '../domain/favorite.dart';
import '../favorite_providers.dart';
import 'favorite_widgets.dart';

class FavoriteCategoriesPage extends ConsumerStatefulWidget {
  const FavoriteCategoriesPage({super.key});
  @override
  ConsumerState<FavoriteCategoriesPage> createState() =>
      _FavoriteCategoriesPageState();
}

class _FavoriteCategoriesPageState
    extends ConsumerState<FavoriteCategoriesPage> {
  bool busy = false;
  Future<void> run(Future<void> Function(FavoriteRepository) action) async {
    final repository = ref.read(favoriteRepositoryProvider);
    if (repository == null || !repository.isCurrent || busy) return;
    setState(() => busy = true);
    try {
      await action(repository);
      if (mounted && repository.isCurrent) refreshFavorites(ref);
    } on AppFailure catch (error) {
      if (mounted && repository.isCurrent) showNotice(context, error.message);
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> edit(FavoriteCategory category, String action) async {
    final repository = ref.read(favoriteRepositoryProvider);
    if (repository == null || busy || category.isDefault) return;
    switch (action) {
      case 'name':
        final name = await promptText(
          context,
          '分类名称',
          value: category.name,
          save: (name) async {
            await repository.updateCategory(category.id, name: name);
          },
        );
        if (name != null && mounted && repository.isCurrent) {
          refreshFavorites(ref);
        }
      case 'color':
        final color = await promptText(
          context,
          '分类颜色',
          value: category.color,
          hint: '#2563EB',
          maxLength: 7,
          save: (color) async {
            if (!RegExp(r'^#[0-9a-fA-F]{6}$').hasMatch(color)) {
              throw const AppFailure(FailureKind.invalidInput);
            }
            await repository.updateCategory(category.id, color: color);
          },
        );
        if (color == null || !mounted || !repository.isCurrent) return;
        refreshFavorites(ref);
      case 'order':
        final input = await promptText(
          context,
          '分类排序',
          value: '${category.order}',
          hint: '数字越小越靠前',
          maxLength: 6,
          save: (input) async {
            final order = int.tryParse(input);
            if (order == null || order < 0 || order > 100000) {
              throw const AppFailure(FailureKind.invalidInput);
            }
            await repository.updateCategory(category.id, order: order);
          },
        );
        if (input == null || !mounted || !repository.isCurrent) return;
        refreshFavorites(ref);
      case 'delete':
        final move = await showDialog<bool>(
          context: context,
          builder: (context) => AlertDialog(
            title: const Text('删除分类'),
            content: Text('选择如何处理“${category.name}”中的收藏。'),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(context),
                child: const Text('取消'),
              ),
              TextButton(
                onPressed: () => Navigator.pop(context, false),
                child: Text(
                  '连同收藏删除',
                  style: TextStyle(color: Theme.of(context).colorScheme.error),
                ),
              ),
              FilledButton(
                onPressed: () => Navigator.pop(context, true),
                child: const Text('移到默认分类'),
              ),
            ],
          ),
        );
        if (move == null || !mounted || !repository.isCurrent) return;
        if (!move &&
            !await confirmAction(
              context,
              '删除分类及其中收藏？',
              '该分类中的全部收藏记录将被删除，此操作无法撤销。',
              action: '删除',
            )) {
          return;
        }
        if (mounted && repository.isCurrent) {
          await run(
            (repo) => repo.deleteCategory(category.id, moveToDefault: move),
          );
        }
    }
  }

  @override
  Widget build(BuildContext context) {
    final repository = ref.watch(favoriteRepositoryProvider);
    if (repository == null) return const SizedBox.shrink();
    final categories = ref.watch(favoriteCategoriesProvider);
    return Scaffold(
      appBar: AppBar(
        title: const Text('收藏分类'),
        actions: [
          IconButton(
            tooltip: '新建分类',
            onPressed: busy
                ? null
                : () async {
                    final name = await promptText(
                      context,
                      '新建分类',
                      hint: '分类名称',
                      save: (name) async {
                        await repository.createCategory(name);
                      },
                    );
                    if (name != null && mounted && repository.isCurrent) {
                      refreshFavorites(ref);
                    }
                  },
            icon: const Icon(Icons.create_new_folder_outlined),
          ),
        ],
      ),
      body: Column(
        children: [
          if (busy) const LinearProgressIndicator(),
          Expanded(
            child: categories.when(
              loading: () => const Center(child: CircularProgressIndicator()),
              error: (e, _) => ErrorPanel(
                error: e,
                retry: () => ref.invalidate(favoriteCategoriesProvider),
              ),
              data: (items) => RefreshIndicator(
                onRefresh: () async {
                  ref.invalidate(favoriteCategoriesProvider);
                  await ref.read(favoriteCategoriesProvider.future);
                },
                child: ListView.builder(
                  physics: const AlwaysScrollableScrollPhysics(),
                  itemCount: items.length,
                  itemBuilder: (context, index) {
                    final category = items[index];
                    return ListTile(
                      leading: Icon(
                        Icons.folder_outlined,
                        color: favoriteColor(category.color),
                      ),
                      title: Text(category.name),
                      subtitle: Text('${category.count} 条收藏'),
                      trailing: category.isDefault
                          ? const Icon(Icons.lock_outline, size: 18)
                          : PopupMenuButton<String>(
                              tooltip: '管理 ${category.name}',
                              enabled: !busy,
                              onSelected: (value) => edit(category, value),
                              itemBuilder: (_) => const [
                                PopupMenuItem(
                                  value: 'name',
                                  child: Text('重命名'),
                                ),
                                PopupMenuItem(
                                  value: 'color',
                                  child: Text('颜色'),
                                ),
                                PopupMenuItem(
                                  value: 'order',
                                  child: Text('排序'),
                                ),
                                PopupMenuItem(
                                  value: 'delete',
                                  child: Text('删除分类'),
                                ),
                              ],
                            ),
                    );
                  },
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
