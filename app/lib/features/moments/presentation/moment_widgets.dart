import 'dart:async';

import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import 'package:share_plus/share_plus.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/storage/remote_attachment_store.dart';
import '../../../core/tenant/session_access.dart';
import '../../../design/components/feedback.dart';
import '../../../design/tokens.dart';
import '../../chat/presentation/message_media.dart';
import '../../contacts/presentation/contact_widgets.dart';
import '../domain/moment.dart';

String momentDate(DateTime? date) {
  if (date == null) return '';
  final local = date.toLocal();
  String two(int value) => value.toString().padLeft(2, '0');
  return '${local.year}/${two(local.month)}/${two(local.day)} ${two(local.hour)}:${two(local.minute)}';
}

class MomentCard extends StatelessWidget {
  const MomentCard({
    super.key,
    required this.moment,
    required this.owner,
    required this.access,
    required this.onOpen,
    required this.onLike,
    required this.onComment,
    required this.onDelete,
    required this.onDeleteComment,
    this.busy = false,
    this.detail = false,
  });
  final Moment moment;
  final String owner;
  final SessionAccess access;
  final VoidCallback onOpen, onLike, onDelete;
  final void Function(MomentComment? reply) onComment;
  final void Function(MomentComment comment) onDeleteComment;
  final bool busy, detail;
  void profile(BuildContext context, String user) =>
      context.push('/moments/user/${Uri.encodeComponent(user)}');
  @override
  Widget build(BuildContext context) {
    final own = moment.author.id == owner;
    final comments = detail ? moment.comments : moment.comments.take(3);
    return Card(
      elevation: 0,
      margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                InkWell(
                  onTap: () => profile(context, moment.author.id),
                  child: ContactAvatar(uri: moment.author.avatar),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: InkWell(
                    onTap: () => profile(context, moment.author.id),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          moment.author.displayName,
                          style: const TextStyle(fontWeight: FontWeight.w600),
                        ),
                        Text(
                          momentDate(moment.createdAt),
                          style: const TextStyle(
                            color: AppColors.secondary,
                            fontSize: 12,
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
                if (own)
                  IconButton(
                    tooltip: '删除动态',
                    onPressed: busy ? null : onDelete,
                    icon: const Icon(Icons.more_horiz),
                  ),
              ],
            ),
            if (moment.text.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 12),
                child: detail
                    ? SelectableText(moment.text)
                    : InkWell(
                        onTap: onOpen,
                        child: Text(
                          moment.text,
                          maxLines: 8,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
              ),
            if (moment.media.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 12),
                child: MomentMediaGrid(moment: moment, access: access),
              ),
            if (moment.mentions.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Wrap(
                  children: [
                    for (final user in moment.mentions)
                      TextButton(
                        onPressed: () => profile(context, user.id),
                        child: Text('@${user.displayName}'),
                      ),
                  ],
                ),
              ),
            if (own)
              Align(
                alignment: Alignment.centerLeft,
                child: TextButton.icon(
                  onPressed: () => showModalBottomSheet<void>(
                    context: context,
                    showDragHandle: true,
                    builder: (_) => SafeArea(
                      child: ListView(
                        shrinkWrap: true,
                        children: [
                          ListTile(
                            title: Text(moment.visibility.label),
                            subtitle: Text(switch (moment.visibility) {
                              MomentVisibility.public => '按服务端好友关系展示',
                              MomentVisibility.private => '只有自己可以查看',
                              MomentVisibility.included => '以下用户可以查看',
                              MomentVisibility.excluded => '以下用户不能查看',
                            }),
                          ),
                          for (final user in moment.audience)
                            ListTile(
                              leading: ContactAvatar(uri: user.avatar),
                              title: Text(user.displayName),
                            ),
                        ],
                      ),
                    ),
                  ),
                  icon: const Icon(Icons.visibility_outlined, size: 16),
                  label: Text(moment.visibility.label),
                ),
              ),
            Wrap(
              spacing: 8,
              children: [
                TextButton.icon(
                  onPressed: busy ? null : onLike,
                  icon: Icon(
                    moment.likedBy(owner)
                        ? Icons.favorite
                        : Icons.favorite_border,
                    size: 18,
                  ),
                  label: Text(
                    moment.likedBy(owner)
                        ? '已赞 ${moment.likes.length}'
                        : '赞 ${moment.likes.length}',
                  ),
                ),
                TextButton.icon(
                  onPressed: busy ? null : () => onComment(null),
                  icon: const Icon(Icons.chat_bubble_outline, size: 18),
                  label: Text('评论 ${moment.comments.length}'),
                ),
                if (!detail)
                  TextButton(onPressed: onOpen, child: const Text('详情')),
              ],
            ),
            if (busy) const LinearProgressIndicator(),
            if (moment.likes.isNotEmpty)
              Container(
                padding: const EdgeInsets.all(10),
                color: AppColors.background,
                child: Text(
                  '♡ ${moment.likes.map((user) => user.displayName).join('、')}',
                  maxLines: detail ? null : 2,
                  overflow: detail ? null : TextOverflow.ellipsis,
                  style: const TextStyle(color: AppColors.primary),
                ),
              ),
            for (final comment in comments)
              InkWell(
                onTap: busy ? null : () => onComment(comment),
                onLongPress: busy || !moment.canDeleteComment(owner, comment)
                    ? null
                    : () => onDeleteComment(comment),
                child: Container(
                  color: AppColors.background,
                  padding: const EdgeInsets.symmetric(
                    horizontal: 10,
                    vertical: 8,
                  ),
                  child: Text.rich(
                    TextSpan(
                      children: [
                        TextSpan(
                          text: comment.author.displayName,
                          style: const TextStyle(
                            color: AppColors.primary,
                            fontWeight: FontWeight.w500,
                          ),
                        ),
                        if (comment.reply != null)
                          TextSpan(text: ' 回复 ${comment.reply!.displayName}'),
                        TextSpan(text: '：${comment.text}'),
                      ],
                    ),
                  ),
                ),
              ),
            if (!detail && moment.comments.length > 3)
              TextButton(onPressed: onOpen, child: const Text('查看全部评论')),
          ],
        ),
      ),
    );
  }
}

