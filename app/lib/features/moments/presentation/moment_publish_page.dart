import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/storage/object_uploader.dart';
import '../../../design/components/feedback.dart';
import '../../../providers.dart';
import '../../chat/presentation/message_media.dart';
import '../../contacts/domain/contact.dart';
import '../../contacts/presentation/select_contacts_page.dart';
import '../data/moment_media_preparer.dart';
import '../domain/moment.dart';
import '../moments_providers.dart';
import 'moment_audience_page.dart';
import 'moments_page.dart';

class MomentPublishPage extends ConsumerStatefulWidget {
  const MomentPublishPage({super.key});
  @override
  ConsumerState<MomentPublishPage> createState() => _MomentPublishPageState();
}

class _MomentPublishPageState extends ConsumerState<MomentPublishPage> {
  final text = TextEditingController();
  final media = <StagedMomentMedia>[];
  final mentions = <String, Contact>{};
  MomentAudience audience = MomentAudience(MomentVisibility.public, [], []);
  ObjectUploader? uploader;
  bool busy = false, committing = false, uncertain = false, allowPop = false;
  bool cancelRequested = false;
  double progress = 0;
  String? failure;
  bool get dirty =>
      text.text.isNotEmpty || media.isNotEmpty || mentions.isNotEmpty;
  Future<void> leave() async {
    if (busy || !mounted) return;
    if (dirty &&
        !await confirmAction(
          context,
          '放弃这次编辑？',
          '尚未发布的文字和所选媒体将被清除。',
          action: '放弃',
        )) {
      return;
    }
    if (!mounted) return;
    setState(() => allowPop = true);
    // Let PopScope observe the updated permission before popping the route.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) Navigator.pop(context);
    });
  }

  Future<void> pick() async {
    final repository = ref.read(momentsRepositoryProvider);
    if (repository == null || busy || uncertain) return;
    final choice = await showModalBottomSheet<String>(
      context: context,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            for (final entry in const {
              'images': '从相册选择图片（最多 9 张）',
              'camera': '拍照',
              'video': '选择视频（15 秒以内）',
              'record': '拍摄视频（15 秒以内）',
            }.entries)
              ListTile(
                title: Text(entry.value),
                onTap: () => Navigator.pop(context, entry.key),
              ),
          ],
        ),
      ),
    );
    if (choice == null || !mounted || !repository.isCurrent) return;
    final video = choice == 'video' || choice == 'record';
    if ((media.isNotEmpty && (video || media.first.video)) ||
        (!video && media.length >= 9)) {
      showNotice(context, '一次动态可选择最多 9 张图片或 1 个视频');
      return;
    }
    setState(() {
      busy = true;
      failure = null;
    });
    final prepared = <StagedMomentMedia>[];
    try {
      final picker = ImagePicker();
      final List<XFile> chosen;
      if (video) {
        final file = await picker.pickVideo(
          source: choice == 'video' ? ImageSource.gallery : ImageSource.camera,
          maxDuration: const Duration(seconds: 15),
        );
        chosen = [?file];
      } else if (choice == 'camera') {
        final file = await picker.pickImage(
          source: ImageSource.camera,
          imageQuality: 85,
          maxWidth: 2560,
          maxHeight: 2560,
          requestFullMetadata: false,
        );
        chosen = [?file];
      } else {
        chosen = await picker.pickMultiImage(
          limit: 9 - media.length,
          imageQuality: 85,
          maxWidth: 2560,
          maxHeight: 2560,
          requestFullMetadata: false,
        );
      }
      repository.check();
      final preparer = MomentMediaPreparer(
        ref.read(imGatewayProvider),
        repository.api.access,
      );
      for (final file in chosen) {
        if (!mounted) throw const AppFailure(FailureKind.cancelled);
        prepared.add(await preparer.prepare(file.path, video: video));
      }
      if (!mounted || !repository.isCurrent) return;
      setState(() => media.addAll(prepared));
      prepared.clear();
    } catch (error) {
      if (mounted && repository.isCurrent) {
        setState(
          () => failure = error is AppFailure
              ? error.message
              : '无法读取媒体，请检查权限或选择其他文件',
        );
      }
    } finally {
      for (final file in prepared) {
        await file.dispose();
      }
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> publish() async {
    final repository = ref.read(momentsRepositoryProvider);
    if (repository == null || busy || uncertain) return;
    if (text.text.trim().isEmpty && media.isEmpty) {
      showNotice(context, '写点内容或选择图片、视频');
      return;
    }
    final task = ObjectUploader(repository.api);
    uploader = task;
    setState(() {
      busy = true;
      failure = null;
      progress = 0;
      cancelRequested = false;
    });
    try {
      final attachments = <MomentMedia>[];
      for (var index = 0; index < media.length; index++) {
        final position = index;
        attachments.add(
          await media[index].upload(
            task,
            progress: (value) {
              if (mounted && repository.isCurrent) {
                setState(() => progress = (position + value) / media.length);
              }
            },
          ),
        );
      }
      repository.check();
      if (cancelRequested) throw const AppFailure(FailureKind.cancelled);
      if (!mounted) return;
      setState(() => committing = true);
      final moment = await repository.create(
        MomentDraft(
          text: text.text,
          media: attachments,
          mediaType: media.firstOrNull?.video == true ? 1 : 0,
          visibility: audience.visibility,
          audience:
              audience.visibility == MomentVisibility.public ||
                  audience.visibility == MomentVisibility.private
              ? []
              : audience.users.toList(),
          groups:
              audience.visibility == MomentVisibility.public ||
                  audience.visibility == MomentVisibility.private
              ? []
              : audience.groups.toList(),
          mentions: audience.visibility == MomentVisibility.private
              ? []
              : mentions.keys.toList(),
        ),
      );
      if (!mounted || !repository.isCurrent) return;
      ref.invalidate(momentUnreadProvider);
      setState(() => allowPop = true);
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && repository.isCurrent) Navigator.pop(context, moment);
      });
    } catch (error) {
      if (mounted && repository.isCurrent) {
        setState(() {
          uncertain =
              committing &&
              (error is! AppFailure ||
                  [
                    FailureKind.network,
                    FailureKind.timeout,
                    FailureKind.invalidResponse,
                    FailureKind.unknown,
                  ].contains(error.kind));
          failure = uncertain
              ? '暂时无法确认发布结果，请先查看我的动态，避免重复发布。当前编辑仍保留。'
              : error is AppFailure
              ? error.message
              : '发布未完成，请重试';
        });
      }
    } finally {
      task.close();
      if (identical(uploader, task)) uploader = null;
      if (mounted) {
        setState(() {
          busy = false;
          committing = false;
        });
      }
    }
  }

  @override
  void dispose() {
    text.dispose();
    uploader?.close();
    for (final file in media) {
      unawaited(file.dispose());
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final repository = ref.watch(momentsRepositoryProvider);
    if (repository == null) return const SizedBox.shrink();
    final enabled = !busy && !uncertain;
    return PopScope(
      canPop: allowPop || !repository.isCurrent,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) unawaited(leave());
      },
      child: Scaffold(
        appBar: AppBar(
          title: const Text('发布动态'),
          leading: IconButton(
            tooltip: '返回',
            onPressed: busy ? null : leave,
            icon: const Icon(Icons.arrow_back),
          ),
          actions: [
            TextButton(
              onPressed: enabled ? publish : null,
              child: Text(
                busy
                    ? committing
                          ? '提交中…'
                          : '准备中…'
                    : '发布',
              ),
            ),
          ],
        ),
        body: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 780),
            child: ListView(
              padding: const EdgeInsets.all(20),
              children: [
                TextField(
                  controller: text,
                  enabled: enabled,
                  maxLength: 2000,
                  minLines: 5,
                  maxLines: 12,
                  decoration: const InputDecoration(
                    hintText: '分享新鲜事…',
                    alignLabelWithHint: true,
                  ),
                ),
                const SizedBox(height: 16),
                LayoutBuilder(
                  builder: (context, constraints) => Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: [
                      for (final file in media)
                        SizedBox(
                          width: (constraints.maxWidth - 16) / 3,
                          height: (constraints.maxWidth - 16) / 3,
                          child: Stack(
                            fit: StackFit.expand,
                            children: [
                              InkWell(
                                onTap: busy
                                    ? null
                                    : () => Navigator.of(context).push<void>(
                                        MaterialPageRoute(
                                          builder: (_) => file.video
                                              ? VideoMessageViewer(
                                                  uri: file.file.uri,
                                                )
                                              : _SelectedImagePreview(
                                                  file: file.file,
                                                ),
                                        ),
                                      ),
                                child: Stack(
                                  fit: StackFit.expand,
                                  children: [
                                    Image.file(
                                      file.thumbnail ?? file.file,
                                      fit: BoxFit.cover,
                                      cacheWidth: 500,
                                      errorBuilder: (_, _, _) => const Icon(
                                        Icons.broken_image_outlined,
                                      ),
                                    ),
                                    if (file.video)
                                      const Center(
                                        child: Icon(
                                          Icons.play_circle_fill,
                                          color: Colors.white,
                                          size: 40,
                                        ),
                                      ),
                                  ],
                                ),
                              ),
                              Positioned(
                                right: 0,
                                top: 0,
                                child: IconButton.filledTonal(
                                  tooltip: '移除所选媒体',
                                  onPressed: enabled
                                      ? () {
                                          setState(() => media.remove(file));
                                          unawaited(file.dispose());
                                        }
                                      : null,
                                  icon: const Icon(Icons.close),
                                ),
                              ),
                            ],
                          ),
                        ),
                    ],
                  ),
                ),
                const SizedBox(height: 12),
                OutlinedButton.icon(
                  onPressed: enabled ? pick : null,
                  icon: const Icon(Icons.add_photo_alternate_outlined),
                  label: const Text('添加图片或视频'),
                ),
                const SizedBox(height: 16),
                Card(
                  elevation: 0,
                  child: Column(
                    children: [
                      ListTile(
                        title: const Text('谁可以看'),
                        subtitle: Text(
                          '${audience.visibility.label}${audience.users.isEmpty && audience.groups.isEmpty ? '' : ' · ${audience.users.length} 位好友、${audience.groups.length} 个群'}',
                        ),
                        trailing: const Icon(Icons.chevron_right),
                        enabled: enabled,
                        onTap: () async {
                          final value = await Navigator.of(context)
                              .push<MomentAudience>(
                                MaterialPageRoute(
                                  builder: (_) =>
                                      MomentAudiencePage(initial: audience),
                                ),
                              );
                          if (value != null &&
                              mounted &&
                              repository.isCurrent) {
                            setState(() => audience = value);
                          }
                        },
                      ),
                      ListTile(
                        title: const Text('@ 提醒好友'),
                        subtitle: Text(
                          audience.visibility == MomentVisibility.private
                              ? '仅自己可见时无需提醒'
                              : '提醒的人仍需符合可见范围',
                        ),
                        trailing: const Icon(Icons.person_add_alt),
                        enabled:
                            enabled &&
                            audience.visibility != MomentVisibility.private,
                        onTap: () async {
                          final selected = await selectContacts(
                            context,
                            title: '提醒好友',
                            excluded: {repository.owner},
                          );
                          if (selected != null &&
                              mounted &&
                              repository.isCurrent) {
                            setState(() {
                              for (final user in selected) {
                                mentions[user.id] = user;
                              }
                            });
                          }
                        },
                      ),
                    ],
                  ),
                ),
                if (mentions.isNotEmpty &&
                    audience.visibility != MomentVisibility.private)
                  Wrap(
                    spacing: 6,
                    children: [
                      for (final user in mentions.values)
                        InputChip(
                          label: Text('@${user.displayName}'),
                          onDeleted: enabled
                              ? () => setState(() => mentions.remove(user.id))
                              : null,
                        ),
                    ],
                  ),
                if (busy) ...[
                  const SizedBox(height: 16),
                  LinearProgressIndicator(
                    value: committing || media.isEmpty ? null : progress,
                  ),
                  Text(
                    committing
                        ? '正在提交动态'
                        : '正在处理媒体 ${(progress * 100).round()}%',
                  ),
                  if (!committing && uploader != null)
                    TextButton(
                      onPressed: () {
                        if (committing) return;
                        cancelRequested = true;
                        uploader?.close();
                      },
                      child: const Text('取消上传，保留编辑'),
                    ),
                ],
                if (failure != null)
                  Padding(
                    padding: const EdgeInsets.symmetric(vertical: 16),
                    child: Text(
                      failure!,
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.error,
                      ),
                    ),
                  ),
                if (uncertain) ...[
                  OutlinedButton(
                    onPressed: () => Navigator.of(context).push<void>(
                      MaterialPageRoute(
                        builder: (_) => MomentsPage(author: repository.owner),
                      ),
                    ),
                    child: const Text('查看我的动态，核对发布结果'),
                  ),
                  TextButton(
                    onPressed: () async {
                      if (await confirmAction(
                            context,
                            '已核对发布结果？',
                            '只有确认动态没有发布成功后才重新提交；若服务端已保存，再次提交会产生重复动态。',
                            action: '确认未发布，继续编辑',
                          ) &&
                          mounted &&
                          repository.isCurrent) {
                        setState(() {
                          uncertain = false;
                          failure = null;
                        });
                      }
                    },
                    child: const Text('核对后继续编辑'),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _SelectedImagePreview extends StatelessWidget {
  const _SelectedImagePreview({required this.file});
  final File file;
  @override
  Widget build(BuildContext context) => Scaffold(
    backgroundColor: Colors.black,
    appBar: AppBar(title: const Text('所选图片')),
    body: InteractiveViewer(
      minScale: .5,
      maxScale: 5,
      child: Center(
        child: Image.file(
          file,
          errorBuilder: (_, _, _) =>
              const Text('图片无法读取', style: TextStyle(color: Colors.white)),
        ),
      ),
    ),
  );
}
