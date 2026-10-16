import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../design/tokens.dart';
import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../core/network/tenant_api.dart';
import '../../../core/tenant/session_access.dart';
import '../../../providers.dart';
import '../../favorite/domain/favorite.dart';
import '../../favorite/favorite_providers.dart';
import '../../rtc/presentation/rtc_providers.dart';
import '../../conversation/data/conversation_repository.dart';
import '../data/chat_repository.dart';
import '../domain/chat_message.dart';
import '../domain/chat_mention.dart';
import '../../group/data/group_repository.dart';
import 'chat_controller.dart';
import 'mention_text_controller.dart';
import 'group_mention_picker.dart';
import 'mentioned_message_text.dart';
import 'chat_media_actions.dart';
import 'chat_batch_selection.dart';
import 'chat_batch_widgets.dart';
import 'chat_content_access.dart';
import 'chat_merged_messages_page.dart';
import 'chat_call_picker.dart';
import 'message_media.dart';
import 'message_readers_sheet.dart';
import 'chat_search_page.dart';
import 'chat_forward_page.dart';
import 'message_privacy_content.dart';

class ChatPage extends ConsumerStatefulWidget {
  const ChatPage({
    super.key,
    required this.conversationId,
    this.sourceMessageId,
    this.sourceCurrent,
    this.now,
    this.playbackFactory,
  });
  final String conversationId;
  final String? sourceMessageId;
  final bool Function()? sourceCurrent;
  final DateTime Function()? now;
  final ChatAudioPlayback Function(VoidCallback onFailure)? playbackFactory;
  @override
  ConsumerState<ChatPage> createState() => _ChatPageState();
}

