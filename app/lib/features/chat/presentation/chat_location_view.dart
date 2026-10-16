import 'dart:async';

import 'package:flutter/material.dart';

import '../../../core/error/app_failure.dart';
import '../../../design/tokens.dart';
import '../domain/chat_location.dart';
import 'chat_content_access.dart';
import 'message_privacy_content.dart';

/// Receiving is entirely local. Never load description.url, locate the device,
/// or hand coordinates to an external service simply to show a received card.
class ChatLocationView extends StatefulWidget {
  const ChatLocationView({
    super.key,
    required this.location,
    required this.captureAccess,
    required this.changes,
  });
  final ChatLocation? location;
  final ChatContentAccess Function(bool Function()) captureAccess;
  final Listenable changes;
  @override
  State<ChatLocationView> createState() => _ChatLocationViewState();
}

class _ChatLocationViewState extends State<ChatLocationView>
    with WidgetsBindingObserver {
  ModalRoute<dynamic>? _origin;
  MaterialPageRoute<void>? _owned;
  int _turn = 0;
  final _lifetime = StreamController<void>.broadcast();
  bool _foreground = true;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _origin = ModalRoute.of(context);
    // Our own detail may cover the source card. Any other cover consumes the
    // original render callback; returning never renews that callback's ticket.
    if (_origin?.isCurrent != true && _owned?.isActive != true) {
      _retire();
    }
  }

  void _retire() {
    ++_turn;
    _lifetime.add(null);
  }

  @override
  void didUpdateWidget(covariant ChatLocationView oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.location != widget.location) {
      _retire();
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (!mounted) {
      return;
    }
    setState(() {
      _foreground = state == AppLifecycleState.resumed;
      if (!_foreground) {
        _retire();
      }
    });
  }

  @override
  void dispose() {
    _retire();
    unawaited(_lifetime.close());
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final location = widget.location;
    if (location == null) {
      return const Text('位置信息暂不可用');
    }
    final origin = _origin, navigator = _origin?.navigator;
    var turn = _turn;
    var consumed = false;
    MaterialPageRoute<void>? detail;
    final scaler = MediaQuery.textScalerOf(context), changes = widget.changes;
    bool current() =>
        mounted &&
        _foreground &&
        turn == _turn &&
        origin?.isActive == true &&
        navigator?.mounted == true &&
        identical(origin?.navigator, navigator) &&
        (detail == null ? origin?.isCurrent == true : detail!.isCurrent);
    ChatContentAccess? captured;
    try {
      // Capture at render, not from a delayed retained onTap. In particular,
      // a completed clear cannot grant an old card a fresh history cut.
      captured = widget.captureAccess(current);
    } on AppFailure {
      // A stale owner/history cut is unavailable, never renewed here.
    }
    final access = captured;
    if (access == null || !access.canView) {
      return const Text('消息内容已不可用');
    }
    void open() {
      if (consumed || !current() || !access.canView || navigator == null) {
        return;
      }
      consumed = true;
      turn = ++_turn;
      final route = MaterialPageRoute<void>(
        builder: (_) => ChatLocationDetail(
          location: location,
          access: access,
          changes: changes,
          sourceChanges: _lifetime.stream,
          textScaler: scaler,
        ),
      );
      detail = route;
      _owned = route;
      unawaited(
        navigator.push<void>(route).whenComplete(() {
          if (identical(_owned, route)) {
            _owned = null;
          }
        }),
      );
    }

    return Semantics(
      button: true,
      label: '查看位置详情',
      child: InkWell(
        onTap: open,
        borderRadius: BorderRadius.circular(12),
        child: ConstrainedBox(
          constraints: const BoxConstraints(
            minWidth: 44,
            minHeight: 44,
            maxWidth: 240,
          ),
          child: Padding(
            padding: const EdgeInsets.all(12),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                const Icon(
                  Icons.location_on_outlined,
                  color: AppColors.primary,
                ),
                const SizedBox(height: 6),
                Text(
                  location.name,
                  maxLines: 3,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(fontWeight: FontWeight.w600),
                ),
                if (location.address.isNotEmpty) ...[
                  const SizedBox(height: 4),
                  Text(
                    location.address,
                    maxLines: 3,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
                const SizedBox(height: 6),
                const Text(
                  '位置 · 查看详情',
                  style: TextStyle(color: AppColors.secondary, fontSize: 12),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class ChatLocationDetail extends StatefulWidget {
  const ChatLocationDetail({
    super.key,
    required this.location,
    required this.access,
    required this.changes,
    required this.sourceChanges,
    required this.textScaler,
  });
  final ChatLocation location;
  final ChatContentAccess access;
  final Listenable changes;
  final Stream<void> sourceChanges;
  final TextScaler textScaler;
  @override
  State<ChatLocationDetail> createState() => _ChatLocationDetailState();
}

class _ChatLocationDetailState extends State<ChatLocationDetail>
    with WidgetsBindingObserver {
  final _subscriptions = <StreamSubscription<dynamic>>[];
  bool _retired = false;
  ModalRoute<dynamic>? _route;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    widget.changes.addListener(_changed);
    final session = widget.access.repository.session;
    _subscriptions.addAll([
      widget.sourceChanges.listen((_) => _changed()),
      session.invalidations.listen((_) => _changed()),
      session.changes.listen((_) => _changed()),
      session.accessChanges.listen((_) => _changed()),
      widget.access.repository.gateway.events.listen((_) => _changed()),
    ]);
  }

  void _changed() {
    if (!mounted) {
      return;
    }
    setState(() {
      if (!widget.access.canView) {
        _retired = true;
      }
    });
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _route = ModalRoute.of(context);
    if (_route?.isCurrent != true) {
      _retired = true;
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state != AppLifecycleState.resumed) {
      setState(() => _retired = true);
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    widget.changes.removeListener(_changed);
    for (final subscription in _subscriptions) {
      unawaited(subscription.cancel());
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final route = _route, navigator = _route?.navigator;
    final available = !_retired && widget.access.canView;
    return MediaQuery(
      data: MediaQuery.of(context).copyWith(textScaler: widget.textScaler),
      child: Scaffold(
        appBar: AppBar(
          title: const Text('位置详情'),
          leading: IconButton(
            tooltip: '返回',
            constraints: const BoxConstraints(minWidth: 44, minHeight: 44),
            visualDensity: VisualDensity.standard,
            style: IconButton.styleFrom(
              tapTargetSize: MaterialTapTargetSize.padded,
            ),
            onPressed: () {
              // No ancestor lookup from a retained/deactivated callback, and
              // never pop a foreign route which covered this owned detail.
              if (route != null &&
                  route.isCurrent &&
                  route.isActive &&
                  navigator != null &&
                  navigator.mounted &&
                  identical(route.navigator, navigator)) {
                navigator.pop();
              }
            },
            icon: const Icon(Icons.arrow_back),
          ),
        ),
        body: !available
            ? const Center(child: Text('消息内容已不可用'))
            : MessagePrivacyContent(
                privacy: widget.access.privacy,
                now: widget.access.repository.now,
                expired: const Center(child: Text('消息内容已不可用')),
                child: SafeArea(
                  child: SingleChildScrollView(
                    padding: const EdgeInsets.all(20),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        const Icon(
                          Icons.location_on_outlined,
                          size: 44,
                          color: AppColors.primary,
                        ),
                        const SizedBox(height: 16),
                        Text(
                          widget.location.name,
                          style: Theme.of(context).textTheme.titleLarge,
                        ),
                        if (widget.location.address.isNotEmpty) ...[
                          const SizedBox(height: 12),
                          Text(widget.location.address),
                        ],
                        const SizedBox(height: 20),
                        Text('纬度：${widget.location.latitude}'),
                        const SizedBox(height: 8),
                        Text('经度：${widget.location.longitude}'),
                        const SizedBox(height: 12),
                        Text(
                          widget.location.coordinateSystem == null
                              ? '消息未标注坐标系'
                              : '消息标注坐标系：${widget.location.coordinateSystem}',
                          style: const TextStyle(color: AppColors.secondary),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
      ),
    );
  }
}
