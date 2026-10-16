import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:share_plus/share_plus.dart';

import '../../../core/error/app_failure.dart';
import '../../../design/components/feedback.dart';
import '../data/favorite_exporter.dart';
import '../domain/favorite.dart';
import '../favorite_providers.dart';
import 'favorite_widgets.dart';

class FavoriteStatsPage extends ConsumerStatefulWidget {
  const FavoriteStatsPage({super.key});
  @override
  ConsumerState<FavoriteStatsPage> createState() => _FavoriteStatsPageState();
}

class _FavoriteStatsPageState extends ConsumerState<FavoriteStatsPage> {
  bool busy = false;
  Future<void> share(FavoriteStats stats, {bool export = false}) async {
    final repository = ref.read(favoriteRepositoryProvider);
    if (busy || repository == null || !repository.isCurrent) return;
    setState(() => busy = true);
    try {
      final file = export
          ? await FavoriteExporter(repository).exportStats()
          : null;
      if (!mounted || !repository.isCurrent) return;
      final box = context.findRenderObject() as RenderBox?;
      await SharePlus.instance.share(
        ShareParams(
          title: '萝卜对话收藏统计',
          text: export ? null : _summary(stats),
          files: file == null
              ? null
              : [XFile(file.path, mimeType: 'application/json')],
          sharePositionOrigin: box == null
              ? null
              : box.localToGlobal(Offset.zero) & box.size,
        ),
      );
    } on AppFailure catch (error) {
      if (mounted && repository.isCurrent) showNotice(context, error.message);
    } catch (_) {
      if (mounted && repository.isCurrent) showNotice(context, '分享未完成，请重试');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  String _summary(FavoriteStats stats) => [
    '萝卜对话 · 收藏统计',
    '收藏总数：${stats.total}',
    '分类数（含默认分类）：${stats.count('categoryCount')}',
    '今日新增：${stats.count('todayAddedCount')}',
    '本周新增：${stats.count('weekAddedCount')}',
    '本月新增：${stats.count('monthAddedCount')}',
    '新增数量按 UTC 统计，本周从周一开始。',
    for (final kind in FavoriteKind.values)
      '${kind.label}：${stats.typeCount(kind)}',
    '最近收藏：${favoriteDate(stats.count('lastFavoriteTime'))}',
  ].join('\n');

  @override
  Widget build(BuildContext context) {
    final result = ref.watch(favoriteStatsProvider);
    final stats = result.value;
    return Scaffold(
      appBar: AppBar(
        title: const Text('收藏统计'),
        actions: [
          IconButton(
            tooltip: '刷新统计',
            onPressed: busy
                ? null
                : () => ref.invalidate(favoriteStatsProvider),
            icon: const Icon(Icons.refresh),
          ),
          PopupMenuButton<String>(
            tooltip: '分享统计',
            enabled: !busy && stats != null && !result.hasError,
            onSelected: (value) => share(stats!, export: value == 'export'),
            itemBuilder: (_) => const [
              PopupMenuItem(value: 'share', child: Text('分享统计摘要')),
              PopupMenuItem(value: 'export', child: Text('导出统计 JSON')),
            ],
          ),
        ],
      ),
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 900),
          child: Column(
            children: [
              if (busy || result.isLoading) const LinearProgressIndicator(),
              Expanded(
                child: RefreshIndicator(
                  onRefresh: () async {
                    ref.invalidate(favoriteStatsProvider);
                    await ref.read(favoriteStatsProvider.future);
                  },
                  child: ListView(
                    padding: const EdgeInsets.all(16),
                    physics: const AlwaysScrollableScrollPhysics(),
                    children: [
                      if (result.hasError)
                        ErrorPanel(
                          error: result.error!,
                          retry: () => ref.invalidate(favoriteStatsProvider),
                        )
                      else if (stats != null) ...[
                        Card(
                          child: Padding(
                            padding: const EdgeInsets.all(20),
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text(
                                  '收藏总数',
                                  style: Theme.of(context)
                                      .textTheme
                                      .titleMedium,
                                ),
                                Text(
                                  '${stats.total}',
                                  style: Theme.of(context)
                                      .textTheme
                                      .displaySmall,
                                ),
                                const SizedBox(height: 12),
                                Text(
                                  '分类 ${stats.count('categoryCount')} 个（含默认分类）',
                                ),
                                if (stats.count('categoryCount') > 0)
                                  Text(
                                    '平均每类 ${(stats.total / stats.count('categoryCount')).toStringAsFixed(1)} 条',
                                  ),
                                const SizedBox(height: 8),
                                Text(
                                  '最近收藏：${favoriteDate(stats.count('lastFavoriteTime'))}',
                                ),
                                const Text('最近收藏时间按设备时区显示。'),
                              ],
                            ),
                          ),
                        ),
                        const SizedBox(height: 12),
                        _section(context, '新增收藏', [
                          const Text('按 UTC 自然日、周、月统计；周一为每周起点。'),
                          const SizedBox(height: 12),
                          Wrap(
                            spacing: 24,
                            runSpacing: 12,
                            children: [
                              Text('今日 ${stats.count('todayAddedCount')} 条'),
                              Text('本周 ${stats.count('weekAddedCount')} 条'),
                              Text('本月 ${stats.count('monthAddedCount')} 条'),
                            ],
                          ),
                        ]),
                        const SizedBox(height: 12),
                        _section(context, '类型分布', [
                          if (stats.total == 0) const Text('还没有收藏内容。'),
                          for (final kind in FavoriteKind.values)
                            _distribution(
                              context,
                              kind.label,
                              stats.typeCount(kind),
                              stats.total,
                              icon: favoriteIcon(kind),
                            ),
                        ]),
                        const SizedBox(height: 12),
                        _section(context, '分类分布', [
                          for (final category in stats.categories)
                            _distribution(
                              context,
                              category.name,
                              category.count,
                              stats.total,
                              color: favoriteColor(category.color),
                            ),
                        ]),
                      ],
                    ],
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _section(BuildContext context, String title, List<Widget> children) =>
      Card(
        child: Padding(
          padding: const EdgeInsets.all(20),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(title, style: Theme.of(context).textTheme.titleMedium),
              const SizedBox(height: 16),
              ...children,
            ],
          ),
        ),
      );

  Widget _distribution(
    BuildContext context,
    String title,
    int count,
    int total, {
    IconData icon = Icons.folder_outlined,
    Color? color,
  }) {
    final fraction = total == 0 ? 0.0 : count / total;
    return Padding(
      padding: const EdgeInsets.only(bottom: 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Wrap(
            spacing: 8,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              Icon(icon, color: color, size: 20),
              Text(title),
              Text('$count 条 · ${(fraction * 100).toStringAsFixed(1)}%'),
            ],
          ),
          const SizedBox(height: 8),
          LinearProgressIndicator(
            value: fraction.clamp(0, 1),
            color: color,
            semanticsLabel: '$title，$count 条，共 $total 条',
          ),
        ],
      ),
    );
  }
}