class _ChatPageState extends ConsumerState<ChatPage>
    with WidgetsBindingObserver {
  late final ChatController controller;
  late final ChatBatchSelection selection;
  late final ChatAudioPlayback playback;
  final input = MentionTextController();
  final focus = FocusNode();
  final scroll = ScrollController();
  bool _draftLoaded = false;
  bool _restoredMentionText = false;
  bool _allowPop = false;
  bool _leaving = false;
  final _viewportKey = GlobalKey();
  final Map<String, GlobalKey> _rowKeys = {};
  bool _foreground = true;
  bool _routeVisible = true;
  bool _readQueued = false;
  bool _receiptBusy = false;
  String _readSignature = '';
  final Set<String> _voiceReadAttempted = {};
  Timer? _audioExpiry;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    final lifecycle = WidgetsBinding.instance.lifecycleState;
    _foreground = lifecycle == null || lifecycle == AppLifecycleState.resumed;
    void audioFailure() {
      if (mounted) _notice('语音播放失败，请重试');
    }

    playback =
        widget.playbackFactory?.call(audioFailure) ??
        ChatAudioPlayback(onFailure: audioFailure);
    playback.addListener(_changed);
    controller = ChatController(
      ChatRepository(
        gateway: ref.read(imGatewayProvider),
        session: ref.read(appRuntimeProvider).session,
        conversationId: widget.conversationId,
        api: ref.read(tenantApiProvider),
        now: widget.now,
      ),
    );
    selection = ChatBatchSelection(controller)..addListener(_changed);
    controller.addListener(_changed);
    input.addListener(_inputChanged);
    unawaited(_start());
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    _foreground = state == AppLifecycleState.resumed;
    _readSignature = '';
    if (_foreground) {
      _scheduleVisibleReads();
    } else {
      unawaited(playback.pause());
    }
  }

  void _scheduleVisibleReads() {
    if (_readQueued) return;
    _readQueued = true;
    WidgetsBinding.instance.addPostFrameCallback((_) async {
      _readQueued = false;
      if (!mounted ||
          !_foreground ||
          !_routeVisible ||
          _receiptBusy ||
          !controller.usable ||
          controller.conversation?.isGroup != true) {
        return;
      }
      final viewport = _viewportKey.currentContext?.findRenderObject();
      if (viewport is! RenderBox || !viewport.hasSize) return;
      final bounds = viewport.localToGlobal(Offset.zero) & viewport.size;
      final ids = <String>[];
      for (final message in controller.messages) {
        if (message.mine ||
            message.privacy.isExpiredAt(controller.repository.now()) ||
            message.kind == ChatMessageKind.voice ||
            message.kind == ChatMessageKind.system ||
            message.kind == ChatMessageKind.typing) {
          continue;
        }
        final row = _rowKeys[message.id]?.currentContext?.findRenderObject();
        if (row is! RenderBox || !row.hasSize) continue;
        final rectangle = row.localToGlobal(Offset.zero) & row.size;
        final visible = rectangle.intersect(bounds);
        final threshold =
            (rectangle.height < bounds.height
                ? rectangle.height
                : bounds.height) /
            2;
        if (visible.width > 0 &&
            visible.height > 0 &&
            visible.height >= threshold) {
          ids.add(message.id);
        }
      }
      ids.sort();
      final signature = ids.join('|');
      if (signature == _readSignature) return;
      _readSignature = signature;
      if (ids.isEmpty) return;
      _receiptBusy = true;
      try {
        await controller.markVisibleRead(ids);
      } finally {
        _receiptBusy = false;
        if (mounted) _scheduleVisibleReads();
      }
    });
  }

  Future<void> _showReaders(String messageId) async {
    final selected = await showModalBottomSheet<String>(
      context: context,
      isScrollControlled: true,
      builder: (_) => MessageReadersSheet(
        repository: controller.repository,
        messageId: messageId,
      ),
    );
    if (!mounted || selected == null) return;
    final group = controller.conversation?.groupId;
    final query = group == null ? '' : '?group=${Uri.encodeComponent(group)}';
    await context.push('/contacts/user/${Uri.encodeComponent(selected)}$query');
  }

  Future<void> _start() async {
    bool initialCurrent() => mounted && (widget.sourceCurrent == null ||
        _routeVisible && widget.sourceCurrent!());
    if (!initialCurrent()) return;
    await controller.start(isCurrent: widget.sourceCurrent == null ? null : initialCurrent);
    final id = widget.sourceMessageId;
    if (id == null || !mounted || !controller.usable || widget.sourceCurrent?.call() == false) return;
    await _locateSource(id, sourceCurrent: widget.sourceCurrent);
  }

  Future<void> _locateSource(String id, {bool Function()? sourceCurrent}) async {
    bool current() => mounted && controller.usable &&
        (sourceCurrent == null || _routeVisible && sourceCurrent());
    if (!current()) return;
    try {
      final found = await controller.viewSource(id, isCurrent: current);
      if (!current()) return;
      if (!found) _notice('原消息已删除或尚未同步到本机');
      if (found && scroll.hasClients) scroll.jumpTo(0);
    } catch (_) {
      if (current()) _notice('暂时无法定位原消息，请稍后重试');
    }
  }

  void _inputChanged() {
    if (input.text.isEmpty) _restoredMentionText = false;
    if (_draftLoaded) controller.updateDraft(input.storedDraft);
    if (mounted) setState(() {});
  }

  void _changed() {
    if (!mounted) return;
    playback.recheck();
    _audioExpiry?.cancel();
    final playing = controller.messages
        .where((item) => item.id == playback.messageId)
        .firstOrNull;
    if (playing != null && playback.playing) {
      final delay = playing.privacy.millisecondsUntilExpiry(
        controller.repository.now(),
      );
      if (delay != null) {
        if (delay <= 0) {
          unawaited(playback.pause());
        } else {
          _audioExpiry = Timer(Duration(milliseconds: delay + 1), () {
            if (mounted && playback.messageId == playing.id) {
              unawaited(playback.pause());
            }
          });
        }
      }
    }
    if (!_draftLoaded && controller.conversation != null) {
      input.restore(controller.draft);
      _restoredMentionText =
          controller.conversation!.isGroup && controller.draft.contains('@');
      input.selection = TextSelection.collapsed(offset: input.text.length);
      _draftLoaded = true;
    }
    if (_foreground &&
        _routeVisible &&
        playback.playing &&
        playback.messageId != null &&
        _voiceReadAttempted.add(playback.messageId!)) {
      unawaited(controller.markVisibleRead([playback.messageId!]));
    }
    setState(() {});
  }

  Future<void> _retry() async {
    _readSignature = '';
    if (controller.conversation == null) {
      await controller.start();
    } else {
      await controller.refresh();
    }
    await controller.markVisibleRead(const []);
    if (mounted) _scheduleVisibleReads();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    selection.removeListener(_changed);
    selection.dispose();
    controller.removeListener(_changed);
    controller.dispose();
    playback.removeListener(_changed);
    playback.dispose();
    input.removeListener(_inputChanged);
    input.dispose();
    focus.dispose();
    scroll.dispose();
    _audioExpiry?.cancel();
    super.dispose();
  }

  Future<void> _leave() async {
    if (selection.active) {
      selection.cancel();
      return;
    }
    if (_leaving) return;
    _leaving = true;
    await controller.flushDraft();
    if (!mounted) return;
    if (controller.draftFailure != null) {
      final leave = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('草稿未保存'),
          content: const Text('暂时无法保存输入内容，返回可能丢失本次修改。'),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('留在此页'),
            ),
            TextButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('仍然返回'),
            ),
          ],
        ),
      );
      if (leave != true) {
        _leaving = false;
        return;
      }
    }
    if (!mounted) return;
    setState(() => _allowPop = true);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) context.pop();
    });
  }

  @override
  Widget build(BuildContext context) {
    _routeVisible = ModalRoute.of(context)?.isCurrent ?? true;
    if (!_routeVisible) _readSignature = '';
    _scheduleVisibleReads();
    return PopScope<Object?>(
      canPop: _allowPop,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) unawaited(_leave());
      },
      child: Scaffold(
        appBar: AppBar(
          leading: BackButton(onPressed: _leave),
          title: selection.active
              ? Text('已选 ${selection.count} 条')
              : InkWell(
                  onTap: controller.conversation == null
                      ? null
                      : _conversationDetails,
                  child: Text(
                    controller.conversation?.title ?? '聊天',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                      fontWeight: FontWeight.w600,
                      fontSize: 18,
                    ),
                  ),
                ),
          actions: [
            if (!selection.active)
              IconButton(
                tooltip: '发起通话',
                icon: const Icon(Icons.call_outlined),
                onPressed: controller.conversation == null || !controller.usable
                    ? null
                    : _call,
              ),
            if (!selection.active)
              IconButton(
                tooltip: '聊天设置',
                icon: const Icon(Icons.more_horiz),
                onPressed: controller.conversation == null ? null : _settings,
              ),
          ],
        ),
        body: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 900),
            child: Column(
              children: [
                if (controller.sourceMessageId != null)
                  MaterialBanner(
                    content: const Text('正在查看来源消息'),
                    actions: [
                      TextButton(
                        onPressed: controller.returnToLatest,
                        child: const Text('返回最新'),
                      ),
                    ],
                  ),
                if (controller.failure != null)
                  MaterialBanner(
                    content: Text(controller.failure!.message),
                    actions: [
                      TextButton(onPressed: _retry, child: const Text('重试')),
                    ],
                  ),
                if (controller.draftFailure != null)
                  MaterialBanner(
                    content: const Text('草稿未保存'),
                    actions: [
                      TextButton(
                        onPressed: controller.flushDraft,
                        child: const Text('重新保存'),
                      ),
                    ],
                  ),
                Expanded(
                  child: controller.loading && controller.messages.isEmpty
                      ? const Center(child: CircularProgressIndicator())
                      : controller.messages.isEmpty
                      ? Center(
                          child: Text(
                            controller.conversation == null
                                ? '暂时无法加载会话'
                                : '还没有消息，开始聊天吧',
                            style: const TextStyle(color: AppColors.secondary),
                          ),
                        )
                      : NotificationListener<ScrollNotification>(
                          onNotification: (notification) {
                            _scheduleVisibleReads();
                            if (notification.metrics.extentAfter < 200) {
                              unawaited(controller.loadOlder());
                            }
                            return false;
                          },
                          child: ListView.builder(
                            key: _viewportKey,
                            controller: scroll,
                            reverse: true,
                            padding: const EdgeInsets.symmetric(
                              horizontal: 14,
                              vertical: 16,
                            ),
                            itemCount:
                                controller.messages.length +
                                (controller.hasMore ? 1 : 0),
                            itemBuilder: (context, index) {
                              if (index == controller.messages.length) {
                                return Center(
                                  child: Padding(
                                    padding: const EdgeInsets.all(10),
                                    child: controller.loadingOlder
                                        ? const CircularProgressIndicator()
                                        : TextButton(
                                            onPressed: controller.loadOlder,
                                            child: const Text('加载更早的消息'),
                                          ),
                                  ),
                                );
                              }
                              final item =
                                  controller.messages[controller
                                          .messages
                                          .length -
                                      1 -
                                      index];
                              return ColoredBox(
                                key: _rowKeys.putIfAbsent(
                                  item.id,
                                  GlobalKey.new,
                                ),
                                color: item.id == controller.sourceMessageId
                                    ? const Color(0xffdbeafe)
                                    : Colors.transparent,
                                child: _message(item),
                              );
                            },
                          ),
                        ),
                ),
                if (selection.active || selection.busy)
                  ChatBatchToolbar(
                    count: selection.count,
                    busy: selection.busy,
                    canMerge: selection.canMerge,
                    onDelete: _deleteSelection,
                    onMerge: _forwardSelection,
                    onCancel: selection.cancel,
                  )
                else
                  _composer(),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _message(ChatMessage item) => ChatSelectionRow(
    active: selection.active,
    eligible: selection.eligible(item),
    selected: selection.selected(item.id),
    onToggle: () {
      if (!selection.toggle(item)) _notice('最多选择 20 条消息');
    },
    child: _messageBody(item),
  );

  Widget _messageBody(ChatMessage item) {
    if (item.kind == ChatMessageKind.system ||
        item.kind == ChatMessageKind.typing) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 10),
        child: Center(
          child: Text(
            item.text,
            textAlign: TextAlign.center,
            style: const TextStyle(color: AppColors.secondary, fontSize: 12),
          ),
        ),
      );
    }
    final avatar = InkWell(
      onTap: item.senderId.isEmpty || !controller.usable
          ? null
          : () => _userDetails(item.senderId),
      onLongPress:
          item.senderId.isEmpty ||
              item.mine ||
              controller.conversation?.isGroup != true ||
              !controller.usable
          ? null
          : () => _mentionSender(item),
      customBorder: const CircleBorder(),
      child: CircleAvatar(
        radius: 18,
        backgroundColor: const Color(0xffdce7ff),
        child: Text(
          item.senderName.isEmpty ? '?' : item.senderName.characters.first,
          style: const TextStyle(color: AppColors.primary, fontSize: 13),
        ),
      ),
    );
    return Padding(
      padding: const EdgeInsets.only(bottom: 16),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisAlignment: item.mine
            ? MainAxisAlignment.end
            : MainAxisAlignment.start,
        children: [
          if (!item.mine) avatar,
          if (!item.mine) const SizedBox(width: 9),
          Flexible(
            child: Column(
              crossAxisAlignment: item.mine
                  ? CrossAxisAlignment.end
                  : CrossAxisAlignment.start,
              children: [
                if (!item.mine && controller.conversation?.isGroup == true)
                  Padding(
                    padding: const EdgeInsets.only(bottom: 5),
                    child: Text(
                      item.senderName,
                      style: const TextStyle(
                        color: AppColors.secondary,
                        fontSize: 12,
                      ),
                    ),
                  ),
                GestureDetector(
                  onLongPress: () => _messageActions(item),
                  child: Container(
                    constraints: BoxConstraints(
                      maxWidth: MediaQuery.sizeOf(context).width * .7,
                    ),
                    padding: const EdgeInsets.symmetric(
                      horizontal: 13,
                      vertical: 10,
                    ),
                    decoration: BoxDecoration(
                      color: item.mine ? const Color(0xffdce8ff) : Colors.white,
                      borderRadius: BorderRadius.circular(12),
                    ),
                    child: MessagePrivacyContent(
                      key: ValueKey(item.id),
                      privacy: item.privacy,
                      now: controller.repository.now,
                      child: _content(item),
                    ),
                  ),
                ),
                const SizedBox(height: 4),
                if (item.privacy.notice case final notice?)
                  Padding(
                    padding: const EdgeInsets.symmetric(vertical: 4),
                    child: Text(
                      notice,
                      style: const TextStyle(
                        color: AppColors.error,
                        fontSize: 11,
                      ),
                    ),
                  ),
                Wrap(
                  spacing: 5,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    Text(
                      _time(item.sentAt),
                      style: const TextStyle(
                        color: AppColors.secondary,
                        fontSize: 10,
                      ),
                    ),
                    if (item.mine &&
                        item.delivery == MessageDelivery.sending) ...[
                      const SizedBox(
                        width: 10,
                        height: 10,
                        child: CircularProgressIndicator(strokeWidth: 1.5),
                      ),
                      Text(
                        controller.progress.containsKey(item.id)
                            ? '${controller.progress[item.id]}%'
                            : '发送中',
                        style: const TextStyle(
                          fontSize: 10,
                          color: AppColors.secondary,
                        ),
                      ),
                    ],
                    if (item.mine && item.delivery == MessageDelivery.failed)
                      TextButton.icon(
                        style: TextButton.styleFrom(
                          minimumSize: const Size(44, 44),
                          padding: const EdgeInsets.symmetric(horizontal: 6),
                        ),
                        onPressed: () => controller.resend(item.id),
                        icon: const Icon(
                          Icons.error_outline,
                          color: AppColors.error,
                          size: 16,
                        ),
                        label: const Text(
                          '发送失败，重试',
                          style: TextStyle(
                            color: AppColors.error,
                            fontSize: 12,
                          ),
                        ),
                      ),
                    if (item.mine &&
                        item.delivery == MessageDelivery.sent &&
                        controller.conversation?.isGroup == true)
                      TextButton(
                        onPressed: () => _showReaders(item.id),
                        style: TextButton.styleFrom(
                          minimumSize: const Size(44, 44),
                          padding: const EdgeInsets.symmetric(horizontal: 6),
                        ),
                        child: Text(
                          '${item.groupReadCount ?? 0} 人已读',
                          style: const TextStyle(fontSize: 11),
                        ),
                      )
                    else if (item.mine && item.delivery == MessageDelivery.sent)
                      Text(
                        item.read ? '已读' : '已发送',
                        style: const TextStyle(
                          fontSize: 10,
                          color: AppColors.secondary,
                        ),
                      ),
                  ],
                ),
              ],
            ),
          ),
          if (item.mine) const SizedBox(width: 9),
          if (item.mine) avatar,
        ],
      ),
    );
  }

  ChatContentAccess? _contentAccess(
    ChatMessage child,
    ChatMessage? portal,
    bool Function()? portalCurrent,
  ) {
    try {
      return ChatContentAccess(
        repository: controller.repository,
        root: portal ?? child,
        child: child,
        isRouteCurrent: () =>
            mounted &&
            _foreground &&
            (portalCurrent?.call() ??
                (ModalRoute.of(context)?.isCurrent ?? false)),
      );
    } on AppFailure {
      return null;
    }
  }

  Widget _content(
    ChatMessage item, {
    ChatMessage? portal,
    bool Function()? portalCurrent,
  }) {
    switch (item.kind) {
      case ChatMessageKind.forwarded:
        final original = item.forwardedMessage;
        if (original == null) {
          if (!item.mergedContentComplete) return const Text('聊天记录内容过多，暂不可用');
          if (item.mergedMessages.isEmpty) return const Text('[聊天记录]');
          final root = portal ?? item;
          return TextButton.icon(
            icon: const Icon(Icons.forum_outlined),
            label: Text('聊天记录 · ${item.mergedMessages.length} 条 · 查看'),
            onPressed: () async {
              final access = _contentAccess(item, portal, portalCurrent);
              if (access == null || !access.canView) return;
              await playback.pause();
              if (!mounted || !access.canView) return;
              await Navigator.of(context).push(
                MaterialPageRoute<void>(
                  builder: (_) => ChatMergedMessagesPage(
                    root: root,
                    messages: item.mergedMessages,
                    now: controller.repository.now,
                    canView: () => controller.repository.canView(root),
                    onExit: () {
                      unawaited(playback.pause());
                    },
                    childBuilder: (child, isCurrent) =>
                        _content(child, portal: root, portalCurrent: isCurrent),
                  ),
                ),
              );
            },
          );
        }
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('转发的消息', style: TextStyle(fontWeight: FontWeight.w600)),
            const SizedBox(height: 6),
            Text(
              original.senderName,
              style: const TextStyle(fontSize: 12, color: AppColors.secondary),
            ),
            _messageText(original, portal: portal ?? item, embedded: true),
            if (original.quotePreview != null)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Text('引用：${original.quotePreview}'),
              ),
            const Text(
              '原提及仅作展示，不会再次提醒成员',
              style: TextStyle(fontSize: 11, color: AppColors.secondary),
            ),
          ],
        );
      case ChatMessageKind.image:
      case ChatMessageKind.sticker:
        final image = item.playableMedia;
        if (image == null) return const Text('图片暂不可用');
        return Semantics(
          label: '图片消息',
          button: true,
          child: InkWell(
            onTap: () async {
              final access = _contentAccess(item, portal, portalCurrent);
              if (access == null || !access.canView) return;
              await playback.pause();
              if (!mounted || !access.canView) return;
              await _image(image, access);
            },
            child: ClipRRect(
              borderRadius: BorderRadius.circular(6),
              child: Image(
                image: image.scheme == 'file'
                    ? FileImage(File.fromUri(image))
                    : NetworkImage(image.toString()),
                width: 210,
                height: 160,
                fit: BoxFit.cover,
                errorBuilder: (_, _, _) => const SizedBox(
                  width: 170,
                  height: 80,
                  child: Center(child: Text('图片加载失败，点击重试')),
                ),
              ),
            ),
          ),
        );
      case ChatMessageKind.video:
      case ChatMessageKind.voice:
      case ChatMessageKind.file:
        final icon = switch (item.kind) {
          ChatMessageKind.video => Icons.play_circle_outline,
          ChatMessageKind.voice => Icons.volume_up_outlined,
          _ => Icons.insert_drive_file_outlined,
        };
        return InkWell(
          onTap: item.playableMedia == null
              ? null
              : () async {
                  final access = _contentAccess(item, portal, portalCurrent);
                  if (access == null || !access.canView) {
                    _notice('消息内容已不可用');
                    return;
                  }
                  if (item.kind == ChatMessageKind.voice) {
                    await playback.toggle(
                      item,
                      allowed: () => access.canView,
                      privacy: access.privacy,
                      now: controller.repository.now,
                    );
                    if (mounted &&
                        access.canView &&
                        portal == null &&
                        playback.playing &&
                        playback.messageId == item.id &&
                        _voiceReadAttempted.add(item.id)) {
                      await controller.markVisibleRead([item.id]);
                    }
                  } else if (item.kind == ChatMessageKind.video) {
                    await playback.pause();
                    if (!mounted || !access.canView) return;
                    await Navigator.of(context).push(
                      MaterialPageRoute<void>(
                        builder: (_) => MessagePrivacyContent(
                          privacy: access.privacy,
                          now: controller.repository.now,
                          expired: const Scaffold(
                            body: Center(child: Text('消息内容已不可用')),
                          ),
                          child: VideoMessageViewer(uri: item.playableMedia!),
                        ),
                      ),
                    );
                  } else {
                    if (!access.canRetain) {
                      _notice('阅后即焚或隐私状态无法确认的文件不能交给外部应用；外部副本无法由本应用收回');
                      return;
                    }
                    if (item.media != null) {
                      await _open(item.media!, check: access.checkRetention);
                    }
                  }
                },
          child: SizedBox(
            width: 210,
            child: Row(
              children: [
                Icon(
                  item.kind == ChatMessageKind.voice &&
                          playback.messageId == item.id &&
                          playback.playing
                      ? Icons.pause_circle_outline
                      : icon,
                  color: AppColors.primary,
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        item.fileName ??
                            (item.kind == ChatMessageKind.voice
                                ? '语音消息'
                                : '视频消息'),
                        maxLines: 3,
                        overflow: TextOverflow.ellipsis,
                      ),
                      if (item.durationSeconds != null)
                        Text(
                          '${item.durationSeconds} 秒',
                          style: const TextStyle(
                            color: AppColors.secondary,
                            fontSize: 12,
                          ),
                        ),
                      if (item.fileBytes != null)
                        Text(
                          _fileSize(item.fileBytes!),
                          style: const TextStyle(
                            color: AppColors.secondary,
                            fontSize: 12,
                          ),
                        ),
                      if (item.playableMedia == null)
                        const Text(
                          '媒体暂不可用',
                          style: TextStyle(
                            color: AppColors.secondary,
                            fontSize: 12,
                          ),
                        ),
                    ],
                  ),
                ),
              ],
            ),
          ),
        );
      case ChatMessageKind.quote:
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            _messageText(
              item,
              portal: portal ?? item,
              embedded: portal != null,
            ),
            const SizedBox(height: 8),
            InkWell(
              onTap: portal != null || item.quoteId == null
                  ? null
                  : () => _locateSource(item.quoteId!),
              child: Container(
                padding: const EdgeInsets.all(8),
                decoration: BoxDecoration(
                  color: Colors.black.withValues(alpha: .04),
                  borderRadius: BorderRadius.circular(5),
                ),
                child: Text(
                  item.quotePreview ?? '引用消息',
                  maxLines: 3,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(
                    fontSize: 13,
                    color: AppColors.secondary,
                  ),
                ),
              ),
            ),
          ],
        );
      default:
        return _messageText(
          item,
          portal: portal ?? item,
          embedded: portal != null,
        );
    }
  }

  Widget _messageText(
    ChatMessage item, {
    ChatMessage? portal,
    bool embedded = false,
  }) => item.mentions.isEmpty
      ? Text(
          item.text.isEmpty ? '[暂不支持的消息]' : item.text,
          style: const TextStyle(fontSize: 16, height: 1.45),
        )
      : MentionedMessageText(
          text: item.text,
          mentions: item.mentions,
          canOpen: (identity) =>
              !identity.all ||
              !embedded ||
              item.sourceGroupId?.isNotEmpty == true,
          onMention: (identity) => _openReceivedMention(
            portal ?? item,
            identity,
            original: item,
            embedded: embedded,
          ),
        );

  MessageHistoryAccess _mentionLease() =>
      controller.repository.gateway.historyAccess(
        controller.repository.generation,
        conversationId: widget.conversationId,
      );

  Future<void> _pickMention() async {
    final group = controller.conversation?.groupId;
    if (!controller.usable || group == null || group.isEmpty) return;
    try {
      final lease = _mentionLease();
      final groupRepository = GroupRepository(
        controller.repository.gateway,
        SessionAccess(controller.repository.session),
      );
      final selected = await showModalBottomSheet<MentionIdentity>(
        context: context,
        isScrollControlled: true,
        showDragHandle: true,
        builder: (_) => GroupMentionPicker(
          repository: groupRepository,
          groupId: group,
          checkLease: lease.check,
        ),
      );
      if (selected == null || !_acceptsPortal(lease)) return;
      _insertMention(selected);
    } on AppFailure catch (error) {
      if (mounted && controller.usable) _notice(error.message);
    }
  }

  Future<void> _mentionSender(ChatMessage item) async {
    if (!controller.repository.canView(item)) return;
    try {
      final lease = _mentionLease();
      final identity = await controller.repository.mentionMember(item.senderId);
      if (!_acceptsPortal(lease) || !controller.repository.canView(item)) {
        return;
      }
      _insertMention(identity);
    } on AppFailure catch (error) {
      if (mounted && controller.usable) _notice(error.message);
    }
  }

  void _insertMention(MentionIdentity identity) {
    if (!input.insert(identity)) {
      _notice('请先完成输入，最多可提醒 10 个不同身份。');
      return;
    }
    focus.requestFocus();
  }

  Future<void> _openReceivedMention(
    ChatMessage item,
    MentionIdentity identity, {
    ChatMessage? original,
    bool embedded = false,
  }) async {
    final source = original ?? item;
    if (!controller.repository.canView(item) ||
        !source.mentions.any((m) => m.identity.userId == identity.userId)) {
      return;
    }
    try {
      final lease = _mentionLease();
      await playback.pause();
      if (!mounted) return;
      if (!_acceptsPortal(lease) || !controller.repository.canView(item)) {
        return;
      }
      final group = embedded
          ? source.sourceGroupId
          : controller.conversation?.groupId;
      if (identity.all) {
        if (group != null && group.isNotEmpty) {
          await context.push(
            Uri(pathSegments: ['', 'contacts', 'groups', group]).toString(),
          );
        }
      } else {
        await context.push(
          Uri(
            pathSegments: ['', 'contacts', 'user', identity.userId],
            queryParameters: group == null || group.isEmpty
                ? null
                : {'group': group},
          ).toString(),
        );
      }
    } on AppFailure catch (error) {
      if (mounted && controller.usable) _notice(error.message);
    }
  }

  Widget _composer() => SafeArea(
    top: false,
    child: Material(
      color: Colors.white,
      child: Padding(
        padding: const EdgeInsets.all(8),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (_restoredMentionText)
              const Padding(
                padding: EdgeInsets.symmetric(horizontal: 12, vertical: 4),
                child: Text(
                  '草稿仅保留文字，请重新选择需要提醒的成员',
                  style: TextStyle(color: AppColors.secondary, fontSize: 13),
                ),
              ),
            if (controller.quote != null)
              Row(
                children: [
                  Expanded(
                    child: Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 12),
                      child: MessagePrivacyContent(
                        key: ValueKey('quote:${controller.quote!.id}'),
                        privacy: controller.quote!.privacy,
                        now: controller.repository.now,
                        child: Text(
                          '回复 ${controller.quote!.senderName}：${controller.quote!.text}',
                          maxLines: 2,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            color: AppColors.secondary,
                            fontSize: 13,
                          ),
                        ),
                      ),
                    ),
                  ),
                  IconButton(
                    tooltip: '取消引用',
                    onPressed: controller.clearReply,
                    icon: const Icon(Icons.close, size: 18),
                  ),
                ],
              ),
            if (controller.conversation?.canSend == false)
              const Padding(
                padding: EdgeInsets.all(8),
                child: Text(
                  '此会话当前无法发送消息',
                  style: TextStyle(color: AppColors.secondary),
                ),
              )
            else ...[
              Wrap(
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  IconButton(
                    tooltip: '插入表情',
                    onPressed: controller.conversation == null ? null : _emoji,
                    icon: const Icon(
                      Icons.sentiment_satisfied_alt,
                      color: AppColors.secondary,
                    ),
                  ),
                  ChatMediaActions(controller: controller),
                  if (controller.conversation?.isGroup == true)
                    IconButton(
                      tooltip: '提及群成员',
                      onPressed: controller.preparing || !controller.usable
                          ? null
                          : _pickMention,
                      icon: const Icon(Icons.alternate_email),
                    ),
                ],
              ),
              if (input.tokens.isNotEmpty)
                SingleChildScrollView(
                  scrollDirection: Axis.horizontal,
                  child: Row(
                    children: input
                        .freeze()
                        .recipients
                        .map(
                          (m) => Padding(
                            padding: const EdgeInsets.only(right: 6),
                            child: InputChip(
                              label: Text('${m.label} · ${m.identityLabel}'),
                              deleteButtonTooltipMessage:
                                  '取消提醒${m.identityLabel}，保留普通文字',
                              onDeleted: () => input.removeIdentity(m.userId),
                            ),
                          ),
                        )
                        .toList(),
                  ),
                ),
              Row(
                crossAxisAlignment: CrossAxisAlignment.end,
                children: [
                  Expanded(
                    child: TextField(
                      controller: input,
                      focusNode: focus,
                      minLines: 1,
                      maxLines: 5,
                      enabled:
                          controller.conversation != null && controller.usable,
                      textInputAction: TextInputAction.newline,
                      decoration: const InputDecoration(
                        hintText: '输入消息…',
                        contentPadding: EdgeInsets.symmetric(
                          horizontal: 12,
                          vertical: 11,
                        ),
                      ),
                      maxLength: 20000,
                      buildCounter: (
                        _, {
                        required currentLength,
                        required isFocused,
                        required maxLength,
                      }) => null,
                    ),
                  ),
                  const SizedBox(width: 6),
                  IconButton.filled(
                    tooltip: '发送',
                    onPressed:
                        input.text.trim().isEmpty ||
                            controller.preparing ||
                            !controller.usable
                        ? null
                        : _send,
                    icon: controller.preparing
                        ? const SizedBox(
                            width: 18,
                            height: 18,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.arrow_upward),
                  ),
                ],
              ),
            ],
          ],
        ),
      ),
    ),
  );

  Future<void> _send() async {
    if (input.value.composing.isValid && !input.value.composing.isCollapsed) {
      _notice('请先完成当前输入。');
      return;
    }
    final draft = input.freeze();
    final text = draft.text;
    final accepted = await controller.sendText(text, mentionDraft: draft);
    if (!mounted || !accepted) return;
    if (input.text == text) input.clear();
    focus.requestFocus();
    if (scroll.hasClients) {
      unawaited(
        scroll.animateTo(
          0,
          duration: const Duration(milliseconds: 180),
          curve: Curves.easeOut,
        ),
      );
    }
  }

  Future<void> _emoji() => showModalBottomSheet<void>(
    context: context,
    showDragHandle: true,
    builder: (context) => SafeArea(
      child: Padding(
        padding: const EdgeInsets.all(18),
        child: Wrap(
          spacing: 8,
          runSpacing: 8,
          children:
              [
                    '😀',
                    '😊',
                    '🥰',
                    '😎',
                    '😂',
                    '😭',
                    '👍',
                    '🙏',
                    '❤️',
                    '🎉',
                    '🌹',
                    '🤝',
                    '🤔',
                    '😴',
                    '👋',
                    '🔥',
                    '👏',
                    '💪',
                    '✨',
                    '🥳',
                  ]
                  .map(
                    (emoji) => IconButton(
                      tooltip: emoji,
                      onPressed: () {
                        final selection = input.selection;
                        final start = selection.isValid
                            ? selection.start
                            : input.text.length;
                        final end = selection.isValid
                            ? selection.end
                            : input.text.length;
                        input.value = TextEditingValue(
                          text: input.text.replaceRange(start, end, emoji),
                          selection: TextSelection.collapsed(
                            offset: start + emoji.length,
                          ),
                        );
                        Navigator.pop(context);
                        focus.requestFocus();
                      },
                      icon: Text(emoji, style: const TextStyle(fontSize: 26)),
                    ),
                  )
                  .toList(),
        ),
      ),
    ),
  );

  bool _acceptsPortal(MessageHistoryAccess lease) {
    if (!mounted || !controller.usable) return false;
    try {
      lease.check();
      return true;
    } on AppFailure {
      return false;
    }
  }

  Future<void> _messageActions(ChatMessage item) async {
    final repository = controller.repository;
    if (!controller.usable) return;
    late final MessageHistoryAccess lease;
    try {
      lease = repository.gateway.historyAccess(
        repository.generation,
        conversationId: repository.conversationId,
      );
    } on AppFailure {
      return;
    }
    final expired = !repository.canView(item);
    final retainable = repository.canRetain(item);
    final action = await showModalBottomSheet<String>(
      context: context,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              for (final entry in <String, String>{
                if (retainable) 'copy': '复制文字',
                if (retainable) 'reply': '引用回复',
                if (item.canForward && retainable) 'forward': '转发到聊天',
                if (!expired &&
                    item.mine &&
                    item.delivery == MessageDelivery.sent)
                  'revoke': '撤回消息',
                if (!expired && item.delivery == MessageDelivery.failed)
                  'retry': '重新发送',
                if (item.delivery == MessageDelivery.sent && retainable)
                  'favorite': '收藏',
                'delete': '删除消息',
                if (selection.eligible(item)) 'multiple': '多选消息',
              }.entries)
                ListTile(
                  title: Text(entry.value),
                  onTap: () => Navigator.pop(context, entry.key),
                ),
            ],
          ),
        ),
      ),
    );
    if (action == null || !mounted || !controller.usable) return;
    try {
      lease.check();
    } on AppFailure {
      return;
    }
    if (const {'copy', 'reply', 'forward', 'favorite'}.contains(action) &&
        !repository.canRetain(item)) {
      _notice(item.privacy.notice ?? '此消息暂不能保留');
      return;
    }
    if (action != 'delete' &&
        item.privacy.isExpiredAt(controller.repository.now())) {
      _notice('阅后即焚内容已到期');
      return;
    }
    switch (action) {
      case 'multiple':
        selection.begin(item);
      case 'copy':
        await Clipboard.setData(ClipboardData(text: item.text));
      case 'reply':
        controller.reply(item);
        focus.requestFocus();
      case 'retry':
        await controller.resend(item.id);
      case 'forward':
        try {
          final snapshot = await controller.repository.favoriteSnapshot(
            item.id,
          );
          if (!mounted || !controller.usable) return;
          final sent = await Navigator.of(context).push<bool>(
            MaterialPageRoute(
              builder: (_) => ChatForwardPage(
                source: controller.repository,
                snapshot: snapshot,
              ),
            ),
          );
          if (mounted && controller.usable && sent == true) {
            _notice('消息已转发');
          }
        } catch (error) {
          if (mounted && controller.usable) {
            _notice(error is AppFailure ? error.message : '暂时无法转发此消息');
          }
        }
      case 'favorite':
        final repository = ref.read(favoriteRepositoryProvider);
        if (repository == null) return;
        try {
          final snapshot = await controller.repository.favoriteSnapshot(
            item.id,
          );
          if (!mounted || !controller.usable) return;
          await repository.add(
            sourceId: snapshot.messageId,
            conversationId: snapshot.conversationId,
            content: snapshot.content,
            title: snapshot.title,
            thumbnail: snapshot.thumbnail,
            kind: switch (snapshot.kind) {
              ChatMessageKind.image => FavoriteKind.image,
              ChatMessageKind.voice => FavoriteKind.audio,
              ChatMessageKind.video => FavoriteKind.video,
              ChatMessageKind.file => FavoriteKind.file,
              _ => FavoriteKind.message,
            },
          );
          if (mounted && controller.usable) _notice('已收藏');
        } catch (error) {
          if (mounted && controller.usable) {
            _notice(error is AppFailure ? error.message : '收藏失败，请重试');
          }
        }
      case 'revoke':
        await controller.revoke(item);
      case 'delete':
        if (await _confirm('删除消息？', '将删除你账号中的此条消息，此操作无法撤销。')) {
          if (!_acceptsPortal(lease)) return;
          await controller.delete(item);
        }
    }
  }

  Future<void> _deleteSelection() async {
    try {
      final intent = selection.capture();
      if (!await _confirm(
        '删除所选消息？',
        '将提交 ${intent.messageIds.length} 条删除请求。已完成部分无法撤销，失败或结果未确认时会停止后续请求。',
      )) {
        return;
      }
      if (!mounted || !controller.usable) return;
      intent.check();
      final result = await selection.deleteConfirmed(intent);
      if (mounted && controller.usable) _notice(result.notice);
    } catch (error) {
      if (mounted && controller.usable) {
        _notice(error is AppFailure ? error.message : '删除请求未提交');
      }
    }
  }

  Future<void> _forwardSelection() async {
    try {
      final intent = selection.capture();
      if (!selection.beginForward(intent)) return;
      focus.unfocus();
      final sent = await Navigator.of(context).push<bool>(
        MaterialPageRoute(
          builder: (_) => ChatForwardPage.merge(
            source: controller.repository,
            selection: intent,
          ),
        ),
      );
      selection.endForward(sent: sent == true);
      if (mounted && controller.usable && sent == true) _notice('合并消息已转发');
    } catch (error) {
      selection.endForward(sent: false);
      if (mounted && controller.usable) {
        _notice(error is AppFailure ? error.message : '暂时无法合并转发');
      }
    }
  }

  Future<void> _call() async {
    final conversation = controller.conversation;
    if (conversation == null || !controller.usable) return;
    final rtc = ref.read(rtcCoordinatorProvider);
    if (rtc.busy) {
      await context.push('/rtc');
      return;
    }
    final video = await showModalBottomSheet<bool>(
      context: context,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.call),
              title: const Text('语音通话'),
              onTap: () => Navigator.pop(context, false),
            ),
            ListTile(
              leading: const Icon(Icons.videocam),
              title: const Text('视频通话'),
              onTap: () => Navigator.pop(context, true),
            ),
          ],
        ),
      ),
    );
    if (video == null || !mounted || !controller.usable) return;
    List<String>? ids;
    if (conversation.isGroup) {
      ids = await showDialog<List<String>>(
        context: context,
        builder: (_) => ChatCallPicker(repository: controller.repository),
      );
    } else {
      ids = [if (conversation.userId != null) conversation.userId!];
    }
    if (ids == null || ids.isEmpty || !mounted || !controller.usable) return;
    await playback.pause();
    if (!mounted || !controller.usable) return;
    unawaited(
      rtc.dial(
        inviteeIds: ids,
        video: video,
        groupId: conversation.groupId ?? '',
        title: conversation.title,
      ),
    );
    await context.push('/rtc');
  }

  Future<void> _userDetails(String userId) async {
    if (!controller.usable) return;
    await playback.pause();
    if (!mounted || !controller.usable) return;
    final group = controller.conversation?.groupId;
    await context.push(
      Uri(
        pathSegments: ['', 'contacts', 'user', userId],
        queryParameters: group == null || group.isEmpty
            ? null
            : {'group': group},
      ).toString(),
    );
  }

  Future<void> _conversationDetails() async {
    final conversation = controller.conversation;
    if (conversation == null || !controller.usable) return;
    if (conversation.isGroup && conversation.groupId != null) {
      await playback.pause();
      if (!mounted || !controller.usable) return;
      await context.push(
        Uri(pathSegments: ['', 'contacts', 'groups', conversation.groupId!])
            .toString(),
      );
    } else if (conversation.userId != null) {
      await _userDetails(conversation.userId!);
    }
  }

  Future<void> _settings() async {
    final item = controller.conversation;
    if (item == null || !controller.usable) return;
    final repository = ConversationRepository(
      ref.read(imGatewayProvider),
      ref.read(appRuntimeProvider).session,
    );
    late final MessageHistoryAccess lease;
    try {
      lease = controller.repository.gateway.historyAccess(
        controller.repository.generation,
        conversationId: controller.repository.conversationId,
      );
    } on AppFailure {
      return;
    }
    final action = await showModalBottomSheet<String>(
      context: context,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              title: Text(item.isGroup ? '群资料与成员' : '联系人资料'),
              onTap: () => Navigator.pop(context, 'details'),
            ),
            ListTile(
              leading: const Icon(Icons.search),
              title: const Text('查找聊天记录'),
              onTap: () => Navigator.pop(context, 'search'),
            ),
            ListTile(
              title: Text(item.pinned ? '取消置顶' : '置顶会话'),
              onTap: () => Navigator.pop(context, 'pin'),
            ),
            ListTile(
              title: Text(item.muted ? '关闭免打扰' : '开启免打扰'),
              onTap: () => Navigator.pop(context, 'mute'),
            ),
            ListTile(
              title: const Text(
                '清空聊天记录',
                style: TextStyle(color: AppColors.error),
              ),
              onTap: () => Navigator.pop(context, 'clear'),
            ),
          ],
        ),
      ),
    );
    if (action == null || !mounted || !_acceptsPortal(lease)) return;
    if (action == 'search') {
      await playback.pause();
      if (!mounted || !_acceptsPortal(lease)) return;
      final id = await Navigator.of(context).push<String>(
        MaterialPageRoute(
          builder: (_) => ChatSearchPage(repository: controller.repository),
        ),
      );
      if (id != null) await _locateSource(id);
      return;
    }
    if (action == 'details') {
      await _conversationDetails();
      return;
    }
    if (action == 'clear') {
      if (await _confirm('清空聊天记录？', '将清空你账号中的此会话记录，此操作无法撤销。')) {
        if (!_acceptsPortal(lease)) return;
        await controller.clearHistory();
      }
      return;
    }
    try {
      if (action == 'pin') await repository.pin(item);
      if (action == 'mute') await repository.mute(item);
      await controller.refreshConversation();
    } catch (_) {
      if (mounted) _notice('设置未完成，请重试');
    }
  }

  Future<bool> _confirm(String title, String body) async =>
      await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text(title),
          content: Text(body),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('确认'),
            ),
          ],
        ),
      ) ??
      false;

  Future<void> _open(Uri uri, {void Function()? check}) async {
    try {
      check?.call();
      if (!await launchUrl(uri, mode: LaunchMode.externalApplication) &&
          mounted) {
        _notice('无法打开此媒体，请稍后重试');
      }
    } catch (_) {
      if (mounted) _notice('无法打开此媒体，请稍后重试');
    }
  }

  Future<void> _image(Uri uri, ChatContentAccess access) => showDialog<void>(
    context: context,
    builder: (context) => Dialog.fullscreen(
      backgroundColor: Colors.black,
      child: Stack(
        children: [
          Center(
            child: MessagePrivacyContent(
              privacy: access.privacy,
              now: controller.repository.now,
              expired: const Text(
                '阅后即焚内容已到期',
                style: TextStyle(color: Colors.white),
              ),
              child: InteractiveViewer(
                child: Image(
                  image: uri.scheme == 'file'
                      ? FileImage(File.fromUri(uri))
                      : NetworkImage(uri.toString()),
                  errorBuilder: (_, _, _) => const Text(
                    '图片加载失败',
                    style: TextStyle(color: Colors.white),
                  ),
                ),
              ),
            ),
          ),
          SafeArea(
            child: Align(
              alignment: Alignment.topRight,
              child: IconButton(
                tooltip: '关闭图片',
                onPressed: () => Navigator.pop(context),
                icon: const Icon(Icons.close, color: Colors.white),
              ),
            ),
          ),
        ],
      ),
    ),
  );

  void _notice(String message) =>
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(message)));
  String _time(DateTime value) =>
      '${value.hour.toString().padLeft(2, '0')}:${value.minute.toString().padLeft(2, '0')}';
  String _fileSize(int size) => size < 1024
      ? '$size B'
      : size < 1024 * 1024
      ? '${(size / 1024).toStringAsFixed(1)} KB'
      : '${(size / (1024 * 1024)).toStringAsFixed(1)} MB';
}
