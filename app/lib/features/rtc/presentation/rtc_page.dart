import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:livekit_client/livekit_client.dart' as lk;

import '../data/livekit_media_session.dart';
import '../domain/rtc_call.dart';
import '../domain/rtc_coordinator.dart';
import 'rtc_providers.dart';

String _endLabel(RtcCoordinator call) => switch (call.endAction) {
  RtcEndAction.reject => '拒绝',
  RtcEndAction.cancel => '取消呼叫',
  RtcEndAction.hangUp => '挂断',
};

/// Mounted once around the router, so incoming invitations work on any page.
class RtcIncomingHost extends ConsumerStatefulWidget {
  const RtcIncomingHost({
    super.key,
    required this.child,
    required this.onOpenCall,
    this.showOngoingBar = true,
  });
  final Widget child;
  final VoidCallback onOpenCall;
  final bool showOngoingBar;
  @override
  ConsumerState<RtcIncomingHost> createState() => _RtcIncomingHostState();
}

class _RtcIncomingHostState extends ConsumerState<RtcIncomingHost>
    with WidgetsBindingObserver {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    ref
        .read(rtcCoordinatorProvider)
        .background(state != AppLifecycleState.resumed);
  }

  @override
  Widget build(BuildContext context) {
    final call = ref.watch(rtcCoordinatorProvider);
    final visible = call.phase == RtcPhase.incoming;
    return Stack(
      children: [
        widget.child,
        if (visible && widget.showOngoingBar)
          Positioned(
            top: 0,
            left: 0,
            right: 0,
            child: SafeArea(
              child: Material(
                color: const Color(0xff143958),
                elevation: 12,
                child: Padding(
                  padding: const EdgeInsets.all(16),
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        '${call.title} 邀请你${call.call!.video ? '视频' : '语音'}通话',
                        style: const TextStyle(
                          color: Colors.white,
                          fontSize: 17,
                        ),
                      ),
                      const SizedBox(height: 12),
                      Wrap(
                        spacing: 12,
                        children: [
                          OutlinedButton(
                            onPressed: () => unawaited(call.hangUp()),
                            style: OutlinedButton.styleFrom(
                              foregroundColor: Colors.white,
                            ),
                            child: const Text('拒绝'),
                          ),
                          FilledButton(
                            onPressed: () {
                              widget.onOpenCall();
                              unawaited(call.accept());
                            },
                            child: const Text('接听'),
                          ),
                        ],
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ),
        if (call.busy && !visible && widget.showOngoingBar)
          Positioned(
            top: 0,
            left: 16,
            right: 16,
            child: SafeArea(
              child: Material(
                color: const Color(0xffe7f3ff),
                child: ListTile(
                  dense: true,
                  leading: const Icon(Icons.call),
                  title: const Text('通话正在进行'),
                  onTap: widget.onOpenCall,
                  trailing: IconButton(
                    tooltip: _endLabel(call),
                    icon: const Icon(Icons.call_end),
                    onPressed: () => unawaited(call.hangUp()),
                  ),
                ),
              ),
            ),
          ),
      ],
    );
  }
}

class RtcPage extends ConsumerStatefulWidget {
  const RtcPage({super.key});
  @override
  ConsumerState<RtcPage> createState() => _RtcPageState();
}

class _RtcPageState extends ConsumerState<RtcPage> {
  Timer? _clock;
  @override
  void initState() {
    super.initState();
    _clock = Timer.periodic(const Duration(seconds: 1), (_) {
      if (mounted) setState(() {});
    });
  }

  @override
  void dispose() {
    _clock?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final call = ref.watch(rtcCoordinatorProvider);
    final media = call.media;
    return Scaffold(
      backgroundColor: const Color(0xff102638),
      appBar: AppBar(
        title: const Text('通话'),
        backgroundColor: const Color(0xff102638),
        foregroundColor: Colors.white,
      ),
      body: SafeArea(
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                children: [
                  Text(
                    call.title.isEmpty ? '通话' : call.title,
                    style: const TextStyle(color: Colors.white, fontSize: 22),
                    textAlign: TextAlign.center,
                  ),
                  const SizedBox(height: 10),
                  Text(
                    _status(call),
                    style: const TextStyle(color: Colors.white70),
                    textAlign: TextAlign.center,
                  ),
                  if (call.failure case final failure?)
                    Text(
                      failure.message,
                      style: const TextStyle(color: Colors.orangeAccent),
                    ),
                ],
              ),
            ),
            Expanded(
              child: (media?.peers.isEmpty ?? true)
                  ? const Center(
                      child: Icon(
                        Icons.person,
                        size: 88,
                        color: Colors.white54,
                      ),
                    )
                  : GridView.count(
                      crossAxisCount: media!.peers.length > 2 ? 2 : 1,
                      childAspectRatio: media.peers.length > 2 ? 0.9 : 1.3,
                      children: media.peers
                          .map((peer) => _PeerTile(peer, media))
                          .toList(),
                    ),
            ),
            if (call.reason == RtcEndReason.permissionDenied)
              TextButton(
                onPressed: () => unawaited(call.permissions.openSettings()),
                child: const Text('前往设置开启麦克风或相机权限'),
              ),
            Padding(
              padding: const EdgeInsets.all(20),
              child: Wrap(
                alignment: WrapAlignment.center,
                spacing: 12,
                runSpacing: 12,
                children: [
                  if (media != null && call.busy) ...[
                    _control(
                      media.microphone ? Icons.mic : Icons.mic_off,
                      '麦克风',
                      call.toggleMicrophone,
                    ),
                    _control(
                      media.speaker ? Icons.volume_up : Icons.hearing,
                      '扬声器',
                      call.toggleSpeaker,
                    ),
                    if (call.call?.video ?? false) ...[
                      _control(
                        media.camera ? Icons.videocam : Icons.videocam_off,
                        '摄像头',
                        call.toggleCamera,
                      ),
                      _control(
                        Icons.flip_camera_ios,
                        '切换镜头',
                        call.switchCamera,
                      ),
                    ],
                  ],
                  if (call.phase == RtcPhase.incoming)
                    FilledButton.icon(
                      onPressed: () => unawaited(call.accept()),
                      icon: const Icon(Icons.call),
                      label: const Text('接听'),
                    ),
                  if (call.busy)
                    FilledButton.icon(
                      style: FilledButton.styleFrom(
                        backgroundColor: Colors.red,
                      ),
                      onPressed: () => unawaited(call.hangUp()),
                      icon: const Icon(Icons.call_end),
                      label: Text(_endLabel(call)),
                    )
                  else
                    FilledButton(
                      onPressed: () => context.pop(),
                      child: const Text('返回'),
                    ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _control(
    IconData icon,
    String label,
    Future<void> Function() action,
  ) => Column(
    mainAxisSize: MainAxisSize.min,
    children: [
      IconButton.filledTonal(
        tooltip: label,
        onPressed: () => unawaited(action()),
        icon: Icon(icon),
      ),
      Text(label, style: const TextStyle(color: Colors.white70)),
    ],
  );

  String _status(RtcCoordinator call) => switch (call.phase) {
    RtcPhase.idle => '暂无通话',
    RtcPhase.requestingPermission => '等待麦克风或相机授权',
    RtcPhase.outgoing => '正在呼叫…',
    RtcPhase.incoming => '等待接听',
    RtcPhase.connecting => '正在连接…',
    RtcPhase.reconnecting => '网络中断，正在重连…',
    RtcPhase.active => () {
      final seconds = DateTime.now()
          .difference(call.connectedAt ?? DateTime.now())
          .inSeconds;
      return '${(seconds ~/ 60).toString().padLeft(2, '0')}:${(seconds % 60).toString().padLeft(2, '0')}';
    }(),
    RtcPhase.ended => switch (call.reason) {
      RtcEndReason.cancelled => '通话已取消',
      RtcEndReason.declined => '通话已拒绝',
      RtcEndReason.timedOut => '无人接听',
      RtcEndReason.hungUp => '通话已结束',
      RtcEndReason.busy => '对方忙线',
      RtcEndReason.otherDevice => '已在其他设备处理',
      RtcEndReason.permissionDenied => '需要麦克风或相机权限',
      RtcEndReason.sessionChanged => '账号状态已改变，通话已结束',
      RtcEndReason.disconnected => '连接已断开',
      _ => '通话未能建立',
    },
  };
}

class _PeerTile extends StatelessWidget {
  const _PeerTile(this.peer, this.media);
  final RtcPeer peer;
  final RtcMediaSession media;
  @override
  Widget build(BuildContext context) {
    final track = media is LiveKitMediaSession
        ? (media as LiveKitMediaSession).videoTrack(peer.id)
        : null;
    return Card(
      color: const Color(0xff25475f),
      clipBehavior: Clip.antiAlias,
      child: Stack(
        fit: StackFit.expand,
        children: [
          if (track != null && peer.camera)
            lk.VideoTrackRenderer(track, fit: lk.VideoViewFit.cover)
          else
            const Center(
              child: Icon(Icons.person, size: 70, color: Colors.white54),
            ),
          Align(
            alignment: Alignment.bottomLeft,
            child: Container(
              color: Colors.black54,
              padding: const EdgeInsets.all(8),
              child: Text(
                '${peer.name}${peer.local ? '（我）' : ''}${peer.microphone ? '' : ' · 静音'}',
                style: const TextStyle(color: Colors.white),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
