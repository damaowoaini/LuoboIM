import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:webview_flutter/webview_flutter.dart';
import 'package:webview_flutter_android/webview_flutter_android.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../core/tenant/session_coordinator.dart';
import '../../../design/tokens.dart';
import '../data/native_location_once.dart';
import '../domain/location_pick.dart';
import 'chat_controller.dart';

abstract interface class LocationMapHost {
  Widget build(BuildContext context);
  Future<void> start();
  Future<bool> command(String method, Map<String, Object> arguments);
  Future<void> dispose();
}

typedef LocationMapHostFactory = LocationMapHost Function({
  required LocationPickerConfig config,
  required String nonce,
  required ValueChanged<String> candidate,
  required ValueChanged<String> status,
  required VoidCallback failed,
  required VoidCallback retired,
});

// The factory is the OS WebView boundary. Tests still exercise the real page,
// protocol parser, source lease and controller/repository SDK dispatch chain.
final class LocationPickerDependencies {
  LocationPickerDependencies({
    this.config,
    NativeLocationOnce? native,
    this.hostFactory = WebViewLocationMapHost.new,
  }) : native = native ?? MethodChannelLocationOnce();
  final LocationPickerConfig? config;
  final NativeLocationOnce native;
  final LocationMapHostFactory hostFactory;

  static LocationPickerDependencies deployment() => LocationPickerDependencies(
    config: LocationPickerConfig.fromEnvironment(),
  );
}

final class WebViewLocationMapHost implements LocationMapHost {
  WebViewLocationMapHost({
    required this.config,
    required this.nonce,
    required this.candidate,
    required this.status,
    required this.failed,
    required this.retired,
  }) : controller = WebViewController(
         onPermissionRequest: (request) => request.deny(),
       );
  final LocationPickerConfig config;
  final String nonce;
  final ValueChanged<String> candidate, status;
  final VoidCallback failed, retired;
  final WebViewController controller;
  bool _active = true, _document = false, _initialized = false;
  int _starts = 0;

  @override
  Widget build(BuildContext context) => WebViewWidget(controller: controller);

  @override
  Future<void> start() async {
    await controller.setJavaScriptMode(JavaScriptMode.unrestricted);
    if (controller.platform case final AndroidWebViewController android) {
      await android.setGeolocationPermissionsPromptCallbacks(
        onShowPrompt: (_) async =>
            const GeolocationPermissionsResponse(allow: false, retain: false),
      );
    }
    await controller.addJavaScriptChannel(
      'LuoboMapCandidateV1',
      onMessageReceived: (message) {
        if (_active && _document && _initialized) {
          candidate(message.message);
        }
      },
    );
    await controller.addJavaScriptChannel(
      'LuoboMapStateV1',
      onMessageReceived: (message) {
        if (_active && _document && _initialized) {
          status(message.message);
        }
      },
    );
    await controller.setNavigationDelegate(
      NavigationDelegate(
        onNavigationRequest: (request) {
          if (!_active || !request.isMainFrame || !config.allows(request.url)) {
            // The controlled page has frame-src none. Never grant a second
            // frame the native channel or open an external navigation.
            retired();
            return NavigationDecision.prevent;
          }
          return NavigationDecision.navigate;
        },
        onPageStarted: (url) {
          _document = false;
          if (++_starts != 1 || !config.allows(url)) {
            retired();
          }
        },
        onPageFinished: (url) async {
          if (!_active || _starts != 1 || _initialized || !config.allows(url)) {
            return;
          }
          _document = true;
          _initialized = true;
          try {
            final accepted = await command('init', {
              'requestNonce': nonce,
              'initialCenter': {
                'lng': config.longitude,
                'lat': config.latitude,
              },
            });
            if (!accepted && _active) {
              failed();
            }
          } catch (_) {
            if (_active) {
              failed();
            }
          }
        },
        onWebResourceError: (error) {
          if (_active && error.isForMainFrame != false) {
            failed();
          }
        },
        onHttpError: (error) {
          final uri = error.request?.uri ?? error.response?.uri;
          if (_active && uri != null && config.allows(uri.toString())) {
            failed();
          }
        },
      ),
    );
    if (_active) {
      await controller.loadRequest(config.uri);
    }
  }

