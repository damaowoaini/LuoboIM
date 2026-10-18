import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:just_audio/just_audio.dart';
import 'package:video_player/video_player.dart';

import '../domain/chat_message.dart';
import '../domain/message_privacy.dart';

final class ChatAudioPlayback extends ChangeNotifier {
  ChatAudioPlayback({
    required this.onFailure,
    AudioPlayer Function()? playerFactory,
  }) : _playerFactory = playerFactory ?? AudioPlayer.new;
  final VoidCallback onFailure;
  final AudioPlayer Function() _playerFactory;
  AudioPlayer? _player;
  StreamSubscription<PlayerState>? _subscription;
  StreamSubscription<void>? _privacy;
  Timer? _expiry;
  bool Function()? _permission;
  MessagePrivacy? _policy;
  DateTime Function() _now = DateTime.now;
  String? messageId;
  bool playing = false;
  bool _disposed = false;
  int _request = 0;

  void _unbind() {
    _permission = null;
    _policy = null;
    _expiry?.cancel();
    _expiry = null;
    unawaited(_privacy?.cancel());
    _privacy = null;
  }

  /// A metadata change or route exit invalidates a pending setUrl as well as
  /// active playback. Hiding the media widget alone is not cancellation.
  void recheck() {
    if (_disposed || _permission == null) return;
    _expiry?.cancel();
    if (!_permission!() || !_policy!.canViewAt(_now())) {
      ++_request;
      _unbind();
      playing = false;
      unawaited(_player?.stop().catchError((Object _) {}));
      notifyListeners();
      return;
    }
    final remaining = _policy!.millisecondsUntilExpiry(_now());
    if (remaining != null) {
      _expiry = Timer(
        Duration(milliseconds: (remaining + 1).clamp(1, 86400000)),
        recheck,
      );
    }
  }

  Future<void> toggle(
    ChatMessage message, {
    bool Function()? allowed,
    MessagePrivacy? privacy,
    DateTime Function()? now,
  }) async {
    final policy = privacy ?? message.privacy;
    final clock = now ?? DateTime.now;
    bool permitted() =>
        !_disposed && (allowed?.call() ?? true) && policy.canViewAt(clock());
    final media = message.playableAudio;
    if (media == null || !permitted()) return;
    final request = ++_request;
    _unbind();
    _permission = permitted;
    _policy = policy;
    _now = clock;
    _privacy = policy.changes?.listen((_) => recheck());
    recheck();
    try {
      final player = _player ??= _playerFactory();
      _subscription ??= player.playerStateStream.listen((state) {
        recheck();
        playing =
            (_permission?.call() ?? false) &&
            state.playing &&
            state.processingState != ProcessingState.completed;
        if (!_disposed) notifyListeners();
      });
      if (messageId == message.id && playing) {
        await pause();
        return;
      }
      if (messageId != message.id) {
        await player.stop();
        if (!permitted() || request != _request) return;
        messageId = message.id;
        await player
            .setUrl(media.toString())
            .timeout(const Duration(seconds: 15));
      } else if (player.processingState == ProcessingState.completed) {
        await player.seek(Duration.zero);
      }
      if (permitted() && request == _request) {
        unawaited(
          player.play().catchError((Object _) {
            if (!_disposed && request == _request) onFailure();
          }),
        );
      }
    } catch (_) {
      if (!_disposed && request == _request) onFailure();
    }
  }

  Future<void> pause() async {
    ++_request;
    _unbind();
    playing = false;
    await _player?.pause();
  }

  @override
  void dispose() {
    _disposed = true;
    ++_request;
    _unbind();
    unawaited(_subscription?.cancel());
    final player = _player;
    if (player != null) unawaited(player.dispose());
    super.dispose();
  }
}

class VideoMessageViewer extends StatefulWidget {
  const VideoMessageViewer({super.key, required this.uri});
  final Uri uri;
  @override
  State<VideoMessageViewer> createState() => _VideoMessageViewerState();
}

class _VideoMessageViewerState extends State<VideoMessageViewer>
    with WidgetsBindingObserver {
  late final VideoPlayerController controller;
  bool ready = false;
  bool failed = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    controller = widget.uri.scheme == 'file'
        ? VideoPlayerController.file(File.fromUri(widget.uri))
        : VideoPlayerController.networkUrl(widget.uri);
    controller.addListener(_changed);
    unawaited(
      controller
          .initialize()
          .timeout(const Duration(seconds: 15))
          .then(
            (_) {
              if (mounted) setState(() => ready = true);
            },
            onError: (Object _) {
              if (mounted) setState(() => failed = true);
            },
          ),
    );
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    backgroundColor: Colors.black,
    appBar: AppBar(
      backgroundColor: Colors.black,
      foregroundColor: Colors.white,
      title: const Text('视频'),
    ),
    body: Center(
      child: failed
          ? const Text('视频加载失败，请返回后重试', style: TextStyle(color: Colors.white))
          : !ready
          ? const CircularProgressIndicator()
          : Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                Expanded(
                  child: Center(
                    child: AspectRatio(
                      aspectRatio: controller.value.aspectRatio,
                      child: VideoPlayer(controller),
                    ),
                  ),
                ),
                VideoProgressIndicator(
                  controller,
                  allowScrubbing: true,
                  padding: const EdgeInsets.all(16),
                ),
                IconButton(
                  tooltip: controller.value.isPlaying ? '暂停' : '播放',
                  icon: Icon(
                    controller.value.isPlaying
                        ? Icons.pause_circle
                        : Icons.play_circle,
                    size: 48,
                    color: Colors.white,
                  ),
                  onPressed: () async {
                    try {
                      if (controller.value.isPlaying) {
                        await controller.pause();
                      } else {
                        await controller.play();
                      }
                    } catch (_) {
                      if (mounted) setState(() => failed = true);
                    }
                  },
                ),
              ],
            ),
    ),
  );

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    controller.removeListener(_changed);
    unawaited(controller.dispose());
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state != AppLifecycleState.resumed) {
      unawaited(controller.pause().catchError((Object _) {}));
    }
  }
}