class MomentMediaGrid extends StatelessWidget {
  const MomentMediaGrid({
    super.key,
    required this.moment,
    required this.access,
  });
  final Moment moment;
  final SessionAccess access;
  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      final video = moment.mediaType == 1;
      final width = moment.media.length == 1
          ? constraints.maxWidth.clamp(0.0, 320.0)
          : (constraints.maxWidth - 12) / 3;
      return Wrap(
        spacing: 6,
        runSpacing: 6,
        children: [
          for (var index = 0; index < moment.media.length; index++)
            SizedBox(
              width: width,
              height: width,
              child: Semantics(
                button: true,
                label: video ? '播放动态视频' : '查看图片 ${index + 1}',
                child: InkWell(
                  onTap: moment.media[index].uri == null
                      ? null
                      : () {
                          if (!access.isCurrent) return;
                          Navigator.of(context).push<void>(
                            MaterialPageRoute(
                              builder: (_) => video
                                  ? VideoMessageViewer(
                                      uri: moment.media[index].uri!,
                                    )
                                  : MomentGallery(
                                      media: moment.media,
                                      initial: index,
                                      access: access,
                                    ),
                            ),
                          );
                        },
                  child: Stack(
                    fit: StackFit.expand,
                    children: [
                      if ((video
                              ? moment.media[index].thumbnailUri
                              : moment.media[index].thumbnailUri ??
                                    moment.media[index].uri)
                          case final Uri uri)
                        Image.network(
                          uri.toString(),
                          fit: BoxFit.cover,
                          cacheWidth: 600,
                          errorBuilder: (_, _, _) => const ColoredBox(
                            color: AppColors.background,
                            child: Icon(Icons.broken_image_outlined),
                          ),
                        )
                      else
                        const ColoredBox(
                          color: AppColors.background,
                          child: Icon(Icons.image_not_supported_outlined),
                        ),
                      if (video)
                        const Center(
                          child: Icon(
                            Icons.play_circle_fill,
                            size: 48,
                            color: Colors.white,
                          ),
                        ),
                    ],
                  ),
                ),
              ),
            ),
        ],
      );
    },
  );
}

class MomentGallery extends StatefulWidget {
  const MomentGallery({
    super.key,
    required this.media,
    required this.initial,
    required this.access,
  });
  final List<MomentMedia> media;
  final int initial;
  final SessionAccess access;
  @override
  State<MomentGallery> createState() => _MomentGalleryState();
}

class _MomentGalleryState extends State<MomentGallery> {
  late final controller = PageController(initialPage: widget.initial);
  late final store = RemoteAttachmentStore(widget.access);
  late int index = widget.initial;
  bool saving = false;
  Future<void> save() async {
    final uri = widget.media[index].uri;
    if (saving || uri == null || !widget.access.isCurrent) return;
    setState(() => saving = true);
    try {
      final file = await store.download(
        uri,
        name: uri.pathSegments.lastOrNull ?? '动态图片.jpg',
      );
      if (!mounted || !widget.access.isCurrent) return;
      final box = context.findRenderObject() as RenderBox?;
      await SharePlus.instance.share(
        ShareParams(
          files: [XFile(file.path)],
          title: '保存动态图片',
          sharePositionOrigin: box == null
              ? null
              : box.localToGlobal(Offset.zero) & box.size,
        ),
      );
    } catch (error) {
      if (mounted && widget.access.isCurrent) {
        showNotice(
          context,
          error is AppFailure ? error.message : '图片保存未完成，请重试',
        );
      }
    } finally {
      if (mounted) setState(() => saving = false);
    }
  }

  @override
  void dispose() {
    controller.dispose();
    store.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    backgroundColor: Colors.black,
    appBar: AppBar(
      title: Text('${index + 1} / ${widget.media.length}'),
      actions: [
        TextButton(
          onPressed: saving ? null : save,
          child: Text(saving ? '下载中…' : '保存或分享'),
        ),
      ],
    ),
    body: PageView.builder(
      controller: controller,
      itemCount: widget.media.length,
      onPageChanged: (value) => setState(() => index = value),
      itemBuilder: (_, i) {
        final uri = widget.media[i].uri;
        return uri == null
            ? const Center(
                child: Text('图片地址不可用', style: TextStyle(color: Colors.white)),
              )
            : InteractiveViewer(
                minScale: .5,
                maxScale: 5,
                child: Center(
                  child: Image.network(
                    uri.toString(),
                    errorBuilder: (_, _, _) => const Text(
                      '图片加载失败',
                      style: TextStyle(color: Colors.white),
                    ),
                  ),
                ),
              );
      },
    ),
  );
}