  @override
  Future<bool> command(String method, Map<String, Object> arguments) async {
    if (!_active ||
        !_document ||
        !const {'init', 'convertGps', 'dispose'}.contains(method)) {
      return false;
    }
    final result = await controller
        .runJavaScriptReturningResult(
          'window.LuoboMapPickerV1.$method(${jsonEncode(arguments)})',
        )
        .timeout(const Duration(seconds: 5));
    Object? value = result;
    // Android/iOS may wrap a JavaScript JSON string differently. Decode at
    // most two string layers and still require the same one-field contract.
    for (var i = 0; i < 2 && value is String; ++i) {
      if (value.length > 128) {
        return false;
      }
      try {
        value = jsonDecode(value);
      } on FormatException {
        return false;
      }
    }
    return _active &&
        value is Map &&
        value.length == 1 &&
        value['accepted'] == true;
  }

  @override
  Future<void> dispose() async {
    if (!_active) {
      return;
    }
    try {
      await command('dispose', {'requestNonce': nonce});
    } catch (_) {
      // Local source retirement does not rely on executing a remote script.
    } finally {
      _active = false;
      _document = false;
    }
  }
}

class ChatLocationPickerPage extends StatefulWidget {
  const ChatLocationPickerPage({
    super.key,
    required this.controller,
    required this.access,
    required this.sourceCurrent,
    required this.dependencies,
    this.sourceChanges,
  });
  final ChatController controller;
  final MessageHistoryAccess access;
  final bool Function() sourceCurrent;
  final LocationPickerDependencies dependencies;
  final Listenable? sourceChanges;

  @override
  State<ChatLocationPickerPage> createState() => _ChatLocationPickerPageState();
}

