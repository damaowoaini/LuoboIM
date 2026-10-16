import 'dart:async';
import 'dart:io';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import 'package:record/record.dart';

import '../../../core/error/app_failure.dart';
import '../../../design/tokens.dart';
import '../domain/chat_message.dart';
import 'chat_controller.dart';
import 'saved_emoji_sheet.dart' show EmojiPortalGuard;

class ChatMediaActions extends StatefulWidget {
  const ChatMediaActions({
    super.key,
    required this.controller,
    this.onLocation,
    this.onCard,
  });
  final ChatController controller;
  final Future<void> Function()? onLocation;
  final Future<void> Function()? onCard;
  @override
  State<ChatMediaActions> createState() => _ChatMediaActionsState();
}

class _ChatMediaActionsState extends State<ChatMediaActions>
    with WidgetsBindingObserver {
  bool _busy = false, _choosing = false, _openingChoice = false;
  int _locationEpoch = 0;
  ModalRoute<dynamic>? _choiceRoute;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    if (route?.isActive == true &&
        route?.isCurrent != true &&
        !_openingChoice &&
        _choiceRoute?.isCurrent != true) {
      ++_locationEpoch;
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state != AppLifecycleState.resumed) {
      ++_locationEpoch;
    } else if (mounted) {
      // A new explicit foreground click gets a new rendered callback; the
      // previous background callback remains retired.
      setState(() {});
    }
  }

  @override
  void didUpdateWidget(covariant ChatMediaActions oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!identical(oldWidget.controller, widget.controller)) {
      ++_locationEpoch;
    }
  }

  @override
  void dispose() {
    ++_locationEpoch;
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final renderedEpoch = _locationEpoch;
    return IconButton(
      tooltip: '发送附件',
      onPressed:
          _busy ||
              widget.controller.preparing ||
              !widget.controller.usable ||
              widget.controller.conversation == null
          ? null
          : () => _choose(renderedEpoch),
      icon: _busy
          ? const SizedBox(
              width: 18,
              height: 18,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          : const Icon(Icons.add_circle_outline, color: AppColors.secondary),
    );
  }

  Future<void> _choose(int renderedEpoch) async {
    final origin = ModalRoute.of(context), navigator = Navigator.of(context);
    if (_choosing ||
        renderedEpoch != _locationEpoch ||
        origin?.isCurrent != true) {
      return;
    }
    final location = widget.onLocation,
        card = widget.onCard,
        controller = widget.controller;
    _choosing = _openingChoice = true;
    final choice = await showModalBottomSheet<String>(
      context: context,
      showDragHandle: true,
      builder: (context) {
        _choiceRoute = ModalRoute.of(context);
        _openingChoice = false;
        return EmojiPortalGuard(
          onCovered: () {
            ++_locationEpoch;
          },
          child: SafeArea(
            child: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  for (final entry in {
                    'images': '从相册选择图片',
                    'camera': '拍照',
                    'video': '从相册选择视频',
                    'recordVideo': '拍摄视频',
                    'files': '选择文件',
                    'voice': '语音消息',
                    if (location != null) 'location': '位置',
                    if (card != null) 'card': '好友名片',
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
      },
    );
    if (choice == 'location' || choice == 'card') {
      // The sheet Future completes on pop, before its exit animation. Wait
      // for this exact owned route, never popUntil or choose an arbitrary page.
      final menu = _choiceRoute;
      if (menu != null) {
        await menu.completed;
      }
      _choosing = false;
      if (!mounted ||
          renderedEpoch != _locationEpoch ||
          !identical(controller, widget.controller) ||
          !controller.usable ||
          origin?.isCurrent != true ||
          !navigator.mounted ||
          !identical(origin?.navigator, navigator)) {
        return;
      }
      await (choice == 'card' ? card : location)?.call();
      return;
    }
    _choosing = false;
    _openingChoice = false;
    if (choice == null || !mounted || !widget.controller.usable) return;
    setState(() => _busy = true);
    try {
      final picker = ImagePicker();
      switch (choice) {
        case 'images':
          final images = await picker.pickMultiImage(
            limit: 9,
            imageQuality: 85,
            maxWidth: 2560,
            maxHeight: 2560,
            requestFullMetadata: false,
          );
          for (final image in images) {
            if (!mounted || !widget.controller.usable) return;
            await widget.controller.sendAttachment(
              ChatAttachment(
                kind: AttachmentKind.image,
                path: image.path,
                name: image.name,
              ),
            );
          }
        case 'camera':
          final image = await picker.pickImage(
            source: ImageSource.camera,
            imageQuality: 85,
            maxWidth: 2560,
            maxHeight: 2560,
            requestFullMetadata: false,
          );
          if (image != null && mounted && widget.controller.usable) {
            await widget.controller.sendAttachment(
              ChatAttachment(
                kind: AttachmentKind.image,
                path: image.path,
                name: image.name,
              ),
            );
          }
        case 'video':
        case 'recordVideo':
          final video = await picker.pickVideo(
            source: choice == 'video'
                ? ImageSource.gallery
                : ImageSource.camera,
            maxDuration: const Duration(seconds: 60),
          );
          if (video != null && mounted && widget.controller.usable) {
            await widget.controller.sendAttachment(
              ChatAttachment(
                kind: AttachmentKind.video,
                path: video.path,
                name: video.name,
              ),
            );
          }
        case 'files':
          final files = await openFiles();
          for (final file in files) {
            if (!mounted || !widget.controller.usable) return;
            await widget.controller.sendAttachment(
              ChatAttachment(
                kind: AttachmentKind.file,
                path: file.path,
                name: file.name,
              ),
            );
          }
        case 'voice':
          if (!mounted) return;
          final voice = await showModalBottomSheet<ChatAttachment>(
            context: context,
            isDismissible: false,
            enableDrag: false,
            builder: (_) => VoiceRecorderSheet(controller: widget.controller),
          );
          if (voice != null) {
            try {
              if (mounted && widget.controller.usable) {
                await widget.controller.sendAttachment(voice);
              }
            } finally {
              // Only recorder-owned sources are removed. System selections
              // remain untouched even when staging or upload fails.
              final file = File(voice.path);
              if (await file.exists()) await file.delete();
            }
          }
      }
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('媒体操作未完成，请检查权限或选择其他文件后重试')),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }
}

class VoiceRecorderSheet extends StatefulWidget {
  const VoiceRecorderSheet({
    super.key,
    required this.controller,
    this.recorderFactory,
  });
  final ChatController controller;
  final AudioRecorder Function()? recorderFactory;
  @override
  State<VoiceRecorderSheet> createState() => _VoiceRecorderSheetState();
}

class _VoiceRecorderSheetState extends State<VoiceRecorderSheet>
    with WidgetsBindingObserver {
  AudioRecorder? _recorder;
  final _clock = Stopwatch();
  Timer? _timer;
  bool _pressed = false;
  bool _starting = false;
  bool _recording = false;
  bool _finishing = false;
  bool _cancel = false;
  String? _path;
  String? _error;
  Future<void>? _startTask;
  Future<void>? _finishTask;

  void _begin() => unawaited(_startTask = _start());
  void _end({bool cancel = false}) =>
      unawaited(_finishTask = _finish(cancel: cancel));

  Future<void> _deleteSource() async {
    final path = _path;
    _path = null;
    if (path == null) return;
    try {
      final file = File(path);
      if (await file.exists()) await file.delete();
    } catch (_) {
      /* No raw media path or platform error in logs. */
    }
  }

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state != AppLifecycleState.resumed) _end(cancel: true);
  }

  Future<void> _start() async {
    if (_starting || _recording || _finishing) return;
    _pressed = true;
    _cancel = false;
    _starting = true;
    _error = null;
    setState(() {});
    try {
      final recorder = _recorder ??=
          (widget.recorderFactory ?? AudioRecorder.new)();
      if (!await recorder.hasPermission().timeout(
        const Duration(seconds: 15),
      )) {
        throw const AppFailure(FailureKind.invalidInput);
      }
      if (!mounted || !_pressed || !widget.controller.usable) return;
      final repository = widget.controller.repository;
      final directory = await repository.gateway.mediaDirectory(
        repository.generation,
      );
      if (!mounted || !_pressed || !widget.controller.usable) return;
      _path =
          '${directory.path}/recording_${DateTime.now().microsecondsSinceEpoch}.m4a';
      await recorder
          .start(
            const RecordConfig(encoder: AudioEncoder.aacLc, numChannels: 1),
            path: _path!,
          )
          .timeout(const Duration(seconds: 10));
      if (!mounted || !_pressed || !widget.controller.usable) {
        await recorder.cancel();
        return;
      }
      _recording = true;
      _clock
        ..reset()
        ..start();
      _timer = Timer.periodic(const Duration(milliseconds: 200), (_) {
        if (_clock.elapsed >= const Duration(seconds: 60)) {
          _end();
        } else if (mounted) {
          setState(() {});
        }
      });
    } catch (_) {
      _error = '无法开始录音，请检查麦克风权限后重试';
      try {
        await _recorder?.cancel();
      } catch (_) {}
    } finally {
      if (!_recording) await _deleteSource();
      _starting = false;
      if (mounted) setState(() {});
    }
  }

  Future<void> _finish({bool cancel = false}) async {
    _pressed = false;
    if (!_recording || _finishing) return;
    _finishing = true;
    _recording = false;
    _timer?.cancel();
    _clock.stop();
    if (mounted) setState(() {});
    try {
      if (cancel ||
          _cancel ||
          _clock.elapsedMilliseconds < 1000 ||
          !widget.controller.usable) {
        await _recorder!.cancel().timeout(const Duration(seconds: 5));
        if (!cancel && !_cancel) _error = '说话时间太短，请至少录制 1 秒';
      } else {
        final path = await _recorder!.stop().timeout(
          const Duration(seconds: 5),
        );
        if (path == null || path != _path) {
          throw const AppFailure(FailureKind.unknown);
        }
        if (mounted && widget.controller.usable) {
          _path = null;
          Navigator.pop(
            context,
            ChatAttachment(
              kind: AttachmentKind.voice,
              path: path,
              name: '语音.m4a',
              durationSeconds: (_clock.elapsedMilliseconds / 1000).ceil().clamp(
                1,
                60,
              ),
            ),
          );
        }
      }
    } catch (_) {
      _error = '录音未完成，请重试';
    } finally {
      await _deleteSource();
      _finishing = false;
      if (mounted) setState(() {});
    }
  }

  @override
  Widget build(BuildContext context) => SafeArea(
    child: Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              const Expanded(
                child: Text(
                  '语音消息',
                  style: TextStyle(fontSize: 20, fontWeight: FontWeight.w600),
                ),
              ),
              IconButton(
                tooltip: '取消录音',
                onPressed: () async {
                  await _finish(cancel: true);
                  if (context.mounted) Navigator.pop(context);
                },
                icon: const Icon(Icons.close),
              ),
            ],
          ),
          const SizedBox(height: 18),
          Icon(
            _recording ? Icons.mic : Icons.mic_none,
            color: _cancel ? AppColors.error : AppColors.primary,
            size: 52,
          ),
          const SizedBox(height: 12),
          Text(
            _recording ? '${_clock.elapsed.inSeconds} / 60 秒' : '按住说话，上滑取消',
            style: const TextStyle(color: AppColors.secondary),
          ),
          if (_error != null)
            Padding(
              padding: const EdgeInsets.all(8),
              child: Text(
                _error!,
                style: const TextStyle(color: AppColors.error),
              ),
            ),
          const SizedBox(height: 20),
          Semantics(
            label: '按住录音，松开发送，上滑取消',
            button: true,
            child: GestureDetector(
              onLongPressStart: (_) => _begin(),
              onLongPressMoveUpdate: (details) {
                if (_recording) {
                  setState(() => _cancel = details.offsetFromOrigin.dy < -70);
                }
              },
              onLongPressEnd: (_) => _end(),
              onLongPressCancel: () => _end(cancel: true),
              child: Container(
                width: double.infinity,
                padding: const EdgeInsets.all(20),
                decoration: BoxDecoration(
                  color: _cancel
                      ? const Color(0xfffee4e2)
                      : const Color(0xffdce8ff),
                  borderRadius: BorderRadius.circular(12),
                ),
                child: Center(
                  child: Text(
                    _starting
                        ? '正在准备录音…'
                        : _finishing
                        ? '正在处理…'
                        : _cancel
                        ? '松开取消'
                        : _recording
                        ? '松开发送'
                        : '按住说话',
                    style: const TextStyle(
                      fontSize: 18,
                      fontWeight: FontWeight.w600,
                      color: AppColors.primary,
                    ),
                  ),
                ),
              ),
            ),
          ),
        ],
      ),
    ),
  );

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _timer?.cancel();
    _pressed = false;
    final recorder = _recorder;
    if (recorder != null) {
      unawaited(() async {
        await _startTask;
        await _finishTask;
        try {
          await recorder.cancel();
        } catch (_) {}
        await _deleteSource();
        await recorder.dispose();
      }());
    }
    super.dispose();
  }
}
