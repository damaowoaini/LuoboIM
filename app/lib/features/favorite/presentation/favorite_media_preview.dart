import 'dart:async';

import 'package:flutter/material.dart';
import 'package:share_plus/share_plus.dart';

import '../../../core/error/app_failure.dart';
import '../../../design/components/feedback.dart';
import '../../chat/domain/chat_message.dart';
import '../../chat/presentation/message_media.dart';
import '../../chat/presentation/message_privacy_content.dart';
import '../data/favorite_attachment_store.dart';
import '../data/favorite_repository.dart';
import '../domain/favorite.dart';

class FavoriteMediaPreview extends StatefulWidget {
  const FavoriteMediaPreview({
    super.key,
    required this.item,
    required this.repository,
  });
  final FavoriteItem item;
  final FavoriteRepository repository;
  @override
  State<FavoriteMediaPreview> createState() => _FavoriteMediaPreviewState();
}

class _FavoriteMediaPreviewState extends State<FavoriteMediaPreview>
    with WidgetsBindingObserver {
  late final ChatAudioPlayback audio;
  late final FavoriteAttachmentStore store;
  late final StreamSubscription<int> invalidations;
  StreamSubscription<void>? privacy;
  bool downloading = false;
  int received = 0;
  int? total;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    audio = ChatAudioPlayback(onFailure: () => notice('语音播放失败，请重试'))
      ..addListener(changed);
    store = FavoriteAttachmentStore(widget.repository.api.access);
    privacy = widget.repository.privacyChanges?.listen((_) {
      if (!widget.item.canUseContent) {
        store.cancel();
        unawaited(audio.pause());
      }
      if (mounted) setState(() {});
    });
    invalidations = widget.repository.api.access.session.invalidations.listen((
      _,
    ) {
      unawaited(audio.pause());
    });
  }

  void changed() {
    if (mounted) setState(() {});
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (ModalRoute.of(context)?.isCurrent == false) {
      unawaited(Future<void>.microtask(audio.pause));
    }
  }

  void notice(String message) {
    if (mounted && widget.repository.isCurrent) showNotice(context, message);
  }

  Future<void> save() async {
    final item = widget.item;
    final uri = item.decoded.media;
    if (downloading ||
        !item.canUseContent ||
        uri == null ||
        !widget.repository.isCurrent) {
      return;
    }
    setState(() {
      downloading = true;
      received = 0;
      total = null;
    });
    try {
      final file = await store.download(
        uri,
        checkSource: () => widget.repository.checkContent(item),
        name: item.decoded.name.isNotEmpty
            ? item.decoded.name
            : item.title.isNotEmpty
            ? item.title
            : uri.pathSegments.lastOrNull ?? '收藏附件.bin',
        progress: (bytes, length) {
          if (mounted && widget.repository.isCurrent) {
            setState(() {
              received = bytes;
              total = length;
            });
          }
        },
      );
      if (!mounted ||
          !widget.repository.isCurrent ||
          !widget.item.canUseContent) {
        if (await file.exists()) await file.delete();
        return;
      }
      final box = context.findRenderObject() as RenderBox?;
      await SharePlus.instance.share(
        ShareParams(
          title: item.displayTitle,
          files: [XFile(file.path)],
          sharePositionOrigin: box == null
              ? null
              : box.localToGlobal(Offset.zero) & box.size,
        ),
      );
    } on AppFailure catch (error) {
      notice(error.message);
    } catch (_) {
      notice('附件操作未完成，请重试');
    } finally {
      if (mounted) setState(() => downloading = false);
    }
  }

  @override
  void didUpdateWidget(covariant FavoriteMediaPreview oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!widget.item.canUseContent) {
      store.cancel();
      unawaited(audio.pause());
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state != AppLifecycleState.resumed) unawaited(audio.pause());
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    unawaited(invalidations.cancel());
    unawaited(privacy?.cancel());
    audio.removeListener(changed);
    audio.dispose();
    store.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final item = widget.item;
    final uri = item.decoded.media;
    if (!item.canUseContent ||
        uri == null ||
        item.kind == FavoriteKind.message ||
        item.kind == FavoriteKind.link) {
      return const SizedBox.shrink();
    }
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (item.kind == FavoriteKind.video)
            OutlinedButton.icon(
              onPressed: () async {
                await audio.pause();
                if (!context.mounted ||
                    !widget.repository.isCurrent ||
                    !item.canUseContent) {
                  return;
                }
                await Navigator.of(context).push<void>(
                  MaterialPageRoute(
                    builder: (_) => MessagePrivacyContent(
                      privacy: item.decoded.privacy,
                      retainableOnly: true,
                      expired: const Scaffold(
                        body: Center(child: Text('私密收藏已隔离')),
                      ),
                      child: VideoMessageViewer(uri: uri),
                    ),
                  ),
                );
              },
              icon: const Icon(Icons.play_circle_outline),
              label: const Text('播放视频'),
            ),
          if (item.kind == FavoriteKind.audio)
            OutlinedButton.icon(
              onPressed: () => audio.toggle(
                ChatMessage(
                  id: item.id,
                  senderId: '',
                  senderName: '',
                  kind: ChatMessageKind.voice,
                  text: '',
                  sentAt: DateTime.fromMillisecondsSinceEpoch(item.createdAt),
                  mine: false,
                  delivery: MessageDelivery.sent,
                  media: uri,
                  privacy: item.decoded.privacy,
                ),
                allowed: () =>
                    widget.repository.isCurrent && item.canUseContent,
              ),
              icon: Icon(
                audio.playing
                    ? Icons.pause_circle_outline
                    : Icons.play_circle_outline,
              ),
              label: Text(audio.playing ? '暂停音频' : '播放音频'),
            ),
          if (downloading) ...[
            LinearProgressIndicator(
              value: total == null || total! <= 0
                  ? null
                  : (received / total!).clamp(0, 1),
            ),
            const SizedBox(height: 8),
            Text(
              total == null
                  ? '已下载 ${_size(received)}'
                  : '${_size(received)} / ${_size(total!)}',
            ),
            TextButton(onPressed: store.cancel, child: const Text('取消下载')),
          ] else
            OutlinedButton.icon(
              onPressed: save,
              icon: const Icon(Icons.download_outlined),
              label: const Text('下载并保存附件'),
            ),
        ],
      ),
    );
  }

  String _size(int bytes) => bytes < 1024 * 1024
      ? '${(bytes / 1024).toStringAsFixed(1)} KB'
      : '${(bytes / 1024 / 1024).toStringAsFixed(1)} MB';
}
