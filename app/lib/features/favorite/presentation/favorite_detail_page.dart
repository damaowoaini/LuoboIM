import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:share_plus/share_plus.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../core/error/app_failure.dart';
import '../../../design/components/feedback.dart';
import '../../chat/presentation/message_privacy_content.dart';
import '../data/favorite_repository.dart';
import '../domain/favorite.dart';
import '../favorite_providers.dart';
import 'favorite_widgets.dart';
import 'favorite_media_preview.dart';
import 'favorite_forward_page.dart';

class FavoriteDetailPage extends ConsumerStatefulWidget {
  const FavoriteDetailPage({super.key, required this.favoriteId});
  final String favoriteId;
  @override
  ConsumerState<FavoriteDetailPage> createState() => _FavoriteDetailPageState();
}

class _FavoriteDetailPageState extends ConsumerState<FavoriteDetailPage> {
  bool busy = false;
  Future<void> run(
    Future<void> Function(FavoriteRepository) operation, {
    bool refresh = true,
  }) async {
    final repository = ref.read(favoriteRepositoryProvider);
    if (repository == null || busy) return;
    setState(() => busy = true);
    try {
      await operation(repository);
      if (mounted && repository.isCurrent && refresh) refreshFavorites(ref);
    } on AppFailure catch (error) {
      if (mounted && repository.isCurrent) showNotice(context, error.message);
    } catch (_) {
      if (mounted && repository.isCurrent) showNotice(context, '操作未完成，请重试');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> edit(FavoriteItem item, String field) async {
    final repository = ref.read(favoriteRepositoryProvider);
    if (repository == null || busy || !item.canUseContent) return;
    final original = switch (field) {
      'title' => item.title,
      'notes' => item.notes,
      _ => item.tags.join('，'),
    };
    final text = await promptText(
      context,
      switch (field) {
        'title' => '收藏标题',
        'notes' => '收藏备注',
        _ => '收藏标签',
      },
      guard: (child) => MessagePrivacyContent(
        privacy: item.decoded.privacy,
        retainableOnly: true,
        expired: AlertDialog(
          title: const Text('内容已不可用'),
          content: const Text('这条收藏的隐私状态已更新，未提交的编辑已丢弃。'),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context),
              child: const Text('关闭'),
            ),
          ],
        ),
        child: child,
      ),
      value: original,
      hint: field == 'tags' ? '多个标签用逗号分隔' : '',
      maxLength: field == 'notes'
          ? 8192
          : field == 'title'
          ? 1024
          : 2048,
      lines: field == 'title' ? 2 : 5,
      trim: field != 'notes',
      save: (text) async {
        repository.checkContent(item);
        await repository.update(
          item.id,
          title: field == 'title' ? text : null,
          notes: field == 'notes' ? text : null,
          tags: field == 'tags' ? favoriteTags(text) : null,
        );
      },
    );
    if (text == null || !mounted || !repository.isCurrent) return;
    refreshFavorites(ref);
  }

  Future<void> share(FavoriteItem item) => run((repository) async {
    if (!item.canUseContent) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    repository.checkContent(item);
    final box = context.findRenderObject() as RenderBox?;
    final data = jsonEncode({
      'format': 'luobo-favorites',
      'version': 1,
      'accountKey': repository.api.access.credentials.accountKey,
      'favorites': [item.toJson()],
    });
    await SharePlus.instance.share(
      ShareParams(
        title: item.displayTitle,
        files: [
          XFile.fromData(
            Uint8List.fromList(utf8.encode(data)),
            mimeType: 'application/json',
          ),
        ],
        fileNameOverrides: [
          'luobo-favorite-${DateTime.now().millisecondsSinceEpoch}.json',
        ],
        sharePositionOrigin: box == null
            ? null
            : box.localToGlobal(Offset.zero) & box.size,
      ),
    );
  }, refresh: false);
  @override
  Widget build(BuildContext context) {
    ref.watch(favoritePrivacyRevisionProvider);
    final repository = ref.watch(favoriteRepositoryProvider);
    if (repository == null) return const SizedBox.shrink();
    final info = ref.watch(favoriteInfoProvider(widget.favoriteId));
    final categories = ref.watch(favoriteCategoriesProvider);
    return Scaffold(
      appBar: AppBar(
        title: const Text('收藏详情'),
        actions: [
          if (info.hasValue && info.requireValue.canUseContent)
            IconButton(
              tooltip: '导出此条收藏',
              onPressed: busy ? null : () => share(info.requireValue),
              icon: const Icon(Icons.ios_share),
            ),
        ],
      ),
      body: info.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => ErrorPanel(
          error: e,
          retry: () => ref.invalidate(favoriteInfoProvider(widget.favoriteId)),
        ),
        data: (item) {
          if (!item.canUseContent) {
            return Center(
              child: ListView(
                shrinkWrap: true,
                padding: const EdgeInsets.all(24),
                children: [
                  Text(item.retentionNotice ?? '私密收藏已隔离'),
                  const SizedBox(height: 12),
                  Text('${item.kind.label} · ${favoriteDate(item.createdAt)}'),
                  TextButton(
                    onPressed: busy
                        ? null
                        : () async {
                            if (!await confirmAction(
                              context,
                              '删除隔离收藏？',
                              '仅删除这条收藏记录。',
                              action: '删除',
                            )) {
                              return;
                            }
                            if (!context.mounted || !repository.isCurrent) {
                              return;
                            }
                            await run((repo) async {
                              await repo.remove([item.id]);
                              if (context.mounted && repo.isCurrent) {
                                refreshFavorites(ref);
                                context.pop();
                              }
                            }, refresh: false);
                          },
                    child: const Text('删除收藏'),
                  ),
                ],
              ),
            );
          }
          final category = categories.value
              ?.where((value) => value.id == item.categoryId)
              .firstOrNull;
          final decoded = item.decoded;
          final image = item.kind == FavoriteKind.image
              ? decoded.media ?? favoriteMedia(item.thumbnail)
              : null;
          return Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 800),
              child: ListView(
                padding: const EdgeInsets.all(20),
                children: [
                  if (busy) const LinearProgressIndicator(),
                  Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Icon(favoriteIcon(item.kind), size: 28),
                      const SizedBox(width: 12),
                      Expanded(
                        child: Text(
                          item.displayTitle,
                          style: const TextStyle(
                            fontSize: 23,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 8),
                  Text(
                    '${item.kind.label} · ${favoriteDate(item.createdAt)}',
                    style: TextStyle(
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                    ),
                  ),
                  const SizedBox(height: 20),
                  if (image != null)
                    ClipRRect(
                      borderRadius: BorderRadius.circular(12),
                      child: Image.network(
                        image.toString(),
                        height: 280,
                        fit: BoxFit.contain,
                        errorBuilder: (_, _, _) => const Padding(
                          padding: EdgeInsets.all(32),
                          child: Text('图片加载失败，请稍后重试'),
                        ),
                      ),
                    ),
                  SelectableText(
                    decoded.summary,
                    style: const TextStyle(fontSize: 17, height: 1.6),
                  ),
                  FavoriteMediaPreview(
                    key: ValueKey(
                      '${repository.api.access.credentials.accountKey}/${item.id}',
                    ),
                    item: item,
                    repository: repository,
                  ),
                  if (decoded.media != null && item.kind == FavoriteKind.link)
                    Padding(
                      padding: const EdgeInsets.symmetric(vertical: 12),
                      child: OutlinedButton.icon(
                        icon: Icon(Icons.open_in_new),
                        label: Text('打开链接'),
                        onPressed: busy
                            ? null
                            : () => run((repo) async {
                                repo.checkContent(item);
                                final opened = await launchUrl(
                                  decoded.media!,
                                  mode: LaunchMode.externalApplication,
                                );
                                if (!opened) {
                                  throw const AppFailure(FailureKind.unknown);
                                }
                              }, refresh: false),
                      ),
                    ),
                  const SizedBox(height: 20),
                  Card(
                    elevation: 0,
                    child: Column(
                      children: [
                        ListTile(
                          title: const Text('标题'),
                          subtitle: Text(
                            item.title.isEmpty ? '未设置' : item.title,
                          ),
                          trailing: const Icon(Icons.edit_outlined),
                          onTap: busy ? null : () => edit(item, 'title'),
                        ),
                        ListTile(
                          title: const Text('分类'),
                          subtitle: Text(category?.name ?? item.categoryId),
                          trailing: const Icon(Icons.chevron_right),
                          onTap: busy
                              ? null
                              : () async {
                                  try {
                                    final values = await ref.read(
                                      favoriteCategoriesProvider.future,
                                    );
                                    if (!context.mounted ||
                                        !repository.isCurrent ||
                                        !item.canUseContent) {
                                      return;
                                    }
                                    final id = await chooseFavoriteCategory(
                                      context,
                                      values,
                                    );
                                    if (id != null &&
                                        mounted &&
                                        repository.isCurrent &&
                                        item.canUseContent) {
                                      await run((repo) async {
                                        await repo.update(
                                          item.id,
                                          categoryId: id,
                                        );
                                      });
                                    }
                                  } on AppFailure catch (error) {
                                    if (context.mounted &&
                                        repository.isCurrent) {
                                      showNotice(context, error.message);
                                    }
                                  }
                                },
                        ),
                        ListTile(
                          title: const Text('标签'),
                          subtitle: Text(
                            item.tags.isEmpty ? '未设置' : item.tags.join(' · '),
                          ),
                          trailing: const Icon(Icons.edit_outlined),
                          onTap: busy ? null : () => edit(item, 'tags'),
                        ),
                        ListTile(
                          title: const Text('备注'),
                          subtitle: Text(
                            item.notes.isEmpty ? '未设置' : item.notes,
                          ),
                          trailing: const Icon(Icons.edit_outlined),
                          onTap: busy ? null : () => edit(item, 'notes'),
                        ),
                      ],
                    ),
                  ),
                  if (item.conversationId.isNotEmpty)
                    ListTile(
                      leading: const Icon(Icons.chat_bubble_outline),
                      title: const Text('打开来源聊天'),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: busy
                          ? null
                          : () => context.push(
                              Uri(
                                pathSegments: ['', 'chat', item.conversationId],
                                queryParameters: item.sourceId.isEmpty
                                    ? null
                                    : {'message': item.sourceId},
                              ).toString(),
                            ),
                    ),
                  if (item.decoded.privacy.notice case final notice?)
                    ListTile(
                      leading: const Icon(Icons.lock_clock_outlined),
                      title: Text(notice),
                    ),
                  if (item.decoded.canForward)
                    ListTile(
                      leading: const Icon(Icons.forward_outlined),
                      title: const Text('转发到聊天'),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: busy
                          ? null
                          : () async {
                              if (!repository.isCurrent ||
                                  !item.canUseContent) {
                                return;
                              }
                              final sent = await Navigator.of(context)
                                  .push<bool>(
                                    MaterialPageRoute(
                                      builder: (_) => FavoriteForwardPage(
                                        item: item,
                                        repository: repository,
                                      ),
                                    ),
                                  );
                              if (context.mounted &&
                                  repository.isCurrent &&
                                  sent == true) {
                                showNotice(context, '消息已转发');
                              }
                            },
                    ),
                  TextButton.icon(
                    onPressed: busy
                        ? null
                        : () async {
                            if (!mounted ||
                                !repository.isCurrent ||
                                !item.canUseContent) {
                              return;
                            }
                            await Clipboard.setData(
                              ClipboardData(text: item.content),
                            );
                            if (context.mounted) showNotice(context, '内容已复制');
                          },
                    icon: const Icon(Icons.copy_outlined),
                    label: const Text('复制完整内容'),
                  ),
                  TextButton(
                    onPressed: busy
                        ? null
                        : () async {
                            if (!await confirmAction(
                              context,
                              '删除收藏？',
                              '将删除这条收藏记录。',
                              action: '删除',
                            )) {
                              return;
                            }
                            if (!mounted || !repository.isCurrent) return;
                            await run((repo) async {
                              await repo.remove([item.id]);
                              if (context.mounted && repo.isCurrent) {
                                refreshFavorites(ref);
                                context.pop();
                              }
                            }, refresh: false);
                          },
                    child: Text(
                      '删除收藏',
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.error,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          );
        },
      ),
    );
  }
}
