import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/access/app_access.dart';
import '../../../core/access/native_access.dart';
import '../../../providers.dart';
import '../../rtc/domain/rtc_call.dart';
import '../../rtc/presentation/rtc_providers.dart';

/// Outside the Router and all its modal/RTC layers. Locked content is removed
/// from both the widget and accessibility trees, not merely made transparent.
class AppAccessHost extends ConsumerStatefulWidget {
  const AppAccessHost({super.key, required this.child});
  final Widget child;
  @override
  ConsumerState<AppAccessHost> createState() => _AppAccessHostState();
}

class _AppAccessHostState extends ConsumerState<AppAccessHost>
    with WidgetsBindingObserver {
  bool inactive = false;
  bool revealing = false;
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    final access = ref.read(appAccessProvider);
    if (state == AppLifecycleState.hidden ||
        state == AppLifecycleState.paused ||
        state == AppLifecycleState.detached) {
      access.background(true);
    } else if (state == AppLifecycleState.resumed) {
      access.background(false);
    }
    setState(() => inactive = state != AppLifecycleState.resumed);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final access = ref.watch(appAccessProvider);
    if (!inactive && (access.enabled || access.blocksContent) && !revealing) {
      revealing = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        revealing = false;
        if (mounted && !inactive) {
          unawaited(access.revealNativeCover().catchError((Object _) {}));
        }
      });
    }
    final content = access.blocksContent
        ? Navigator(
            key: ValueKey('unlock.${access.owner}.${access.revision}'),
            onGenerateRoute: (_) =>
                MaterialPageRoute<void>(builder: (_) => const AppUnlockPage()),
          )
        : widget.child;
    if (inactive && (access.enabled || access.blocksContent)) {
      return Stack(
        children: [
          ExcludeSemantics(child: IgnorePointer(child: content)),
          const Positioned.fill(child: ColoredBox(color: Color(0xfff4f5f7))),
        ],
      );
    }
    return content;
  }
}

class AppUnlockPage extends ConsumerStatefulWidget {
  const AppUnlockPage({super.key});
  @override
  ConsumerState<AppUnlockPage> createState() => _AppUnlockPageState();
}

class _AppUnlockPageState extends ConsumerState<AppUnlockPage> {
  final pin = TextEditingController();
  String? notice;
  bool leaving = false;
  @override
  void dispose() {
    pin.dispose();
    super.dispose();
  }

  Future<void> verify({bool biometric = false}) async {
    final access = ref.read(appAccessProvider);
    if (access.busy || leaving) return;
    setState(() => notice = null);
    final value = pin.text;
    pin.clear();
    try {
      if (biometric) {
        await access.unlockBiometric();
      } else {
        await access.unlockPin(value);
      }
    } catch (error) {
      if (mounted) {
        setState(
          () => notice = error is AccessFailure ? error.message : '验证未能完成，请重试',
        );
      }
    }
  }

  Future<void> logout() async {
    if (leaving) return;
    setState(() => leaving = true);
    pin.clear();
    try {
      await ref.read(appRuntimeProvider).session.signOut();
    } catch (_) {
      if (mounted) setState(() => notice = '退出结果尚未确认，请核对后再试');
    } finally {
      if (mounted) setState(() => leaving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final access = ref.watch(appAccessProvider);
    final waiting = access.busy || leaving;
    return PopScope(
      canPop: false,
      child: Scaffold(
        appBar: AppBar(
          title: const Text('屏幕锁'),
          automaticallyImplyLeading: false,
        ),
        body: SafeArea(
          child: Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 420),
              child: SingleChildScrollView(
                padding: const EdgeInsets.all(24),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const Icon(Icons.lock_outline, size: 48),
                    const SizedBox(height: 16),
                    const Text('验证后继续使用萝卜对话'),
                    if (access.enabled) ...[
                      const SizedBox(height: 20),
                      TextField(
                        controller: pin,
                        obscureText: true,
                        enableSuggestions: false,
                        autocorrect: false,
                        enableInteractiveSelection: false,
                        maxLength: 12,
                        keyboardType: TextInputType.number,
                        inputFormatters: [
                          FilteringTextInputFormatter.digitsOnly,
                        ],
                        enabled: !waiting,
                        decoration: const InputDecoration(
                          labelText: '解锁密码',
                          hintText: '6–12位数字',
                        ),
                        onSubmitted: (_) => unawaited(verify()),
                      ),
                      FilledButton(
                        onPressed: waiting ? null : () => unawaited(verify()),
                        child: const Text('解锁'),
                      ),
                      if (access.biometricEnabled)
                        TextButton.icon(
                          onPressed: waiting
                              ? null
                              : () => unawaited(verify(biometric: true)),
                          icon: const Icon(Icons.fingerprint),
                          label: const Text('使用生物识别'),
                        ),
                    ] else if (access.phase == AppAccessPhase.checking)
                      const Padding(
                        padding: EdgeInsets.all(20),
                        child: CircularProgressIndicator(),
                      )
                    else
                      TextButton(
                        onPressed: waiting
                            ? null
                            : () => unawaited(
                                ref.read(appRuntimeProvider).retry(),
                              ),
                        child: const Text('重试读取屏幕锁设置'),
                      ),
                    if (waiting) const LinearProgressIndicator(),
                    if (notice ?? access.failure?.message
                        case final String message)
                      Padding(
                        padding: const EdgeInsets.all(12),
                        child: Text(message),
                      ),
                    const _LockedCallControls(),
                    TextButton(
                      onPressed: leaving ? null : logout,
                      child: const Text('退出账号'),
                    ),
                    const Text(
                      '解锁密码仅保护本机应用访问。忘记时可退出并重新验证账号；现有聊天记录会保留。',
                      textAlign: TextAlign.center,
                    ),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _LockedCallControls extends ConsumerWidget {
  const _LockedCallControls();
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final call = ref.watch(rtcCoordinatorProvider);
    if (!call.busy) return const SizedBox.shrink();
    final incoming = call.phase == RtcPhase.incoming;
    return Column(
      children: [
        Text(incoming ? '有通话邀请，解锁后可接听' : '通话正在进行'),
        TextButton.icon(
          onPressed: () => unawaited(call.hangUp()),
          icon: const Icon(Icons.call_end),
          label: Text(incoming ? '拒绝通话' : '结束通话'),
        ),
      ],
    );
  }
}
