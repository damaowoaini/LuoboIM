import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';

import '../../../core/storage/staged_image.dart';
import '../../../core/tenant/session_access.dart';
import '../../../core/tenant/session_coordinator.dart';
import '../../../design/tokens.dart';
import '../../../providers.dart';
import '../data/chat_background_store.dart';
import 'chat_font_size_page.dart';

final chatBackgroundPickerProvider = Provider<Future<XFile?> Function()>(
  (_) => () => ImagePicker().pickImage(source: ImageSource.gallery,
    maxWidth: 2560, maxHeight: 2560, imageQuality: 90, requestFullMetadata: false));

/// Only the message viewport/preview uses this surface. Controls and the composer
/// keep their normal theme and OS/app text scaling is inherited unchanged.
class ChatBackgroundSurface extends StatelessWidget {
  const ChatBackgroundSurface({super.key, required this.image, required this.child});
  final File? image;
  final Widget child;
  @override
  Widget build(BuildContext context) => Stack(fit: StackFit.expand, children: [
    const ColoredBox(color: AppColors.background),
    if (image != null) IgnorePointer(child: Image.file(image!,
      key: const ValueKey('chat-background-image'), fit: BoxFit.cover,
      excludeFromSemantics: true, errorBuilder: (_, _, _) => const Align(
        alignment: Alignment.bottomCenter, child: Text('背景图片无法显示，请重新选择',
          style: TextStyle(color: AppColors.error))))),
    child,
  ]);
}

class ChatBackgroundPage extends ConsumerStatefulWidget {
  const ChatBackgroundPage({super.key, required this.preferences, required this.sourceCurrent, this.fontFactor = 1});
  final ChatBackgroundPreferences preferences;
  final bool Function() sourceCurrent;
  final double fontFactor;
  @override
  ConsumerState<ChatBackgroundPage> createState() => _ChatBackgroundPageState();
}

class _ChatBackgroundPageState extends ConsumerState<ChatBackgroundPage> {
  late final _preferences = widget.preferences;
  late final _sourceCurrent = widget.sourceCurrent;
  StreamSubscription<int>? _invalidations, _access;
  StreamSubscription<SessionSnapshot>? _changes;
  ModalRoute<dynamic>? _route;
  ChatBackgroundSnapshot _original = ChatBackgroundSnapshot.empty;
  StagedImage? _staged;
  File? _draft;
  bool _started = false, _retired = false, _loading = true, _loaded = false;
  bool _choosing = false, _saving = false, _dirty = false, _defaultRequested = false;
  int _epoch = 0;
  String? _failure;
  bool get _current => mounted && !_retired && _preferences.current && _sourceCurrent();
  bool get _active => _current && _route?.isCurrent == true;

