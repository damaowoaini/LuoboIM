import 'package:flutter/services.dart';

enum AccessFailureKind {
  cancelled,
  incorrect,
  cooldown,
  unavailable,
  lockedOut,
  storage,
  stale,
}

final class AccessFailure implements Exception {
  const AccessFailure(this.kind);
  final AccessFailureKind kind;
  String get message => switch (kind) {
    AccessFailureKind.cancelled => '验证已取消，请使用解锁密码或重新验证',
    AccessFailureKind.incorrect => '解锁密码不正确',
    AccessFailureKind.cooldown => '尝试次数较多，请稍后再试',
    AccessFailureKind.unavailable => '此设备暂时无法使用生物识别，请使用解锁密码',
    AccessFailureKind.lockedOut => '生物识别已被系统锁定，请使用解锁密码',
    AccessFailureKind.storage => '无法确认屏幕锁设置，请稍后重试或退出账号',
    AccessFailureKind.stale => '此验证已失效，请重新操作',
  };
  @override
  String toString() => 'AccessFailure(${kind.name})';
}

abstract interface class NativeAccess {
  Future<bool> biometricAvailable();
  Future<String> derivePin(String pin, String salt, int iterations);
  Future<void> biometric(String owner, int request, {bool enroll = false});
  Future<void> cancel();
  Future<void> removeBiometric(String owner);
  Future<void> privacy({required bool enabled, required bool covered});
}

final class MethodChannelNativeAccess implements NativeAccess {
  const MethodChannelNativeAccess();
  static const channel = MethodChannel('luobo/app_access');
  Future<T?> _call<T>(String method, [Map<String, Object?>? arguments]) async {
    try {
      return await channel
          .invokeMethod<T>(method, arguments)
          .timeout(const Duration(seconds: 45));
    } on PlatformException catch (error) {
      throw AccessFailure(switch (error.code) {
        'cancelled' => AccessFailureKind.cancelled,
        'locked_out' => AccessFailureKind.lockedOut,
        'unavailable' => AccessFailureKind.unavailable,
        _ => AccessFailureKind.storage,
      });
    } catch (_) {
      throw const AccessFailure(AccessFailureKind.storage);
    }
  }

  @override
  Future<bool> biometricAvailable() async =>
      await _call<bool>('capabilities') == true;
  @override
  Future<String> derivePin(String pin, String salt, int iterations) async {
    final result = await _call<String>('derivePin', {
      'pin': pin,
      'salt': salt,
      'iterations': iterations,
    });
    if (result == null) throw const AccessFailure(AccessFailureKind.storage);
    return result;
  }

  @override
  Future<void> biometric(
    String owner,
    int request, {
    bool enroll = false,
  }) async {
    try {
      final result = await _call<int>(enroll ? 'enroll' : 'authenticate', {
        'owner': owner,
        'request': request,
      });
      if (result != request) throw const AccessFailure(AccessFailureKind.stale);
    } catch (_) {
      await cancel();
      rethrow;
    }
  }

  @override
  Future<void> cancel() async {
    try {
      await channel.invokeMethod<void>('cancel');
    } catch (_) {}
  }

  @override
  Future<void> removeBiometric(String owner) async {
    await _call<void>('remove', {'owner': owner});
  }

  @override
  Future<void> privacy({required bool enabled, required bool covered}) async {
    await _call<void>('privacy', {'enabled': enabled, 'covered': covered});
  }
}
