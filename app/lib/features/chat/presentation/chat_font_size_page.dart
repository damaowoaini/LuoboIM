import 'dart:async';

import 'package:flutter/material.dart';

import '../../../design/tokens.dart';
import '../../../core/tenant/session_coordinator.dart';
import '../data/chat_reading_preferences.dart';

/// Preserve the platform's nonlinear accessibility scaling; nested quotes use
/// the original platform scaler rather than multiplying the app factor twice.
class ChatReadingTextScale extends StatelessWidget {
  const ChatReadingTextScale({super.key, required this.factor, required this.child});
  final double factor;
  final Widget child;
  @override
  Widget build(BuildContext context) {
    final base = context.dependOnInheritedWidgetOfExactType<_ReadingScaleBase>()?.scaler ??
        MediaQuery.textScalerOf(context);
    return _ReadingScaleBase(scaler: base, child: MediaQuery(
      data: MediaQuery.of(context).copyWith(textScaler: _ReadingTextScaler(base, factor)),
      child: child,
    ));
  }
}

class _ReadingScaleBase extends InheritedWidget {
  const _ReadingScaleBase({required this.scaler, required super.child});
  final TextScaler scaler;
  @override
  bool updateShouldNotify(_ReadingScaleBase oldWidget) => scaler != oldWidget.scaler;
}

class _ReadingTextScaler extends TextScaler {
  const _ReadingTextScaler(this.base, this.factor);
  final TextScaler base;
  final double factor;
  @override
  double scale(double fontSize) => base.scale(fontSize) * factor;
  @override
  double get textScaleFactor => scale(14) / 14;
}

/// Own-route observation used by the actual settings sheet. Rebuilds cannot
/// substitute a new callback into an already covered sheet's original ticket.
class ChatFontRouteGuard extends StatefulWidget {
  const ChatFontRouteGuard({super.key, required this.onCovered,
    required this.onRoute, required this.child});
  final VoidCallback onCovered;
  final ValueChanged<ModalRoute<dynamic>?> onRoute;
  final Widget child;
  @override
  State<ChatFontRouteGuard> createState() => _ChatFontRouteGuardState();
}

class _ChatFontRouteGuardState extends State<ChatFontRouteGuard> {
  late final VoidCallback _covered = widget.onCovered;
  late final ValueChanged<ModalRoute<dynamic>?> _registered = widget.onRoute;
  bool _retired = false;
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    _registered(route);
    if (!_retired && route != null && route.isActive && !route.isCurrent) {
      _retired = true;
      _covered();
    }
  }
  @override
  Widget build(BuildContext context) => widget.child;
}

class ChatFontSizePage extends StatefulWidget {
  const ChatFontSizePage({super.key, required this.preferences,
    required this.sourceCurrent});
  final ChatReadingPreferences preferences;
  final bool Function() sourceCurrent;
  @override
  State<ChatFontSizePage> createState() => _ChatFontSizePageState();
}

class _ChatFontSizePageState extends State<ChatFontSizePage> {
  late final ChatReadingPreferences _preferences = widget.preferences;
  late final bool Function() _sourceCurrent = widget.sourceCurrent;
  StreamSubscription<int>? _session, _access;
  StreamSubscription<SessionSnapshot>? _changes;
  ModalRoute<dynamic>? _route;
  bool _started = false, _retired = false, _loading = true, _busy = false;
  bool _loaded = false;
  String? _failure;
  double _draft = defaultChatFontFactor;
  int _epoch = 0;
  bool get _ownerCurrent => mounted && !_retired &&
      _preferences.current && _sourceCurrent();
  bool get _active => _ownerCurrent && (_route?.isCurrent ?? false);