  @override
  void initState() {
    super.initState();
    _invalidations = _preferences.session.invalidations.listen((_) => _retire());
    _access = _preferences.session.accessChanges.listen((_) { if (!_preferences.current) _retire(); });
    _changes = _preferences.session.changes.listen((_) { if (!_preferences.current) _retire(); });
  }
  void _retire() {
    if (_retired) return;
    _retired = true; ++_epoch;
    if (mounted) setState(() {});
  }
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _route = ModalRoute.of(context);
    if (_route?.isActive == true && _route?.isCurrent == false) _retire();
    if (!_started) {
      _started = true;
      WidgetsBinding.instance.addPostFrameCallback((_) { if (_active) unawaited(_read()); });
    }
  }
  Future<void> _read() async {
    if (!_active || _saving || _choosing) return;
    final epoch = ++_epoch;
    setState(() { _loading = true; _failure = null; });
    try {
      final value = await _preferences.readForEditor();
      if (!_active || epoch != _epoch) return;
      final previousStage = _staged;
      setState(() {
        _original = value; _draft = value.unavailable ? null : value.image; _staged = null;
        _failure = value.requiresDefaultRestore
          ? '背景记录已损坏，请明确恢复默认背景，或重新读取核对。'
          : value.unavailable ? '背景图片已不可用，请重新选择或恢复默认背景。' : null;
        _loaded = true; _dirty = false; _defaultRequested = false; _loading = false;
      });
      await previousStage?.dispose();
    } catch (_) {
      if (_active && epoch == _epoch) {
        setState(() { _loaded = false; _loading = false; _failure = '背景读取失败，请重新读取。'; });
      }
    }
  }
  Future<void> _choose() async {
    if (!_active || !_loaded || _loading || _choosing || _saving || _original.requiresDefaultRestore) return;
    final epoch = _epoch;
    bool current() => _active && epoch == _epoch;
    setState(() { _choosing = true; _failure = null; });
    StagedImage? image;
    try {
      final selected = await ref.read(chatBackgroundPickerProvider)();
      if (!current() || selected == null) return;
      final access = SessionAccess(_preferences.session);
      final prepared = await ImageStager(ref.read(imGatewayProvider), access).prepare(selected.path);
      image = prepared;
      if (!current()) return;
      final previousStage = _staged;
      setState(() { _staged = prepared; _draft = prepared.file; _dirty = true; _defaultRequested = false; });
      image = null;
      await previousStage?.dispose();
    } catch (_) {
      if (current()) setState(() => _failure = '图片未能读取，请检查相册权限或换一张图片重试。');
    } finally {
      await image?.dispose();
      if (mounted) setState(() => _choosing = false);
    }
  }
  Future<void> _default() async {
    if (!_active || !_loaded || _loading || _choosing || _saving) return;
    final previous = _staged;
    setState(() { _staged = null; _draft = null; _dirty = true; _defaultRequested = true; _failure = null; });
    await previous?.dispose();
  }
  void _close(ModalRoute<dynamic>? route, NavigatorState navigator) {
    if (route?.isCurrent == true && route?.isActive == true && navigator.mounted &&
        identical(route?.navigator, navigator)) {
      navigator.pop();
    }
  }
  Future<void> _save(ModalRoute<dynamic>? route, NavigatorState navigator) async {
    if (!_active || !_loaded || !_dirty || _loading || _choosing || _saving ||
        (_original.requiresDefaultRestore && !_defaultRequested)) {
      return;
    }
    final epoch = _epoch, selected = _staged, original = _original;
    setState(() { _saving = true; _failure = null; });
    try {
      bool current() => _active && epoch == _epoch;
      if (_defaultRequested) {
        await _preferences.restoreDefault(original, sourceCurrent: current);
      } else {
        await _preferences.save(original, selected, sourceCurrent: current);
      }
      if (mounted && _active && epoch == _epoch) _close(route, navigator);
    } catch (_) {
      if (mounted && _active && epoch == _epoch) {
        setState(() => _failure = '背景未确认保存，已保留本次选择。请重新读取核对，或重试保存。');
      }
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }
  @override
  void dispose() {
    ++_epoch;
    unawaited(_invalidations?.cancel()); unawaited(_access?.cancel()); unawaited(_changes?.cancel());
    unawaited(_staged?.dispose());
    super.dispose();
  }
  @override
  Widget build(BuildContext context) {
    final route = ModalRoute.of(context), navigator = Navigator.of(context);
    final editable = _active && _loaded && !_loading && !_choosing && !_saving;
    return PopScope<Object?>(canPop: !_saving || !_current, child: Scaffold(
      appBar: AppBar(title: const Text('聊天背景'), leading: IconButton(
        tooltip: '取消背景设置', constraints: const BoxConstraints(minWidth: 44, minHeight: 44),
        visualDensity: VisualDensity.standard,
        onPressed: !_saving || !_current ? () => _close(route, navigator) : null,
        icon: const Icon(Icons.arrow_back))),
      body: !_current ? const Center(child: Text('会话已变化，请返回后重新打开。')) :
      SafeArea(child: Column(children: [
        Expanded(child: SingleChildScrollView(padding: const EdgeInsets.all(16),
          child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
            const Text('预览', style: TextStyle(color: AppColors.secondary)),
            const SizedBox(height: 12),
            SizedBox(height: 260, child: ClipRRect(borderRadius: BorderRadius.circular(12),
              child: ChatBackgroundSurface(image: _draft, child: Align(
                alignment: Alignment.topRight, child: Container(margin: const EdgeInsets.all(16),
                  padding: const EdgeInsets.all(14), decoration: BoxDecoration(
                    color: const Color(0xffdce8ff), borderRadius: BorderRadius.circular(12)),
                  child: ChatReadingTextScale(factor: widget.fontFactor,
                    child: const Text('这是一条聊天背景预览消息。', style: TextStyle(fontSize: 16)))))))),
            const SizedBox(height: 16),
            if (_loading || _choosing) LinearProgressIndicator(
              semanticsLabel: _loading ? '正在读取聊天背景' : '正在准备背景图片'),
            if (_failure != null) ...[
              Text(_failure!, style: const TextStyle(color: AppColors.error)),
              TextButton(onPressed: _active && !_saving && !_choosing ? _read : null,
                child: const Text('重新读取')),
            ],
            OutlinedButton.icon(onPressed: editable && !_original.requiresDefaultRestore ? _choose : null,
              icon: const Icon(Icons.photo_library_outlined), label: const Text('从相册选择背景')),
            TextButton(onPressed: editable ? _default : null, child: const Text('恢复默认背景')),
            const Text('仅用于当前账号的这段会话，不会发送给其他人。',
              style: TextStyle(color: AppColors.secondary)),
          ]))),
        Padding(padding: const EdgeInsets.all(16), child: SizedBox(width: double.infinity,
          height: 52, child: FilledButton(onPressed: editable && _dirty
            ? () => unawaited(_save(route, navigator)) : null,
            child: Text(_saving ? '正在保存…' : '保存背景')))),
      ])),
    ));
  }
}