class _ChatLocationPickerPageState extends State<ChatLocationPickerPage>
    with WidgetsBindingObserver {
  ModalRoute<dynamic>? _route;
  NavigatorState? _navigator;
  LocationMapHost? _host;
  LocationPick? _pick;
  Timer? _readyTimeout, _conversionTimeout;
  StreamSubscription<int>? _accessChanges;
  StreamSubscription<SessionSnapshot>? _ownerChanges;
  final String _intentId = newLocationNonce();
  String _nonce = '', _notice = '';
  String? _request;
  Completer<void>? _resumed;
  bool _ready = false, _retired = false, _sending = false;
  bool _foreground = true, _converting = false;
  int _load = 0, _command = 0;

  bool get _owns {
    if (!mounted || _retired || !widget.controller.usable) {
      return false;
    }
    try {
      widget.access.check();
      return widget.sourceCurrent() &&
          _route?.isActive == true &&
          _navigator?.mounted == true &&
          identical(_route?.navigator, _navigator);
    } on AppFailure {
      return false;
    }
  }

  bool get _current => _owns && _foreground && _route?.isCurrent == true;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    final state = WidgetsBinding.instance.lifecycleState;
    _foreground = state == null || state == AppLifecycleState.resumed;
    widget.controller.addListener(_changed);
    widget.sourceChanges?.addListener(_changed);
    final session = widget.controller.repository.session;
    _accessChanges = session.accessChanges.listen((_) => _changed());
    _ownerChanges = session.changes.listen((_) => _changed());
    WidgetsBinding.instance.addPostFrameCallback((_) => unawaited(_reload()));
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _route ??= ModalRoute.of(context);
    _navigator ??= Navigator.of(context);
    if (_route?.isActive == true && _route?.isCurrent != true) {
      _retire(notify: false);
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    _foreground = state == AppLifecycleState.resumed;
    if (state == AppLifecycleState.hidden ||
        state == AppLifecycleState.paused ||
        state == AppLifecycleState.detached ||
        !_foreground && _request == null) {
      _retire();
    } else if (!_foreground) {
      _resumed ??= Completer<void>();
    } else {
      if (_resumed case final resumed?) {
        if (!resumed.isCompleted) {
          resumed.complete();
        }
        _resumed = null;
      }
      if (!_owns) {
        _retire();
      }
    }
    // The native adapter owns permission-prompt versus active-fix lifetime.
    // It cancels sensors on background. No Dart continuation/convert/send is
    // admitted while non-resumed, including an owned permission prompt.
  }

  void _changed() {
    if (!_owns) {
      _retire();
    } else if (mounted) {
      setState(() {});
    }
  }

  void _retire({bool notify = true}) {
    if (_retired) {
      return;
    }
    _retired = true;
    if (_resumed case final resumed?) {
      if (!resumed.isCompleted) {
        resumed.complete();
      }
      _resumed = null;
    }
    ++_load;
    _pick = null;
    _ready = false;
    _readyTimeout?.cancel();
    _conversionTimeout?.cancel();
    if (_request case final id?) {
      unawaited(widget.dependencies.native.cancel(id));
      _request = null;
    }
    if (_host case final host?) {
      unawaited(host.dispose());
    }
    if (notify && mounted) {
      setState(() {});
    }
  }

  Future<void> _reload() async {
    if (!_current ||
        _sending ||
        widget.dependencies.config == null ||
        widget.controller.isLocationIntentUnconfirmed(_intentId)) {
      return;
    }
    final load = ++_load;
    final nonce = _nonce = newLocationNonce();
    final old = _host;
    _host = null;
    _pick = null;
    _ready = false;
    _notice = '';
    _readyTimeout?.cancel();
    _conversionTimeout?.cancel();
    setState(() {});
    await old?.dispose();
    bool current() => _current && load == _load && nonce == _nonce;
    if (!current()) {
      return;
    }
    final host = widget.dependencies.hostFactory(
      config: widget.dependencies.config!,
      nonce: nonce,
      candidate: (value) {
        if (!current() || !_ready) {
          return;
        }
        try {
          final pick = LocationPick.parse(
            value,
            nonce: nonce,
            afterSequence: _pick?.sequence ?? 0,
          );
          _conversionTimeout?.cancel();
          if (_request case final id?) {
            unawaited(widget.dependencies.native.cancel(id));
            _request = null;
          }
          setState(() {
            _pick = pick;
            _converting = false;
            _notice = '';
          });
        } on AppFailure {
          // Wrong nonce/old sequence/malformed candidate grants no action.
        }
      },
      status: (value) {
        if (!current()) {
          return;
        }
        try {
          final status = LocationMapStatus.parse(value, nonce);
          if (status.sequence < (_pick?.sequence ?? 0)) {
            return;
          }
          _readyTimeout?.cancel();
          if (!status.ready) {
            _conversionTimeout?.cancel();
          }
          setState(() {
            if (status.ready) {
              _ready = true;
              _notice = '';
            } else {
              _converting = false;
              if (status.code == 'config' || status.code == 'map_load') {
                _ready = false;
                _pick = null;
              }
              _notice = status.code == 'conversion'
                  ? '定位转换未完成，请重试或在地图上选择位置'
                  : '地图未准备好，请重新加载后再试';
            }
          });
        } on AppFailure {
          // Status has no authority to grant native privileges either.
        }
      },
      failed: () {
        if (current()) {
          _readyTimeout?.cancel();
          setState(() {
            _ready = false;
            _pick = null;
            _notice = '地图加载失败，请检查服务配置后重试';
          });
        }
      },
      retired: _retire,
    );
    setState(() => _host = host);
    _readyTimeout = Timer(const Duration(seconds: 15), () {
      if (current() && !_ready) {
        setState(() => _notice = '地图加载超时，请重新加载后再试');
      }
    });
    try {
      await host.start();
    } catch (_) {
      if (current()) {
        _readyTimeout?.cancel();
        setState(() => _notice = '地图加载失败，请稍后重试');
      }
    }
  }

  Future<void> _gps() async {
    if (!_current || !_ready || _request != null || _converting || _sending) {
      return;
    }
    final id = newLocationNonce(16), nonce = _nonce, load = _load;
    final sequence = _pick?.sequence ?? 0;
    setState(() {
      _request = id;
      _notice = '正在获取一次当前位置…';
    });
    bool current() =>
        _current && load == _load && nonce == _nonce && _request == id;
    try {
      final fix = await widget.dependencies.native.request(id);
      if (!_foreground && _owns && _request == id) {
        await (_resumed ??= Completer<void>()).future.timeout(
          const Duration(seconds: 2),
        );
      }
      if (!current() || sequence != (_pick?.sequence ?? 0)) {
        return;
      }
      _converting = true;
      final accepted = await _host!.command('convertGps', {
        'requestNonce': nonce,
        'commandId': ++_command,
        'expectedSequence': sequence,
        'lng': fix.longitude,
        'lat': fix.latitude,
      });
      if (!current()) {
        return;
      }
      if (!accepted) {
        _converting = false;
        setState(() => _notice = '位置已变化，请重新定位或手动选择');
        return;
      }
      setState(
        () =>
            _notice = fix.approximate ? '已获取大致位置，正在转换；请核对后发送' : '正在转换位置，请核对后发送',
      );
      _conversionTimeout = Timer(const Duration(seconds: 10), () {
        if (_current && load == _load && _converting) {
          setState(() {
            _converting = false;
            _notice = '定位转换超时，请重试或手动选择位置';
          });
        }
      });
    } catch (error) {
      if (error is AppFailure && error.kind == FailureKind.cancelled) {
        _retire();
      } else if (current()) {
        setState(() {
          _converting = false;
          _notice =
              error is AppFailure && error.kind == FailureKind.permissionDenied
              ? '未获得定位权限；仍可在地图上选择位置'
              : '当前位置暂不可用；请重试或在地图上选择位置';
        });
      }
    } finally {
      if (_request == id) {
        _request = null;
      }
      if (mounted) {
        setState(() {});
      }
    }
  }

  Future<void> _send() async {
    final pick = _pick;
    if (!_current ||
        !_ready ||
        pick == null ||
        _sending ||
        _request != null ||
        _converting ||
        widget.controller.isLocationIntentUnconfirmed(_intentId)) {
      return;
    }
    setState(() => _sending = true);
    final result = await widget.controller.sendLocation(
      pick,
      intentId: _intentId,
      access: widget.access,
      checkSource: () {
        if (!_current || !identical(_pick, pick)) {
          throw const AppFailure(FailureKind.cancelled);
        }
      },
    );
    if (!_current) {
      return;
    }
    setState(() {
      _sending = false;
      _notice = switch (result.kind) {
        LocationSendKind.sent => '',
        LocationSendKind.notDispatched => '尚未发送，请检查会话状态后重试',
        LocationSendKind.unconfirmed => '发送结果未确认，请返回聊天核对，勿重复发送',
      };
    });
    if (result.kind == LocationSendKind.sent && _route?.isCurrent == true) {
      _navigator!.pop();
    }
  }

  @override
  void dispose() {
    _retire(notify: false);
    WidgetsBinding.instance.removeObserver(this);
    widget.controller.removeListener(_changed);
    widget.sourceChanges?.removeListener(_changed);
    unawaited(_accessChanges?.cancel());
    unawaited(_ownerChanges?.cancel());
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    ModalRoute.of(context); // Subscribe to foreign-cover/ABA changes.
    final valid = _owns;
    final configured = widget.dependencies.config != null;
    final canSend =
        _current &&
        _ready &&
        _pick != null &&
        !_sending &&
        _request == null &&
        !_converting &&
        !widget.controller.isLocationIntentUnconfirmed(_intentId) &&
        widget.controller.conversation?.canSend == true;
    return Scaffold(
      appBar: AppBar(title: const Text('发送位置')),
      body: !valid
          ? const Center(child: Text('此操作已失效，请返回聊天后重新选择'))
          : !configured
          ? const Center(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: Text('地图服务配置不可用，暂不能选择位置'),
              ),
            )
          : SafeArea(
              child: Column(
                children: [
                  Expanded(
                    flex: 3,
                    child:
                        _host?.build(context) ??
                        const Center(child: Text('正在准备地图…')),
                  ),
                  Flexible(
                    flex: 2,
                    child: SingleChildScrollView(
                      child: Padding(
                        padding: const EdgeInsets.all(12),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [
                            if (_pick case final pick?)
                              Text(
                                pick.name,
                                style: const TextStyle(
                                  color: AppColors.text,
                                  fontWeight: FontWeight.w600,
                                ),
                              ),
                            if (_notice.isNotEmpty)
                              Semantics(
                                liveRegion: true,
                                child: Text(
                                  _notice,
                                  style: const TextStyle(
                                    color: AppColors.secondary,
                                  ),
                                ),
                              ),
                            Wrap(
                              spacing: 8,
                              runSpacing: 8,
                              children: [
                                OutlinedButton.icon(
                                  style: OutlinedButton.styleFrom(
                                    minimumSize: const Size(44, 44),
                                  ),
                                  onPressed:
                                      _current &&
                                          _ready &&
                                          !_sending &&
                                          _request == null &&
                                          !_converting
                                      ? _gps
                                      : null,
                                  icon: const Icon(Icons.my_location),
                                  label: const Text('我的位置'),
                                ),
                                TextButton(
                                  style: TextButton.styleFrom(
                                    minimumSize: const Size(44, 44),
                                  ),
                                  onPressed:
                                      _current &&
                                          !_sending &&
                                          _request == null &&
                                          !widget.controller
                                              .isLocationIntentUnconfirmed(
                                                _intentId,
                                              )
                                      ? _reload
                                      : null,
                                  child: const Text('重新加载地图'),
                                ),
                              ],
                            ),
                            FilledButton(
                              style: FilledButton.styleFrom(
                                minimumSize: const Size(44, 52),
                              ),
                              onPressed: canSend ? _send : null,
                              child: Text(_sending ? '正在发送…' : '发送此位置'),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ),
                ],
              ),
            ),
    );
  }
}