  @override
  void initState() {
    super.initState();
    _session = _preferences.session.invalidations.listen((_) => _retire());
    _access = _preferences.session.accessChanges.listen((_) {
      if (!_preferences.current) _retire();
    });
    _changes = _preferences.session.changes.listen((_) {
      if (!_preferences.current) _retire();
    });
  }
  void _retire() {
    if (_retired) return;
    _retired = true;
    ++_epoch;
    if (mounted) setState(() {});
  }
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    _route = route;
    if (route != null && route.isActive && !route.isCurrent) _retire();
    if (!_started) {
      _started = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (_active) unawaited(_read());
      });
    }
  }
  Future<void> _read() async {
    if (!_active || _busy) return;
    final epoch = ++_epoch;
    setState(() { _loading = true; _failure = null; });
    try {
      final value = await _preferences.read();
      if (!_active || epoch != _epoch) return;
      setState(() { _draft = value; _loading = false; _loaded = true; });
    } catch (_) {
      if (!_active || epoch != _epoch) return;
      setState(() { _loading = false; _loaded = false; _failure = '字号读取失败，请重试或明确恢复默认。'; });
    }
  }
  void _close(ModalRoute<dynamic>? route, NavigatorState navigator) {
    if (route != null && route.isCurrent && route.isActive &&
        identical(route.navigator, navigator) && navigator.mounted) {
      navigator.pop();
    }
  }
  Future<void> _save(ModalRoute<dynamic>? route, NavigatorState navigator) async {
    if (!_active || _loading || _busy || !_loaded) return;
    final epoch = _epoch, value = _draft;
    setState(() { _busy = true; _failure = null; }); // Synchronous repeated taps do not queue writes.
    try {
      await _preferences.save(value, sourceCurrent: () => _active && epoch == _epoch);
      if (mounted && _active && epoch == _epoch) _close(route, navigator);
    } catch (_) {
      if (mounted && _active && epoch == _epoch) {
        setState(() => _failure = '字号保存失败，已保留本次选择，请重试。');
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }
  @override
  void dispose() {
    ++_epoch;
    unawaited(_session?.cancel());
    unawaited(_access?.cancel());
    unawaited(_changes?.cancel());
    super.dispose();
  }
  @override
  Widget build(BuildContext context) {
    final route = ModalRoute.of(context), navigator = Navigator.of(context);
    final active = _active;
    return Scaffold(
      appBar: AppBar(title: const Text('聊天字号'), leading: IconButton(
        tooltip: '取消字号设置', constraints: const BoxConstraints(minWidth: 44, minHeight: 44),
        visualDensity: VisualDensity.standard,
        onPressed: () => _close(route, navigator), icon: const Icon(Icons.arrow_back))),
      body: !_ownerCurrent
        ? const Center(child: Text('会话已变化，请返回后重新打开。'))
        : SafeArea(child: Column(children: [
          Expanded(child: SingleChildScrollView(padding: const EdgeInsets.all(20),
            child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
              const Text('预览', style: TextStyle(color: AppColors.secondary)),
              const SizedBox(height: 12),
              Container(padding: const EdgeInsets.all(16),
                decoration: BoxDecoration(color: const Color(0xffdce8ff),
                  borderRadius: BorderRadius.circular(12)),
                child: ChatReadingTextScale(factor: _draft,
                  child: const Text('调整聊天文字大小\n这是一条包含较长内容的预览消息。',
                    key: ValueKey('chat-font-preview'), style: TextStyle(fontSize: 16)))),
              const SizedBox(height: 24),
              if (_loading) const LinearProgressIndicator(semanticsLabel: '正在读取聊天字号'),
              if (_failure != null) ...[
                Text(_failure!, style: const TextStyle(color: AppColors.error)),
                if (!_loaded) TextButton(onPressed: active && !_busy ? _read : null, child: const Text('重新读取')),
              ],
              Semantics(label: '聊天字号', value: '${(_draft * 100).round()}%',
                child: Slider(value: _draft, min: minimumChatFontFactor,
                  max: maximumChatFontFactor, divisions: 23,
                  label: '${(_draft * 100).round()}%',
                  onChanged: active && !_loading && !_busy && _loaded
                    ? (value) => setState(() => _draft = value) : null)),
              const Row(mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [Text('较小'), Text('标准'), Text('较大')]),
              const SizedBox(height: 12),
              TextButton(onPressed: active && !_loading && !_busy ? () => setState(() {
                _draft = defaultChatFontFactor; _failure = null; _loaded = true;
              }) : null, child: const Text('使用默认字号')),
              const Text('此设置用于当前账号的聊天文字，系统辅助字号仍然生效。',
                style: TextStyle(color: AppColors.secondary)),
            ]))),
          Padding(padding: const EdgeInsets.all(16), child: SizedBox(width: double.infinity,
            height: 52, child: FilledButton(
              onPressed: active && !_loading && !_busy && _loaded
                ? () => unawaited(_save(route, navigator)) : null,
              child: Text(_busy ? '正在保存…' : '保存字号')))),
        ])),
    );
  }
}
