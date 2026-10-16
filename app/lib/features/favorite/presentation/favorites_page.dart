import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:share_plus/share_plus.dart';

import '../../../core/error/app_failure.dart';
import '../../../design/components/feedback.dart';
import '../data/favorite_exporter.dart';
import '../data/favorite_repository.dart';
import '../domain/favorite.dart';
import '../favorite_providers.dart';
import 'favorite_list_controller.dart';
import 'favorite_widgets.dart';

class FavoritesPage extends ConsumerStatefulWidget {
  const FavoritesPage({super.key});
  @override
  ConsumerState<FavoritesPage> createState() => _FavoritesPageState();
}

class _FavoritesPageState extends ConsumerState<FavoritesPage> {
  final search = TextEditingController();
  bool busy = false;
  @override
  void dispose() {
    search.dispose();
    super.dispose();
  }

  Future<void> run(
    FavoriteRepository repository,
    Future<void> Function() operation, {
    bool refresh = true,
  }) async {
    if (busy || !repository.isCurrent) return;
    setState(() => busy = true);
    try {
      await operation();
      if (mounted && repository.isCurrent && refresh) refreshFavorites(ref);
    } on AppFailure catch (error) {
      if (mounted && repository.isCurrent) showNotice(context, error.message);
    } catch (_) {
      if (mounted && repository.isCurrent) showNotice(context, '操作未完成，请重试');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> batch(FavoriteListController controller, int operation) async {
    final repository = controller.repository;
    final ids = controller.selected.toList();
    if (ids.isEmpty || busy) return;
    if (operation != 4 &&
        controller.items.any(
          (item) => ids.contains(item.id) && !item.canUseContent,
        )) {
      showNotice(context, '私密收藏已隔离，仅可删除');
      return;
    }
    String? category;
    List<String> tags = [];
    try {
      if (operation == 1) {
        final categories = await ref.read(favoriteCategoriesProvider.future);
        if (!mounted || !repository.isCurrent) return;
        category = await chooseFavoriteCategory(context, categories);
        if (category == null) return;
      } else if (operation == 2 || operation == 3) {
        final value = await promptText(
          context,
          operation == 2 ? '批量添加标签' : '批量移除标签',
          hint: '多个标签用逗号分隔',
          maxLength: 512,
          lines: 3,
        );
        if (value == null || value.isEmpty) return;
        tags = favoriteTags(value);
      } else if (!await confirmAction(
        context,
        '删除所选收藏？',
        '将删除这 ${ids.length} 条收藏记录。',
        action: '删除',
      )) {
        return;
      }
      if (!mounted || !repository.isCurrent) return;
      await run(repository, () async {
        await repository.batch(
          ids,
          operation: operation,
          categoryId: category,
          tags: tags,
        );
        controller.selectionMode(false);
      });
    } on AppFailure catch (error) {
      if (mounted && repository.isCurrent) showNotice(context, error.message);
    }
  }

  Future<void> export(FavoriteListController controller) async {
    final repository = controller.repository;
    var query = const FavoriteQuery();
    if (controller.query.filtered) {
      final filtered = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('导出收藏'),
          content: const Text('选择本次导出的范围。'),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context),
              child: const Text('取消'),
            ),
            TextButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('当前筛选'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('全部收藏'),
            ),
          ],
        ),
      );
      if (filtered == null || !mounted || !repository.isCurrent) return;
      if (filtered) query = controller.query;
    }
    final format = await showModalBottomSheet<FavoriteExportFormat>(
      context: context,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const ListTile(title: Text('选择导出格式')),
            for (final format in FavoriteExportFormat.values)
              ListTile(
                title: Text(format.label),
                onTap: () => Navigator.pop(context, format),
              ),
          ],
        ),
      ),
    );
    if (format == null || !mounted || !repository.isCurrent) return;
    await run(repository, () async {
      final file = await FavoriteExporter(repository)
          .export(format: format, query: query);
      if (!mounted || !repository.isCurrent) {
        await file.discard();
        return;
      }
      try {
        file.check();
      } catch (_) {
        await file.discard();
        rethrow;
      }
      if (!mounted) {
        await file.discard();
        return;
      }
      final box = context.findRenderObject() as RenderBox?;
      final result = await SharePlus.instance.share(
        ShareParams(
          title: '萝卜对话收藏',
          files: [XFile(file.path, mimeType: file.format.mime)],
          sharePositionOrigin: box == null
              ? null
              : box.localToGlobal(Offset.zero) & box.size,
        ),
      );
      if (mounted &&
          repository.isCurrent &&
          result.status == ShareResultStatus.success) {
        showNotice(context, '已导出 ${file.count} 条收藏');
      }
    }, refresh: false);
  }

  @override
  Widget build(BuildContext context) {
    ref.watch(favoritePrivacyRevisionProvider);
    final controller = ref.watch(favoriteListControllerProvider);
    final categories = ref.watch(favoriteCategoriesProvider);
    final stats = ref.watch(favoriteStatsProvider);
    ref.listen(favoriteRevisionProvider, (_, _) {
      if (controller != null) unawaited(controller.refresh());
    });
    if (controller == null) return const SizedBox.shrink();
    return ListenableBuilder(
      listenable: controller,
      builder: (context, _) {
        final query = controller.query;
        final allCategories = categories.value ?? const <FavoriteCategory>[];
        void filter({
          int? type,
          String? categoryId,
          bool clearCategory = false,
          List<String>? tags,
        }) => unawaited(
          controller.filter(
            FavoriteQuery(
              keyword: search.text.trim(),
              type: type ?? query.type,
              categoryId: clearCategory ? null : categoryId ?? query.categoryId,
              tags: tags ?? query.tags,
            ),
          ),
        );
        final knownCategory =
            query.categoryId == null ||
            allCategories.any((category) => category.id == query.categoryId);
        return Scaffold(
          appBar: AppBar(
            title: Text(
              controller.selecting
                  ? '已选择 ${controller.selected.length} 条'
                  : '我的收藏',
            ),
            actions: [
              TextButton(
                onPressed: busy
                    ? null
                    : () => controller.selectionMode(!controller.selecting),
                child: Text(controller.selecting ? '取消' : '选择'),
              ),
              PopupMenuButton<String>(
                tooltip: '收藏操作',
                enabled: !busy,
                onSelected: (value) {
                  if (value == 'categories') {
                    context.push('/favorites/categories');
                  } else if (value == 'stats') {
                    context.push('/favorites/stats');
                  } else {
                    unawaited(export(controller));
                  }
                },
                itemBuilder: (_) => const [
                  PopupMenuItem(value: 'categories', child: Text('管理分类')),
                  PopupMenuItem(value: 'stats', child: Text('收藏统计')),
                  PopupMenuItem(value: 'export', child: Text('导出收藏')),
                ],
              ),
            ],
          ),
          body: Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 900),
              child: Column(
                children: [
                  if (busy) const LinearProgressIndicator(),
                  Expanded(
                    child: RefreshIndicator(
                      onRefresh: controller.refresh,
                      child: CustomScrollView(
                        physics: const AlwaysScrollableScrollPhysics(),
                        slivers: [
                          SliverPadding(
                            padding: const EdgeInsets.all(16),
                            sliver: SliverList.list(
                              children: [
                                TextField(
                                  controller: search,
                                  enabled: !busy,
                                  onSubmitted: (_) => filter(),
                                  textInputAction: TextInputAction.search,
                                  decoration: InputDecoration(
                                    hintText: '搜索标题、内容、备注或标签',
                                    prefixIcon: const Icon(Icons.search),
                                    suffixIcon: IconButton(
                                      tooltip: '搜索收藏',
                                      onPressed: busy ? null : () => filter(),
                                      icon: const Icon(Icons.arrow_forward),
                                    ),
                                  ),
                                ),
                                const SizedBox(height: 12),
                                LayoutBuilder(
                                  builder: (context, constraints) {
                                    final fields = [
                                      DropdownButtonFormField<int>(
                                        initialValue: query.type,
                                        isExpanded: true,
                                        decoration: const InputDecoration(
                                          labelText: '类型',
                                        ),
                                        items: [
                                          const DropdownMenuItem(
                                            value: 0,
                                            child: Text('全部类型'),
                                          ),
                                          for (final kind
                                              in FavoriteKind.values)
                                            DropdownMenuItem(
                                              value: kind.code,
                                              child: Text(kind.label),
                                            ),
                                        ],
                                        onChanged: busy
                                            ? null
                                            : (value) => filter(type: value),
                                      ),
                                      DropdownButtonFormField<String>(
                                        key: ValueKey(query.categoryId),
                                        initialValue: query.categoryId ?? '',
                                        isExpanded: true,
                                        decoration: const InputDecoration(
                                          labelText: '分类',
                                        ),
                                        items: [
                                          const DropdownMenuItem(
                                            value: '',
                                            child: Text('全部分类'),
                                          ),
                                          for (final category in allCategories)
                                            DropdownMenuItem(
                                              value: category.id,
                                              child: Text(
                                                category.name,
                                                overflow: TextOverflow.ellipsis,
                                              ),
                                            ),
                                          if (!knownCategory)
                                            DropdownMenuItem(
                                              value: query.categoryId,
                                              child: const Text('分类已删除'),
                                            ),
                                        ],
                                        onChanged: busy
                                            ? null
                                            : (value) => filter(
                                                categoryId: value,
                                                clearCategory: value == '',
                                              ),
                                      ),
                                    ];
                                    return constraints.maxWidth < 360
                                        ? Column(
                                            children: [
                                              fields[0],
                                              const SizedBox(height: 12),
                                              fields[1],
                                            ],
                                          )
                                        : Row(
                                            children: [
                                              Expanded(child: fields[0]),
                                              const SizedBox(width: 12),
                                              Expanded(child: fields[1]),
                                            ],
                                          );
                                  },
                                ),
                                Wrap(
                                  spacing: 8,
                                  crossAxisAlignment: WrapCrossAlignment.center,
                                  children: [
                                    TextButton.icon(
                                      onPressed: busy
                                          ? null
                                          : () async {
                                              final tags = await promptText(
                                                context,
                                                '标签筛选',
                                                value: query.tags.join('，'),
                                                hint: '多个标签用逗号分隔，匹配全部标签',
                                                maxLength: 512,
                                                lines: 3,
                                              );
                                              if (tags != null &&
                                                  mounted &&
                                                  controller
                                                      .repository
                                                      .isCurrent) {
                                                filter(
                                                  tags: favoriteTags(tags),
                                                );
                                              }
                                            },
                                      icon: const Icon(Icons.label_outline),
                                      label: Text(
                                        query.tags.isEmpty
                                            ? '筛选标签'
                                            : query.tags.join(' · '),
                                      ),
                                    ),
                                    if (query.filtered)
                                      TextButton(
                                        onPressed: busy
                                            ? null
                                            : () {
                                                search.clear();
                                                unawaited(
                                                  controller.filter(
                                                    const FavoriteQuery(),
                                                  ),
                                                );
                                              },
                                        child: const Text('清除筛选'),
                                      ),
                                  ],
                                ),
                                if (categories.hasError)
                                  ErrorPanel(
                                    error: categories.error!,
                                    retry: () => ref.invalidate(
                                      favoriteCategoriesProvider,
                                    ),
                                  ),
                                if (stats.hasValue)
                                  Padding(
                                    padding: const EdgeInsets.symmetric(
                                      vertical: 8,
                                    ),
                                    child: Text(
                                      '共 ${stats.requireValue.total} 条收藏 · 当前结果 ${controller.total} 条',
                                      style: TextStyle(
                                        color: Theme.of(context)
                                            .colorScheme
                                            .onSurfaceVariant,
                                      ),
                                    ),
                                  )
                                else if (stats.hasError)
                                  TextButton(
                                    onPressed: () =>
                                        ref.invalidate(favoriteStatsProvider),
                                    child: const Text('统计加载失败，重试'),
                                  ),
                              ],
                            ),
                          ),
                          SliverPadding(
                            padding: const EdgeInsets.symmetric(horizontal: 16),
                            sliver: SliverList.builder(
                              itemCount: controller.items.length,
                              itemBuilder: (context, index) {
                                final item = controller.items[index];
                                return FavoriteTile(
                                  item: item,
                                  category: allCategories
                                      .where(
                                        (category) =>
                                            category.id == item.categoryId,
                                      )
                                      .firstOrNull,
                                  selected: controller.selecting
                                      ? controller.selected.contains(item.id)
                                      : null,
                                  onLongPress: busy
                                      ? null
                                      : () {
                                          controller.selectionMode(true);
                                          controller.toggle(item.id);
                                        },
                                  onTap: () {
                                    if (busy) return;
                                    if (controller.selecting) {
                                      if (!controller.toggle(item.id)) {
                                        showNotice(context, '单次最多选择 100 条');
                                      }
                                    } else {
                                      context.push(
                                        '/favorites/item/${Uri.encodeComponent(item.id)}',
                                      );
                                    }
                                  },
                                );
                              },
                            ),
                          ),
                          SliverToBoxAdapter(
                            child: Padding(
                              padding: const EdgeInsets.all(24),
                              child: Column(
                                children: [
                                  if (controller.failure != null)
                                    ErrorPanel(
                                      error: controller.failure!,
                                      retry: controller.items.isEmpty
                                          ? controller.refresh
                                          : controller.next,
                                    ),
                                  if (controller.loading)
                                    const CircularProgressIndicator()
                                  else if (controller.items.isEmpty &&
                                      controller.failure == null)
                                    Text(
                                      query.filtered
                                          ? '没有匹配的收藏'
                                          : '还没有收藏，可在聊天中长按消息收藏',
                                    )
                                  else if (controller.more)
                                    TextButton(
                                      onPressed: controller.next,
                                      child: const Text('加载更多'),
                                    ),
                                ],
                              ),
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                  if (controller.selecting)
                    SafeArea(
                      top: false,
                      child: Padding(
                        padding: const EdgeInsets.all(12),
                        child: Wrap(
                          spacing: 8,
                          children: [
                            for (final entry in {
                              1: '移动分类',
                              2: '添加标签',
                              3: '移除标签',
                              4: '删除',
                            }.entries)
                              TextButton(
                                onPressed: busy || controller.selected.isEmpty
                                    ? null
                                    : () => batch(controller, entry.key),
                                child: Text(entry.value),
                              ),
                          ],
                        ),
                      ),
                    ),
                ],
              ),
            ),
          ),
        );
      },
    );
  }
}
